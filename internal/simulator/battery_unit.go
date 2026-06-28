package simulator

import (
	"math"
	"math/rand"

	"virtual_bess/internal/zaplog"
)

// BatteryUnit 表示一套电池单元 = 1 PCS slave + 1 BMS slave。
// PCS 和 BMS 在物理上紧密耦合（PCS 启动前必须 BMS 合闸），
// 共享一份能量、SOC、功率指令等状态。
type BatteryUnit struct {
	pcs *SlaveBank
	bms *SlaveBank

	// 配置（不可变）
	ratedCapacityKWh   float64
	ratedPowerKW       float64
	soh                float64
	batteryVoltageFull float64
	pcsACVoltage       float64
	clusterCount       int

	// 动态状态
	currentEnergyKWh     float64
	pcsRunning           bool
	bmsHVClosed          bool
	remoteMode           bool
	gridTied             bool
	actualPowerKW        float64
	lastPowerCmdRaw      uint16
	lastPowerCmdAliasRaw uint16
	// PCS DC 欠压故障（HV 未合时尝试开机，或运行中 HV 被拉开）；置位后需通过故障复位清除
	pcsDCUnderVoltFault bool

	// 累计电量
	totalChargeKWh      float64
	totalDischargeKWh   float64
	sessionChargeKWh    float64
	sessionDischargeKWh float64
}

// NewBatteryUnit 构造一套电池单元，并初始化两个 slave bank 的默认寄存器值。
func NewBatteryUnit(cfg BatteryUnitConfig, pcsACVoltage float64, pcs, bms *SlaveBank) *BatteryUnit {
	bu := &BatteryUnit{
		pcs:                pcs,
		bms:                bms,
		ratedCapacityKWh:   cfg.RatedCapacityKWh,
		ratedPowerKW:       cfg.RatedPowerKW,
		soh:                cfg.SOH,
		batteryVoltageFull: cfg.BatteryVoltageFull,
		pcsACVoltage:       pcsACVoltage,
		clusterCount:       cfg.ClusterCount,
		currentEnergyKWh:   cfg.RatedCapacityKWh * cfg.InitialSOC / 100.0,
		remoteMode:         true,
		gridTied:           true,
		bmsHVClosed:        true,
		pcsRunning:         true,
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

func (bu *BatteryUnit) PCSSlaveID() uint8 { return bu.pcs.SlaveID }

func (bu *BatteryUnit) RatedPowerKW() float64 { return bu.ratedPowerKW }

func (bu *BatteryUnit) PCSBank() *SlaveBank { return bu.pcs }

func (bu *BatteryUnit) BMSBank() *SlaveBank { return bu.bms }

// PcsDCUnderVoltFault 暴露 PCS 直流侧欠压故障标志，供 61850 服务端置位告警点。
func (bu *BatteryUnit) PcsDCUnderVoltFault() bool { return bu.pcsDCUnderVoltFault }

// SOC 返回当前电量百分比 (0-100)。
func (bu *BatteryUnit) SOC() float64 {
	if bu.ratedCapacityKWh == 0 {
		return 0
	}
	return bu.currentEnergyKWh / bu.ratedCapacityKWh * 100.0
}

// BatteryVoltage 按单体电压曲线（SOC=0% → 2.8V，SOC=100% → 3.6V）线性插值，
// 再乘以串数得到 pack 电压。满电时近似等于 batteryVoltageFull。
func (bu *BatteryUnit) BatteryVoltage() float64 {
	return float64(bu.cellCount()) * bu.cellAvgVoltage()
}

// cellCount 由满电电压 / 满电单体电压估算串数。
func (bu *BatteryUnit) cellCount() int {
	n := int(math.Round(bu.batteryVoltageFull / cellVoltageFull))
	if n < 2 {
		n = 2
	}
	return n
}

// cellAvgVoltage 按 SOC 在 cellVoltageEmpty ~ cellVoltageFull 之间线性插值。
func (bu *BatteryUnit) cellAvgVoltage() float64 {
	return cellVoltageEmpty + (cellVoltageFull-cellVoltageEmpty)*bu.SOC()/100.0
}

const (
	cellVoltageEmpty = 2.8 // V, SOC=0
	cellVoltageFull  = 3.6 // V, SOC=100
)

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
			bu.pcsDCUnderVoltFault = true
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
		bu.pcsDCUnderVoltFault = false
		bu.pcs.WriteU16(RegPCSFaultReset, 0)
	}
}

func (bu *BatteryUnit) ProcessPowerCommand() {
	cmdRaw := bu.syncPowerCommandRegisters()
	if bu.pcsRunning && bu.remoteMode && bu.bmsHVClosed {
		// RegPCSPowerCmd 按真机 IES1000/IES900 约定取值：负=充电、正=放电。
		// 内部 actualPowerKW 用相反的“正充负放”语义驱动电量/电表/状态，
		// 故此处取反，完成“设备约定 → 内部约定”转换（对齐 emu-go ChargeSign=-1）。
		cmdPowerKW := -float64(uint16ToInt16(cmdRaw)) * 0.1
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
