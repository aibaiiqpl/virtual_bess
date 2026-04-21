package main

import (
	"math"
	"math/rand"
)

// syncRegisters writes all computed state to modbus holding registers.
// Called once per tick after controls and simulation have been processed.
func (b *BESS) syncRegisters() {
	soc := b.soc()
	powerKW := b.actualPowerKW
	batVoltage := b.batteryVoltage()

	b.syncPCSStatus(powerKW)
	b.syncPCSPower(powerKW)
	b.syncPCSGrid(powerKW)
	b.syncPCSDC(powerKW, batVoltage)
	b.syncPCSTemperature()
	b.syncBMSStatus(soc, powerKW)
	b.syncBMSEnergy(soc, batVoltage, powerKW)
	b.syncBMSLimits(soc, batVoltage)
	b.syncSystemStatus(powerKW) // after BMS limits, reads RegBMSMaxChargePW/DischargePW
}

// syncSystemStatus updates system-level status registers (1-5, 100-102).
func (b *BESS) syncSystemStatus(powerKW float64) {
	s := b.server

	// Mutually exclusive: only one of running/fault/standby can be 1
	hasFault := s.HoldingRegisters[RegPCSFaultStatus] == 1 || s.HoldingRegisters[RegBMSFaultStatus] == 1
	isRunning := b.pcsRunning && powerKW != 0 && !hasFault

	s.HoldingRegisters[RegSysRunning] = boolToU16(isRunning)
	s.HoldingRegisters[RegSysFault] = boolToU16(hasFault)
	s.HoldingRegisters[RegSysStandby] = boolToU16(!isRunning && !hasFault)

	// EMU-BMS and EMU-PCS communication: always online
	s.HoldingRegisters[RegEMUBMSComm] = 1
	s.HoldingRegisters[RegEMUPCSComm] = 1

	// Run mode: 2 = remote passive
	s.HoldingRegisters[RegSysRunMode] = 2

	// Sync max charge/discharge power from BMS limits
	s.HoldingRegisters[RegSysMaxChargePW] = s.HoldingRegisters[RegBMSMaxChargePW]
	s.HoldingRegisters[RegSysMaxDischargePW] = s.HoldingRegisters[RegBMSMaxDischargePW]

	// Current actual total power (absolute value)
	s.HoldingRegisters[RegSysActualPower] = uint16(math.Abs(powerKW) * 10)

	// BMS master mode and cluster count: fixed values
	s.HoldingRegisters[RegBMSMasterMode] = 1
	s.HoldingRegisters[RegBMSClusterCount] = 1
}

// syncPCSStatus updates PCS system status, fault, and alarm registers.
func (b *BESS) syncPCSStatus(powerKW float64) {
	s := b.server
	s.HoldingRegisters[RegPCSRemoteStatus] = boolToU16(b.remoteMode)
	s.HoldingRegisters[RegPCSGridStatus] = boolToU16(!b.gridTied)
	s.HoldingRegisters[RegPCSAlarmStatus] = 0
	s.HoldingRegisters[RegPCSFaultStatus] = 0

	// PCS system status: 1-stopped, 2-standby, 3-charging, 4-discharging
	switch {
	case !b.pcsRunning:
		s.HoldingRegisters[RegPCSSysStatus] = 1
	case powerKW > 0:
		s.HoldingRegisters[RegPCSSysStatus] = 3
	case powerKW < 0:
		s.HoldingRegisters[RegPCSSysStatus] = 4
	default:
		s.HoldingRegisters[RegPCSSysStatus] = 2
	}

	// DC undervoltage fault when PCS running without BMS HV closed
	if b.pcsRunning && !b.bmsHVClosed {
		s.HoldingRegisters[RegPCSDCUnderVolt] = 1
		s.HoldingRegisters[RegPCSFaultStatus] = 1
	} else {
		s.HoldingRegisters[RegPCSDCUnderVolt] = 0
	}
}

// syncPCSPower updates total/phase active power, reactive power, apparent power and power factor.
func (b *BESS) syncPCSPower(powerKW float64) {
	s := b.server
	s.HoldingRegisters[RegPCSTotalActivePW] = int16ToUint16(int16(powerKW * 10))
	s.HoldingRegisters[RegPCSTotalReactPW] = 0
	s.HoldingRegisters[RegPCSTotalApparent] = uint16(math.Abs(powerKW) * 10)
	s.HoldingRegisters[RegPCSPowerFactor] = int16ToUint16(100) // 1.00

	// Three-phase active power: equal split
	phasePW := int16(powerKW / 3.0 * 10)
	s.HoldingRegisters[RegPCSActivePWA] = int16ToUint16(phasePW)
	s.HoldingRegisters[RegPCSActivePWB] = int16ToUint16(phasePW)
	s.HoldingRegisters[RegPCSActivePWC] = int16ToUint16(phasePW)

	// Reactive power: zero (unity power factor)
	s.HoldingRegisters[RegPCSReactPWA] = 0
	s.HoldingRegisters[RegPCSReactPWB] = 0
	s.HoldingRegisters[RegPCSReactPWC] = 0
}

// syncPCSGrid updates three-phase voltage (with ±5% jitter), current, and frequency.
func (b *BESS) syncPCSGrid(powerKW float64) {
	s := b.server
	phasePowerKW := powerKW / 3.0

	// Three-phase voltage with ±5% random fluctuation
	phaseRegs := []uint16{RegPCSVoltageA, RegPCSVoltageB, RegPCSVoltageC}
	for _, reg := range phaseRegs {
		jitter := 1.0 + (rand.Float64()*0.1 - 0.05)
		s.HoldingRegisters[reg] = uint16(b.gridVoltage * jitter * 10)
	}

	// Three-phase current derived from power and voltage: I = P(kW)*1000 / V
	currentRegs := []uint16{RegPCSCurrentA, RegPCSCurrentB, RegPCSCurrentC}
	for i, reg := range currentRegs {
		phaseVoltage := float64(s.HoldingRegisters[phaseRegs[i]]) * 0.1
		if phaseVoltage > 0 {
			currentA := phasePowerKW * 1000.0 / phaseVoltage
			s.HoldingRegisters[reg] = int16ToUint16(int16(currentA * 10))
		}
	}

	// Grid frequency: fixed 50.00 Hz
	s.HoldingRegisters[RegPCSFrequency] = 5000
}

// syncPCSDC updates DC side voltage, current, and power registers.
func (b *BESS) syncPCSDC(powerKW, batVoltage float64) {
	s := b.server
	s.HoldingRegisters[RegPCSDCVoltage] = int16ToUint16(int16(batVoltage * 10))

	dcCurrentA := 0.0
	if batVoltage > 0 {
		dcCurrentA = powerKW * 1000.0 / batVoltage
	}
	s.HoldingRegisters[RegPCSDCCurrent] = int16ToUint16(int16(dcCurrentA * 10))
	s.HoldingRegisters[RegPCSDCPower] = int16ToUint16(int16(powerKW * 10))
}

// syncPCSTemperature sets fixed temperature values for PCS internals and IGBTs (30.0 C).
func (b *BESS) syncPCSTemperature() {
	s := b.server
	const temp = 300 // 30.0 °C
	s.HoldingRegisters[RegPCSInternalTemp] = int16ToUint16(temp)
	s.HoldingRegisters[RegPCSIGBTTempA] = int16ToUint16(temp)
	s.HoldingRegisters[RegPCSIGBTTempB] = int16ToUint16(temp)
	s.HoldingRegisters[RegPCSIGBTTempC] = int16ToUint16(temp)
}

// syncBMSStatus updates BMS system status, fault, alarm, and charge/discharge forbidden flags.
func (b *BESS) syncBMSStatus(soc, powerKW float64) {
	s := b.server
	s.HoldingRegisters[RegBMSFaultStatus] = 0
	s.HoldingRegisters[RegBMSAlarmStatus] = 0

	// BMS system status: 1-standby, 2-stopped, 3-charging, 4-discharging
	switch {
	case !b.bmsHVClosed:
		s.HoldingRegisters[RegBMSSysStatus] = 2
	case powerKW > 0:
		s.HoldingRegisters[RegBMSSysStatus] = 3
	case powerKW < 0:
		s.HoldingRegisters[RegBMSSysStatus] = 4
	default:
		s.HoldingRegisters[RegBMSSysStatus] = 1
	}

	// Charge forbidden at SOC 100%, discharge forbidden at SOC 0%
	s.HoldingRegisters[RegBMSChargeForbid] = boolToU16(soc >= 100.0)
	s.HoldingRegisters[RegBMSDischargeForbid] = boolToU16(soc <= 0.0)
}

// syncBMSEnergy updates SOC, SOH, remaining energy, voltage, current, and power registers.
func (b *BESS) syncBMSEnergy(soc, batVoltage, powerKW float64) {
	s := b.server
	s.HoldingRegisters[RegBMSSOC] = uint16(soc * 10)
	s.HoldingRegisters[RegBMSSOH] = uint16(b.soh * 10)

	// Remaining charge/discharge capacity
	s.HoldingRegisters[RegBMSRemainCharge] = uint16((b.ratedCapacityKWh - b.currentEnergyKWh) * 10)
	s.HoldingRegisters[RegBMSRemainDischarge] = uint16(b.currentEnergyKWh * 10)

	// Battery voltage, current, power
	s.HoldingRegisters[RegBMSVoltage] = uint16(batVoltage * 10)
	dcCurrentA := 0.0
	if batVoltage > 0 {
		dcCurrentA = powerKW * 1000.0 / batVoltage
	}
	s.HoldingRegisters[RegBMSCurrent] = int16ToUint16(int16(dcCurrentA * 10))
	s.HoldingRegisters[RegBMSPower] = int16ToUint16(int16(powerKW * 10))
}

// syncBMSLimits updates max allowed charge/discharge power and current.
// These drop to zero when SOC reaches boundaries.
func (b *BESS) syncBMSLimits(soc, batVoltage float64) {
	s := b.server
	ratedCurrentA := b.ratedPowerKW * 1000.0 / batVoltage

	maxChargePW, maxDischargePW := b.ratedPowerKW, b.ratedPowerKW
	maxChargeI, maxDischargeI := ratedCurrentA, ratedCurrentA

	if soc >= 100.0 {
		maxChargePW, maxChargeI = 0, 0
	}
	if soc <= 0.0 {
		maxDischargePW, maxDischargeI = 0, 0
	}

	s.HoldingRegisters[RegBMSMaxChargePW] = uint16(maxChargePW * 10)
	s.HoldingRegisters[RegBMSMaxDischargePW] = uint16(maxDischargePW * 10)
	s.HoldingRegisters[RegBMSMaxChargeI] = uint16(maxChargeI * 10)
	s.HoldingRegisters[RegBMSMaxDischargeI] = uint16(maxDischargeI * 10)
}
