package main

import (
	"math/rand"

	"aiwatt.net/ems/go-common/zaplog"
)

// BatteryUnit 表示一套电池单元 = 1 PCS slave + 1 BMS slave。
// PCS 和 BMS 在物理上紧密耦合（PCS 启动前必须 BMS 合闸），
// 共享一份能量、SOC、功率指令等状态。
type BatteryUnit struct {
	pcs *SlaveBank
	bms *SlaveBank

	// 配置（不可变）
	ratedCapacityKWh  float64
	ratedPowerKW      float64
	soh               float64
	batteryVoltageNom float64
	gridVoltage       float64
	clusterCount      int

	// 动态状态
	currentEnergyKWh     float64
	pcsRunning           bool
	bmsHVClosed          bool
	remoteMode           bool
	gridTied             bool
	actualPowerKW        float64
	lastPowerCmdRaw      uint16
	lastPowerCmdAliasRaw uint16

	// 累计电量
	totalChargeKWh      float64
	totalDischargeKWh   float64
	sessionChargeKWh    float64
	sessionDischargeKWh float64
}

// NewBatteryUnit 构造一套电池单元，并初始化两个 slave bank 的默认寄存器值。
func NewBatteryUnit(cfg BatteryUnitConfig, gridVoltage float64, pcs, bms *SlaveBank) *BatteryUnit {
	bu := &BatteryUnit{
		pcs:               pcs,
		bms:               bms,
		ratedCapacityKWh:  cfg.RatedCapacityKWh,
		ratedPowerKW:      cfg.RatedPowerKW,
		soh:               cfg.SOH,
		batteryVoltageNom: cfg.BatteryVoltage,
		gridVoltage:       gridVoltage,
		clusterCount:      cfg.ClusterCount,
		currentEnergyKWh:  cfg.RatedCapacityKWh * cfg.InitialSOC / 100.0,
		remoteMode:        true,
		gridTied:          true,
		bmsHVClosed:       true,
		pcsRunning:        true,
	}

	// 默认控制寄存器值
	pcs.WriteU16(RegPCSRemoteLocal, 1) // remote
	pcs.WriteU16(RegPCSGridMode, 0)    // grid-tied
	pcs.WriteU16(RegPCSRunMode, 2)     // constant power

	zaplog.Infof("BMS[%d] HV contactor closed (auto startup)", bms.SlaveID)
	zaplog.Infof("PCS[%d] started (auto startup)", pcs.SlaveID)

	return bu
}

func (bu *BatteryUnit) ActualPowerKW() float64 { return bu.actualPowerKW }

// SOC 返回当前电量百分比 (0-100)。
func (bu *BatteryUnit) SOC() float64 {
	if bu.ratedCapacityKWh == 0 {
		return 0
	}
	return bu.currentEnergyKWh / bu.ratedCapacityKWh * 100.0
}

// BatteryVoltage 按 SOC 在 90%~110% 标称电压之间插值。
func (bu *BatteryUnit) BatteryVoltage() float64 {
	return bu.batteryVoltageNom * (0.9 + 0.2*bu.SOC()/100.0)
}

// ---- 写回调：写入瞬间触发的副作用 ----

// OnPCSWrite 处理 PCS slave 的寄存器写。
// 已在 sim.mu 保护下调用，bank 也已经更新。
// 功率指令 alias 的"最新写入优先"逻辑在这里处理。
func (bu *BatteryUnit) OnPCSWrite(addr, value uint16) {
	switch addr {
	case RegPCSPowerCmd:
		bu.pcs.WriteU16(RegPCSPowerCmdAlias, value)
		bu.lastPowerCmdRaw = value
		bu.lastPowerCmdAliasRaw = value
	case RegPCSPowerCmdAlias:
		bu.pcs.WriteU16(RegPCSPowerCmd, value)
		bu.lastPowerCmdRaw = value
		bu.lastPowerCmdAliasRaw = value
	}
}

// OnBMSWrite 处理 BMS slave 的寄存器写。当前 BMS 控制脉冲在 Tick 里清零。
func (bu *BatteryUnit) OnBMSWrite(addr, value uint16) {
	_ = addr
	_ = value
}

// ---- 控制处理（每 Tick 调用） ----

func (bu *BatteryUnit) ProcessBMSControls() {
	if bu.bms.ReadU16(RegBMSCloseHV) == 1 {
		if !bu.bmsHVClosed {
			zaplog.Infof("BMS[%d] HV contactor closed", bu.bms.SlaveID)
		}
		bu.bmsHVClosed = true
		bu.bms.WriteU16(RegBMSCloseHV, 0)
	}
	if bu.bms.ReadU16(RegBMSOpenHV) == 1 {
		if bu.bmsHVClosed {
			zaplog.Infof("BMS[%d] HV contactor opened", bu.bms.SlaveID)
		}
		bu.bmsHVClosed = false
		bu.actualPowerKW = 0
		bu.bms.WriteU16(RegBMSOpenHV, 0)
	}
	if bu.bms.ReadU16(RegBMSFaultReset) == 1 {
		zaplog.Infof("BMS[%d] fault reset", bu.bms.SlaveID)
		bu.bms.WriteU16(RegBMSFaultReset, 0)
	}
}

func (bu *BatteryUnit) ProcessPCSControls() {
	bu.remoteMode = bu.pcs.ReadU16(RegPCSRemoteLocal) == 1
	bu.gridTied = bu.pcs.ReadU16(RegPCSGridMode) == 0

	if bu.pcs.ReadU16(RegPCSStartup) == 1 {
		if !bu.bmsHVClosed {
			zaplog.Warnf("PCS[%d] startup failed: BMS HV not closed, DC undervoltage fault", bu.pcs.SlaveID)
		} else if !bu.pcsRunning {
			zaplog.Infof("PCS[%d] started", bu.pcs.SlaveID)
			bu.pcsRunning = true
		}
		bu.pcs.WriteU16(RegPCSStartup, 0)
	}
	if bu.pcs.ReadU16(RegPCSShutdown) == 1 {
		if bu.pcsRunning {
			zaplog.Infof("PCS[%d] shutdown", bu.pcs.SlaveID)
		}
		bu.pcsRunning = false
		bu.actualPowerKW = 0
		bu.pcs.WriteU16(RegPCSShutdown, 0)
	}
	if bu.pcs.ReadU16(RegPCSEStop) == 1 {
		zaplog.Warnf("PCS[%d] emergency stop", bu.pcs.SlaveID)
		bu.pcsRunning = false
		bu.actualPowerKW = 0
		bu.pcs.WriteU16(RegPCSEStop, 0)
	}
	if bu.pcs.ReadU16(RegPCSFaultReset) == 1 {
		zaplog.Infof("PCS[%d] fault reset", bu.pcs.SlaveID)
		bu.pcs.WriteU16(RegPCSFaultReset, 0)
	}
}

func (bu *BatteryUnit) ProcessPowerCommand() {
	cmdRaw := bu.syncPowerCommandRegisters()
	if bu.pcsRunning && bu.remoteMode && bu.bmsHVClosed {
		cmdPowerKW := float64(uint16ToInt16(cmdRaw)) * 0.1
		if cmdPowerKW > bu.ratedPowerKW {
			cmdPowerKW = bu.ratedPowerKW
		}
		if cmdPowerKW < -bu.ratedPowerKW {
			cmdPowerKW = -bu.ratedPowerKW
		}
		// ±0.5% 抖动，模拟真实功率跟踪误差
		jitter := 1.0 + (rand.Float64()*0.01 - 0.005)
		bu.actualPowerKW = cmdPowerKW * jitter
	} else if !bu.remoteMode {
		// 就地模式：保持当前功率不变
	} else {
		bu.actualPowerKW = 0
	}
}

// syncPowerCommandRegisters 处理 30010 和 3010 的别名同步：
// 取上一 tick 后被改动过的那个寄存器值作为最新指令。
func (bu *BatteryUnit) syncPowerCommandRegisters() uint16 {
	cmdRaw := bu.pcs.ReadU16(RegPCSPowerCmd)
	aliasRaw := bu.pcs.ReadU16(RegPCSPowerCmdAlias)

	cmdChanged := cmdRaw != bu.lastPowerCmdRaw
	aliasChanged := aliasRaw != bu.lastPowerCmdAliasRaw

	selected := cmdRaw
	if aliasChanged && !cmdChanged {
		selected = aliasRaw
	}
	bu.pcs.WriteU16(RegPCSPowerCmd, selected)
	bu.pcs.WriteU16(RegPCSPowerCmdAlias, selected)
	bu.lastPowerCmdRaw = selected
	bu.lastPowerCmdAliasRaw = selected
	return selected
}

// UpdateEnergy 按当前功率推进电量；命中 SOC 边界则截断功率。
func (bu *BatteryUnit) UpdateEnergy(dtSeconds float64) {
	if bu.actualPowerKW == 0 {
		return
	}
	soc := bu.SOC()
	if bu.actualPowerKW > 0 && soc >= 100.0 {
		bu.actualPowerKW = 0
		return
	}
	if bu.actualPowerKW < 0 && soc <= 0.0 {
		bu.actualPowerKW = 0
		return
	}

	deltaEnergy := bu.actualPowerKW * dtSeconds / 3600.0
	bu.currentEnergyKWh += deltaEnergy

	if deltaEnergy > 0 {
		bu.totalChargeKWh += deltaEnergy
		bu.sessionChargeKWh += deltaEnergy
	} else if deltaEnergy < 0 {
		bu.totalDischargeKWh += -deltaEnergy
		bu.sessionDischargeKWh += -deltaEnergy
	}

	if bu.currentEnergyKWh < 0 {
		bu.currentEnergyKWh = 0
		bu.actualPowerKW = 0
	}
	if bu.currentEnergyKWh > bu.ratedCapacityKWh {
		bu.currentEnergyKWh = bu.ratedCapacityKWh
		bu.actualPowerKW = 0
	}
}

func boolToU16(v bool) uint16 {
	if v {
		return 1
	}
	return 0
}
