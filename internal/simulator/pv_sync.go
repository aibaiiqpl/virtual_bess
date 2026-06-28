package simulator

import (
	"math"
	"math/rand"
)

func (pv *PVUnit) Sync() {
	powerKW := pv.actualPowerKW

	switch {
	case !pv.running:
		pv.bank.WriteU16(RegPVRunStatus, 1)
	case powerKW > 0:
		pv.bank.WriteU16(RegPVRunStatus, 5)
	default:
		pv.bank.WriteU16(RegPVRunStatus, 2)
	}

	pv.bank.WriteU32(RegPVTotalEnergyHi, uint32(pv.totalEnergyKWh))
	pv.bank.WriteU32(RegPVDailyEnergyHi, uint32(pv.dailyEnergyKWh))
	pv.bank.WriteU32(RegPVMonthlyEnergyHi, uint32(pv.monthlyEnergyKWh))
	pv.bank.WriteU32(RegPVYearlyEnergyHi, uint32(pv.yearlyEnergyKWh))

	pv.bank.WriteU16(RegPVRatedPower, uint16(pv.ratedPowerKW*10))
	pv.bank.WriteU16(RegPVFaultAlarm, 0)

	phaseRegs := []uint16{RegPVACVoltageA, RegPVACVoltageB, RegPVACVoltageC}
	for _, reg := range phaseRegs {
		jitter := 1.0 + (rand.Float64()*0.01 - 0.005)
		pv.bank.WriteU16(reg, uint16(pv.pcsACVoltage*jitter*10))
	}

	phasePowerKW := powerKW / 3.0
	currentRegs := []uint16{RegPVACCurrentA, RegPVACCurrentB, RegPVACCurrentC}
	for i, reg := range currentRegs {
		phaseVoltage := float64(pv.bank.ReadU16(phaseRegs[i])) * 0.1
		if phaseVoltage > 0 {
			currentA := phasePowerKW * 1000.0 / phaseVoltage
			pv.bank.WriteU16(reg, int16ToUint16(int16(currentA*10)))
		}
	}

	pv.bank.WriteU16(RegPVGridFrequency, 5000)
	pv.bank.WriteU16(RegPVPowerFactor, int16ToUint16(1000))
	pv.bank.WriteU16(RegPVACActivePower, int16ToUint16(int16(powerKW*10)))
	pv.bank.WriteU16(RegPVACReactivePower, 0)
	pv.bank.WriteU16(RegPVInverterEfficiency, uint16(pvInverterEfficiency*1000))
	pv.bank.WriteU16(RegPVDailyPeakPower, int16ToUint16(int16(pv.dailyPeakPowerKW*10)))
	pv.bank.WriteU16(RegPVApparentPower, uint16(math.Abs(powerKW)*10))

	dcInputPowerKW := 0.0
	if powerKW > 0 {
		dcInputPowerKW = powerKW / pvInverterEfficiency
	}
	pv.bank.WriteU16(RegPVDCInputPower, int16ToUint16(int16(dcInputPowerKW*10)))
	pv.bank.WriteU16(RegPVInternalTemp, int16ToUint16(300))
	pv.bank.WriteU16(RegPVDCVoltage, uint16(pv.batteryVoltageNom*10))

	dcCurrentA := 0.0
	if pv.batteryVoltageNom > 0 {
		dcCurrentA = dcInputPowerKW * 1000.0 / pv.batteryVoltageNom
	}
	pv.bank.WriteU16(RegPVDCCurrent, int16ToUint16(int16(dcCurrentA*10)))
}
