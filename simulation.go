package main

// updateSimulation advances energy state based on actual power and elapsed time.
// It enforces SOC boundaries: stops charging at 100%, stops discharging at 0%.
func (b *BESS) updateSimulation(dtSeconds float64) {
	if b.actualPowerKW == 0 {
		return
	}

	soc := b.soc()

	// SOC boundary check
	if b.actualPowerKW > 0 && soc >= 100.0 {
		b.actualPowerKW = 0
		return
	}
	if b.actualPowerKW < 0 && soc <= 0.0 {
		b.actualPowerKW = 0
		return
	}

	// deltaEnergy = power(kW) * time(s) / 3600(s/h) = energy(kWh)
	deltaEnergy := b.actualPowerKW * dtSeconds / 3600.0
	b.currentEnergyKWh += deltaEnergy

	// Track cumulative charge/discharge energy
	if deltaEnergy > 0 {
		b.totalChargeKWh += deltaEnergy
		b.sessionChargeKWh += deltaEnergy
	} else if deltaEnergy < 0 {
		b.totalDischargeKWh += -deltaEnergy
		b.sessionDischargeKWh += -deltaEnergy
	}

	// Clamp stored energy to valid range
	if b.currentEnergyKWh < 0 {
		b.currentEnergyKWh = 0
		b.actualPowerKW = 0
	}
	if b.currentEnergyKWh > b.ratedCapacityKWh {
		b.currentEnergyKWh = b.ratedCapacityKWh
		b.actualPowerKW = 0
	}
}
