package main

import (
	"math"
	"math/rand"
)

// loadPowerFactor is the assumed power factor of the facility load (inductive).
// PV inverter and BESS PCS are assumed to operate at unity PF, so the only source
// of reactive power at the PCC is the load itself.
const loadPowerFactor = 0.95

func (b *BESS) updateMeter(dtSeconds float64) {
	// Grid power at the point of common coupling (PCC):
	//   positive = importing from grid, negative = exporting to grid
	// BESS actualPowerKW: positive=charge (draws from grid), negative=discharge (injects)
	// PV  pvActualPowerKW: always positive (injects)
	// Load loadActualPowerKW: always positive (consumes)
	b.meterGridPowerKW = b.loadActualPowerKW + b.actualPowerKW - b.pvActualPowerKW

	if dtSeconds <= 0 {
		return
	}
	deltaKWh := b.meterGridPowerKW * dtSeconds / 3600.0
	if deltaKWh > 0 {
		b.meterForwardKWh += deltaKWh
	} else {
		b.meterReverseKWh += -deltaKWh
	}
}

func (b *BESS) syncMeterRegisters() {
	s := b.server
	gridPowerKW := b.meterGridPowerKW

	// Reactive power: only the load contributes (PV/PCS at unity PF).
	// Q_load = P_load × tan(arccos(PF)), always >= 0 (inductive).
	tanPhi := math.Sqrt(1-loadPowerFactor*loadPowerFactor) / loadPowerFactor
	reactiveKVar := b.loadActualPowerKW * tanPhi

	// Apparent power: S = √(P² + Q²)
	apparentKVA := math.Sqrt(gridPowerKW*gridPowerKW + reactiveKVar*reactiveKVar)

	// Power factor magnitude = |P|/S. Per GB/T 17215 sign convention,
	// inductive (Q ≥ 0) → positive sign. Q is always ≥ 0 here, so PF ≥ 0.
	pfMag := 1.0
	if apparentKVA > 0 {
		pfMag = math.Abs(gridPowerKW) / apparentKVA
	}

	// Energy (S32, 0.01 kWh → ×100)
	combinedKWh := b.meterForwardKWh + b.meterReverseKWh
	writeS32Holding(s, RegMeterCombinedEnergyHi, int32(combinedKWh*100))
	writeS32Holding(s, RegMeterForwardEnergyHi, int32(b.meterForwardKWh*100))
	writeS32Holding(s, RegMeterReverseEnergyHi, int32(b.meterReverseKWh*100))

	// Voltage A/B/C (U16, 0.1 V) with ±0.5% jitter
	phaseVoltages := [3]float64{}
	phaseVoltRegs := [3]uint16{RegMeterVoltageA, RegMeterVoltageB, RegMeterVoltageC}
	for i, reg := range phaseVoltRegs {
		jitter := 1.0 + (rand.Float64()*0.01 - 0.005)
		v := b.gridVoltage * jitter
		phaseVoltages[i] = v
		s.HoldingRegisters[reg] = uint16(v * 10)
	}

	// Current A/B/C (S32, 0.1 A → ×10):
	//   magnitude follows apparent power: |I| = S_phase / V_phase
	//   sign follows active power direction
	phasePowerKW := gridPowerKW / 3.0
	phaseApparentKVA := apparentKVA / 3.0
	phaseCurrentHi := [3]uint16{RegMeterCurrentAHi, RegMeterCurrentBHi, RegMeterCurrentCHi}
	for i, hiReg := range phaseCurrentHi {
		currentA := 0.0
		if phaseVoltages[i] > 0 {
			mag := phaseApparentKVA * 1000.0 / phaseVoltages[i]
			if phasePowerKW < 0 {
				currentA = -mag
			} else {
				currentA = mag
			}
		}
		writeS32Holding(s, hiReg, int32(currentA*10))
	}

	// Active power total + per-phase (S32, 0.001 kW → ×1000)
	writeS32Holding(s, RegMeterActivePWTotalHi, int32(gridPowerKW*1000))
	phasePW1000 := int32(phasePowerKW * 1000)
	writeS32Holding(s, RegMeterActivePWAHi, phasePW1000)
	writeS32Holding(s, RegMeterActivePWBHi, phasePW1000)
	writeS32Holding(s, RegMeterActivePWCHi, phasePW1000)

	// Reactive power total + per-phase (S32, 0.001 kVar → ×1000)
	writeS32Holding(s, RegMeterReactivePWTotalHi, int32(reactiveKVar*1000))
	phaseReact1000 := int32(reactiveKVar / 3.0 * 1000)
	writeS32Holding(s, RegMeterReactivePWAHi, phaseReact1000)
	writeS32Holding(s, RegMeterReactivePWBHi, phaseReact1000)
	writeS32Holding(s, RegMeterReactivePWCHi, phaseReact1000)

	// Apparent power total + per-phase (S32, 0.001 kVA → ×1000)
	writeS32Holding(s, RegMeterApparentPWTotalHi, int32(apparentKVA*1000))
	phaseAppar1000 := int32(phaseApparentKVA * 1000)
	writeS32Holding(s, RegMeterApparentPWAHi, phaseAppar1000)
	writeS32Holding(s, RegMeterApparentPWBHi, phaseAppar1000)
	writeS32Holding(s, RegMeterApparentPWCHi, phaseAppar1000)

	// Power factor (S32, 0.001): always non-negative (load is inductive)
	pf1000 := int32(pfMag * 1000)
	writeS32Holding(s, RegMeterPFTotalHi, pf1000)
	writeS32Holding(s, RegMeterPFAHi, pf1000)
	writeS32Holding(s, RegMeterPFBHi, pf1000)
	writeS32Holding(s, RegMeterPFCHi, pf1000)

	// Grid frequency: 50.00 ± 0.05 Hz random jitter
	freqHz := 50.0 + (rand.Float64()*2-1)*0.05
	s.HoldingRegisters[RegMeterFrequency] = uint16(freqHz * 100)
}
