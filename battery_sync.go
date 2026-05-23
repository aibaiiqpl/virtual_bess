package main

import (
	"math"
	"math/rand"
)

// Sync 把当前状态写到 PCS 和 BMS 两个 bank 的寄存器。
func (bu *BatteryUnit) Sync() {
	soc := bu.SOC()
	powerKW := bu.actualPowerKW
	batVoltage := bu.BatteryVoltage()

	bu.syncPCSStatus(powerKW)
	bu.syncPCSPower(powerKW)
	bu.syncPCSGrid(powerKW)
	bu.syncPCSDC(powerKW, batVoltage)
	bu.syncPCSTemperature()
	bu.syncBMSStatus(soc, powerKW)
	bu.syncBMSEnergy(soc, batVoltage, powerKW)
	bu.syncBMSLimits(soc, batVoltage)
	bu.syncBMSCellStats(batVoltage)
	bu.syncSystemStatus(powerKW)
	bu.syncClusterRegisters(soc, batVoltage, powerKW)
}

// syncBMSCellStats 写最高/最低/平均单体电压与温度，以及极差。
// 单体平均电压由 SOC 在 2.8V ~ 3.6V 间线性插值；温度围绕 30°C 抖动。
func (bu *BatteryUnit) syncBMSCellStats(_ float64) {
	cellCount := bu.cellCount()

	avgV := bu.cellAvgVoltage()
	spreadV := avgV * (0.003 + rand.Float64()*0.003) // 0.3% ~ 0.6% 极差
	maxV := avgV + spreadV/2
	minV := avgV - spreadV/2

	avgT := 30.0 + (rand.Float64()*2 - 1)
	spreadT := 1.5 + rand.Float64()*1.5 // 1.5°C ~ 3.0°C 极差
	maxT := avgT + spreadT/2
	minT := avgT - spreadT/2

	// 编号 1..cellCount，每 tick 抽签一次
	maxVIdx := uint16(rand.Intn(cellCount) + 1)
	minVIdx := uint16(rand.Intn(cellCount) + 1)
	if minVIdx == maxVIdx {
		minVIdx = uint16((int(maxVIdx)%cellCount) + 1)
	}
	maxTIdx := uint16(rand.Intn(cellCount) + 1)
	minTIdx := uint16(rand.Intn(cellCount) + 1)
	if minTIdx == maxTIdx {
		minTIdx = uint16((int(maxTIdx)%cellCount) + 1)
	}

	bu.bms.WriteU16(RegBMSCellVMax, uint16(maxV*1000))
	bu.bms.WriteU16(RegBMSCellVMaxIdx, maxVIdx)
	bu.bms.WriteU16(RegBMSCellVMin, uint16(minV*1000))
	bu.bms.WriteU16(RegBMSCellVMinIdx, minVIdx)
	bu.bms.WriteU16(RegBMSCellVAvg, uint16(avgV*1000))
	bu.bms.WriteU16(RegBMSCellTMax, int16ToUint16(int16(maxT*10)))
	bu.bms.WriteU16(RegBMSCellTMaxIdx, maxTIdx)
	bu.bms.WriteU16(RegBMSCellTMin, int16ToUint16(int16(minT*10)))
	bu.bms.WriteU16(RegBMSCellTMinIdx, minTIdx)
	bu.bms.WriteU16(RegBMSCellTAvg, int16ToUint16(int16(avgT*10)))
	bu.bms.WriteU16(RegBMSCellVSpread, uint16(spreadV*1000))
	bu.bms.WriteU16(RegBMSCellTSpread, uint16(spreadT*10))
}

func (bu *BatteryUnit) syncSystemStatus(powerKW float64) {
	// 故障/运行/待机互斥
	hasFault := bu.pcs.ReadU16(RegPCSFaultStatus) == 1 || bu.bms.ReadU16(RegBMSFaultStatus) == 1
	isRunning := bu.pcsRunning && powerKW != 0 && !hasFault

	bu.pcs.WriteU16(RegSysRunning, boolToU16(isRunning))
	bu.pcs.WriteU16(RegSysFault, boolToU16(hasFault))
	bu.pcs.WriteU16(RegSysStandby, boolToU16(!isRunning && !hasFault))

	bu.pcs.WriteU16(RegEMUBMSComm, 1)
	bu.pcs.WriteU16(RegEMUPCSComm, 1)

	bu.pcs.WriteU16(RegSysRunMode, 2)

	// 系统最大充放电功率 = min(BMS 限值, 上层外部 setting)
	sysMaxCharge := bu.bms.ReadU16(RegBMSMaxChargePW)
	if ext := bu.pcs.ReadU16(RegMaxChargePWSetting); ext != 0 && ext < sysMaxCharge {
		sysMaxCharge = ext
	}
	sysMaxDischarge := bu.bms.ReadU16(RegBMSMaxDischargePW)
	if ext := bu.pcs.ReadU16(RegMaxDischargePWSetting); ext != 0 && ext < sysMaxDischarge {
		sysMaxDischarge = ext
	}
	bu.pcs.WriteU16(RegSysMaxChargePW, sysMaxCharge)
	bu.pcs.WriteU16(RegSysMaxDischargePW, sysMaxDischarge)
	bu.pcs.WriteU16(RegSysActualPower, uint16(math.Abs(powerKW)*10))

	if bu.clusterCount > 1 {
		bu.pcs.WriteU16(RegBMSMasterMode, 2)
	} else {
		bu.pcs.WriteU16(RegBMSMasterMode, 1)
	}
	bu.pcs.WriteU16(RegBMSClusterCount, uint16(bu.clusterCount))
}

func (bu *BatteryUnit) syncPCSStatus(powerKW float64) {
	bu.pcs.WriteU16(RegPCSRemoteStatus, boolToU16(bu.remoteMode))
	bu.pcs.WriteU16(RegPCSGridStatus, boolToU16(!bu.gridTied))
	bu.pcs.WriteU16(RegPCSAlarmStatus, 0)
	bu.pcs.WriteU16(RegPCSFaultStatus, 0)

	switch {
	case !bu.pcsRunning:
		bu.pcs.WriteU16(RegPCSSysStatus, 1)
	case powerKW > 0:
		bu.pcs.WriteU16(RegPCSSysStatus, 3)
	case powerKW < 0:
		bu.pcs.WriteU16(RegPCSSysStatus, 4)
	default:
		bu.pcs.WriteU16(RegPCSSysStatus, 2)
	}

	if bu.pcsRunning && !bu.bmsHVClosed {
		bu.pcs.WriteU16(RegPCSDCUnderVolt, 1)
		bu.pcs.WriteU16(RegPCSFaultStatus, 1)
	} else {
		bu.pcs.WriteU16(RegPCSDCUnderVolt, 0)
	}
}

func (bu *BatteryUnit) syncPCSPower(powerKW float64) {
	bu.pcs.WriteU16(RegPCSTotalActivePW, int16ToUint16(int16(powerKW*10)))
	bu.pcs.WriteU16(RegPCSTotalReactPW, 0)
	bu.pcs.WriteU16(RegPCSTotalApparent, uint16(math.Abs(powerKW)*10))
	bu.pcs.WriteU16(RegPCSPowerFactor, int16ToUint16(100))

	phasePW := int16(powerKW / 3.0 * 10)
	bu.pcs.WriteU16(RegPCSActivePWA, int16ToUint16(phasePW))
	bu.pcs.WriteU16(RegPCSActivePWB, int16ToUint16(phasePW))
	bu.pcs.WriteU16(RegPCSActivePWC, int16ToUint16(phasePW))

	bu.pcs.WriteU16(RegPCSReactPWA, 0)
	bu.pcs.WriteU16(RegPCSReactPWB, 0)
	bu.pcs.WriteU16(RegPCSReactPWC, 0)
}

func (bu *BatteryUnit) syncPCSGrid(powerKW float64) {
	phasePowerKW := powerKW / 3.0

	phaseRegs := []uint16{RegPCSVoltageA, RegPCSVoltageB, RegPCSVoltageC}
	for _, reg := range phaseRegs {
		jitter := 1.0 + (rand.Float64()*0.01 - 0.005)
		bu.pcs.WriteU16(reg, uint16(bu.pcsACVoltage*jitter*10))
	}

	currentRegs := []uint16{RegPCSCurrentA, RegPCSCurrentB, RegPCSCurrentC}
	for i, reg := range currentRegs {
		phaseVoltage := float64(bu.pcs.ReadU16(phaseRegs[i])) * 0.1
		if phaseVoltage > 0 {
			currentA := phasePowerKW * 1000.0 / phaseVoltage
			bu.pcs.WriteU16(reg, int16ToUint16(int16(currentA*10)))
		}
	}

	bu.pcs.WriteU16(RegPCSFrequency, 5000)
}

func (bu *BatteryUnit) syncPCSDC(powerKW, batVoltage float64) {
	bu.pcs.WriteU16(RegPCSDCVoltage, int16ToUint16(int16(batVoltage*10)))

	dcCurrentA := 0.0
	if batVoltage > 0 {
		dcCurrentA = powerKW * 1000.0 / batVoltage
	}
	bu.pcs.WriteU16(RegPCSDCCurrent, int16ToUint16(int16(dcCurrentA*10)))
	bu.pcs.WriteU16(RegPCSDCPower, int16ToUint16(int16(powerKW*10)))
}

func (bu *BatteryUnit) syncPCSTemperature() {
	const temp = 300 // 30.0 °C
	bu.pcs.WriteU16(RegPCSInternalTemp, int16ToUint16(temp))
	bu.pcs.WriteU16(RegPCSIGBTTempA, int16ToUint16(temp))
	bu.pcs.WriteU16(RegPCSIGBTTempB, int16ToUint16(temp))
	bu.pcs.WriteU16(RegPCSIGBTTempC, int16ToUint16(temp))
}

func (bu *BatteryUnit) syncBMSStatus(soc, powerKW float64) {
	bu.bms.WriteU16(RegBMSFaultStatus, 0)
	bu.bms.WriteU16(RegBMSAlarmStatus, 0)

	switch {
	case !bu.bmsHVClosed:
		bu.bms.WriteU16(RegBMSSysStatus, 2)
	case powerKW > 0:
		bu.bms.WriteU16(RegBMSSysStatus, 3)
	case powerKW < 0:
		bu.bms.WriteU16(RegBMSSysStatus, 4)
	default:
		bu.bms.WriteU16(RegBMSSysStatus, 1)
	}

	bu.bms.WriteU16(RegBMSChargeForbid, boolToU16(soc >= 100.0))
	bu.bms.WriteU16(RegBMSDischargeForbid, boolToU16(soc <= 0.0))
}

func (bu *BatteryUnit) syncBMSEnergy(soc, batVoltage, powerKW float64) {
	bu.bms.WriteU16(RegBMSSOC, uint16(soc*10))
	bu.bms.WriteU16(RegBMSSOH, uint16(bu.soh*10))

	bu.bms.WriteU16(RegBMSRemainCharge, uint16((bu.ratedCapacityKWh-bu.currentEnergyKWh)*10))
	bu.bms.WriteU16(RegBMSRemainDischarge, uint16(bu.currentEnergyKWh*10))

	bu.bms.WriteU16(RegBMSVoltage, uint16(batVoltage*10))
	dcCurrentA := 0.0
	if batVoltage > 0 {
		dcCurrentA = powerKW * 1000.0 / batVoltage
	}
	bu.bms.WriteU16(RegBMSCurrent, int16ToUint16(int16(dcCurrentA*10)))
	bu.bms.WriteU16(RegBMSPower, int16ToUint16(int16(powerKW*10)))
}

func (bu *BatteryUnit) syncBMSLimits(soc, batVoltage float64) {
	ratedCurrentA := 0.0
	if batVoltage > 0 {
		ratedCurrentA = bu.ratedPowerKW * 1000.0 / batVoltage
	}

	maxChargePW, maxDischargePW := bu.ratedPowerKW, bu.ratedPowerKW
	maxChargeI, maxDischargeI := ratedCurrentA, ratedCurrentA

	if soc >= 100.0 {
		maxChargePW, maxChargeI = 0, 0
	}
	if soc <= 0.0 {
		maxDischargePW, maxDischargeI = 0, 0
	}

	bu.bms.WriteU16(RegBMSMaxChargePW, uint16(maxChargePW*10))
	bu.bms.WriteU16(RegBMSMaxDischargePW, uint16(maxDischargePW*10))
	bu.bms.WriteU16(RegBMSMaxChargeI, uint16(maxChargeI*10))
	bu.bms.WriteU16(RegBMSMaxDischargeI, uint16(maxDischargeI*10))
}

func (bu *BatteryUnit) syncClusterRegisters(soc, batVoltage, powerKW float64) {
	n := bu.clusterCount
	if n <= 0 {
		return
	}
	clusterPowerKW := powerKW / float64(n)
	clusterCurrentA := 0.0
	if batVoltage > 0 {
		clusterCurrentA = clusterPowerKW * 1000.0 / batVoltage
	}

	var clusterStatus uint16
	switch {
	case !bu.bmsHVClosed:
		clusterStatus = 2
	case powerKW > 0:
		clusterStatus = 3
	case powerKW < 0:
		clusterStatus = 4
	case bu.pcsRunning:
		clusterStatus = 5
	default:
		clusterStatus = 1
	}

	remainCharge := (bu.ratedCapacityKWh - bu.currentEnergyKWh) / float64(n)
	remainDischarge := bu.currentEnergyKWh / float64(n)

	totalChargeU32 := uint32(bu.totalChargeKWh / float64(n) * 10)
	totalDischU32 := uint32(bu.totalDischargeKWh / float64(n) * 10)
	sessChargeU32 := uint32(bu.sessionChargeKWh / float64(n) * 10)
	sessDischU32 := uint32(bu.sessionDischargeKWh / float64(n) * 10)

	ratedCurrentA := 0.0
	if batVoltage > 0 {
		ratedCurrentA = bu.ratedPowerKW * 1000.0 / batVoltage
	}
	maxChargePW, maxDischargePW := bu.ratedPowerKW/float64(n), bu.ratedPowerKW/float64(n)
	maxChargeI, maxDischargeI := ratedCurrentA/float64(n), ratedCurrentA/float64(n)
	if soc >= 100.0 {
		maxChargePW, maxChargeI = 0, 0
	}
	if soc <= 0.0 {
		maxDischargePW, maxDischargeI = 0, 0
	}

	totalChargeHi, totalChargeLo := uint32ToRegs(totalChargeU32)
	totalDischHi, totalDischLo := uint32ToRegs(totalDischU32)
	sessChargeHi, sessChargeLo := uint32ToRegs(sessChargeU32)
	sessDischHi, sessDischLo := uint32ToRegs(sessDischU32)

	for i := 0; i < n; i++ {
		bu.bms.WriteInputU16(clusterIR(i, OffClusterStatus), clusterStatus)
		bu.bms.WriteInputU16(clusterIR(i, OffClusterSOC), uint16(soc*10))
		bu.bms.WriteInputU16(clusterIR(i, OffClusterSOH), uint16(bu.soh*10))
		bu.bms.WriteInputU16(clusterIR(i, OffClusterRemainCharge), uint16(remainCharge*10))
		bu.bms.WriteInputU16(clusterIR(i, OffClusterRemainDischarge), uint16(remainDischarge*10))
		bu.bms.WriteInputU16(clusterIR(i, OffClusterVoltage), uint16(batVoltage*10))
		bu.bms.WriteInputU16(clusterIR(i, OffClusterCurrent), int16ToUint16(int16(clusterCurrentA*10)))
		bu.bms.WriteInputU16(clusterIR(i, OffClusterPower), int16ToUint16(int16(clusterPowerKW*10)))
		bu.bms.WriteInputU16(clusterIR(i, OffClusterTotalChargeHi), totalChargeHi)
		bu.bms.WriteInputU16(clusterIR(i, OffClusterTotalChargeLo), totalChargeLo)
		bu.bms.WriteInputU16(clusterIR(i, OffClusterTotalDischHi), totalDischHi)
		bu.bms.WriteInputU16(clusterIR(i, OffClusterTotalDischLo), totalDischLo)
		bu.bms.WriteInputU16(clusterIR(i, OffClusterSessChargeHi), sessChargeHi)
		bu.bms.WriteInputU16(clusterIR(i, OffClusterSessChargeLo), sessChargeLo)
		bu.bms.WriteInputU16(clusterIR(i, OffClusterSessDischHi), sessDischHi)
		bu.bms.WriteInputU16(clusterIR(i, OffClusterSessDischLo), sessDischLo)
		bu.bms.WriteInputU16(clusterIR(i, OffClusterMaxChargePW), uint16(maxChargePW*10))
		bu.bms.WriteInputU16(clusterIR(i, OffClusterMaxDischargePW), uint16(maxDischargePW*10))
		bu.bms.WriteInputU16(clusterIR(i, OffClusterMaxChargeI), uint16(maxChargeI*10))
		bu.bms.WriteInputU16(clusterIR(i, OffClusterMaxDischargeI), uint16(maxDischargeI*10))
	}
}
