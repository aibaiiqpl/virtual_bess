package main

import (
	"math"
	"math/rand"
	"time"

	"aiwatt.net/ems/go-common/mbserver"
)

const pvInverterEfficiency = 0.98

type pvLimitMode int

const (
	pvLimitPercent pvLimitMode = iota
	pvLimitFixed
)

func (b *BESS) registerPVWriteHandlers() {
	b.server.RegisterFunctionHandler(6, b.handleWriteHoldingRegister)
	b.server.RegisterFunctionHandler(16, b.handleWriteHoldingRegisters)
}

func (b *BESS) handleWriteHoldingRegister(s *mbserver.Server, frame mbserver.Framer) ([]byte, *mbserver.Exception) {
	b.mu.Lock()
	defer b.mu.Unlock()

	data, exception := mbserver.WriteHoldingRegister(s, frame)
	if isModbusSuccess(exception) {
		register, value := mbserver.RegisterAddressAndValue(frame)
		b.recordPVLimitWrite(uint16(register), value)
	}
	return data, exception
}

func (b *BESS) handleWriteHoldingRegisters(s *mbserver.Server, frame mbserver.Framer) ([]byte, *mbserver.Exception) {
	b.mu.Lock()
	defer b.mu.Unlock()

	data, exception := mbserver.WriteHoldingRegisters(s, frame)
	if isModbusSuccess(exception) {
		register, _, _ := mbserver.RegisterAddressAndNumber(frame)
		values := mbserver.BytesToUint16(frame.GetData()[5:])
		for i, value := range values {
			b.recordPVLimitWrite(uint16(register+i), value)
		}
	}
	return data, exception
}

func isModbusSuccess(exception *mbserver.Exception) bool {
	return exception != nil && *exception == mbserver.Success
}

func (b *BESS) recordPVLimitWrite(register, value uint16) {
	switch register {
	case RegPVPercentLimit:
		value = clampU16(value, 0, 1000)
		b.server.HoldingRegisters[RegPVPercentLimit] = value
		b.lastPVPercentLimitRaw = value
		b.pvLimitMode = pvLimitPercent
	case RegPVFixedLimit:
		value = clampU16(value, 0, b.pvMaxFixedLimitRaw())
		b.server.HoldingRegisters[RegPVFixedLimit] = value
		b.lastPVFixedLimitRaw = value
		b.pvLimitMode = pvLimitFixed
	}
}

func (b *BESS) processPVControls() {
	s := b.server

	if s.HoldingRegisters[RegPVStartup] == 1 {
		b.pvRunning = true
		s.HoldingRegisters[RegPVStartup] = 0
	}
	if s.HoldingRegisters[RegPVShutdown] == 1 {
		b.pvRunning = false
		b.pvActualPowerKW = 0
		s.HoldingRegisters[RegPVShutdown] = 0
	}

	percentRaw := s.HoldingRegisters[RegPVPercentLimit]
	percentChanged := percentRaw != b.lastPVPercentLimitRaw
	percentRaw = clampU16(percentRaw, 0, 1000)
	s.HoldingRegisters[RegPVPercentLimit] = percentRaw

	fixedRaw := s.HoldingRegisters[RegPVFixedLimit]
	fixedChanged := fixedRaw != b.lastPVFixedLimitRaw
	fixedRaw = clampU16(fixedRaw, 0, b.pvMaxFixedLimitRaw())
	s.HoldingRegisters[RegPVFixedLimit] = fixedRaw

	if percentChanged {
		b.lastPVPercentLimitRaw = percentRaw
		b.pvLimitMode = pvLimitPercent
	}
	if fixedChanged {
		b.lastPVFixedLimitRaw = fixedRaw
		b.pvLimitMode = pvLimitFixed
	}
}

func (b *BESS) updatePVSimulation(now time.Time, dtSeconds float64) {
	b.resetPVPeriods(now)

	powerKW := 0.0
	if b.pvRunning {
		powerKW = math.Min(b.pvNaturalPowerKW(now), b.pvActiveLimitKW())
	}
	if powerKW < 0 {
		powerKW = 0
	}

	b.pvActualPowerKW = powerKW
	if powerKW > b.pvDailyPeakPowerKW {
		b.pvDailyPeakPowerKW = powerKW
	}

	if dtSeconds <= 0 || powerKW == 0 {
		return
	}
	deltaEnergy := powerKW * dtSeconds / 3600.0
	b.pvTotalEnergyKWh += deltaEnergy
	b.pvDailyEnergyKWh += deltaEnergy
	b.pvMonthlyEnergyKWh += deltaEnergy
	b.pvYearlyEnergyKWh += deltaEnergy
}

func (b *BESS) pvNaturalPowerKW(now time.Time) float64 {
	hour := float64(now.Hour()) +
		float64(now.Minute())/60.0 +
		float64(now.Second())/3600.0 +
		float64(now.Nanosecond())/float64(time.Hour)

	if hour < 6 || hour >= 18 {
		return 0
	}

	peak := b.pvRatedPowerKW * 0.9
	var natural float64
	switch {
	case hour < 13:
		natural = peak * (hour - 6) / 7
	case hour <= 15:
		natural = peak
	default:
		natural = peak * (18 - hour) / 3
	}
	return natural * b.weatherCoeff
}

func (b *BESS) pvActiveLimitKW() float64 {
	switch b.pvLimitMode {
	case pvLimitFixed:
		return float64(b.lastPVFixedLimitRaw) * 0.1
	default:
		return b.pvRatedPowerKW * float64(b.lastPVPercentLimitRaw) / 1000.0
	}
}

func (b *BESS) resetPVPeriods(now time.Time) {
	dayKey := now.Year()*1000 + now.YearDay()
	monthKey := now.Year()*100 + int(now.Month())
	yearKey := now.Year()

	if b.pvDayKey == 0 {
		b.pvDayKey = dayKey
	}
	if b.pvMonthKey == 0 {
		b.pvMonthKey = monthKey
	}
	if b.pvYearKey == 0 {
		b.pvYearKey = yearKey
	}

	if dayKey != b.pvDayKey {
		b.pvDailyEnergyKWh = 0
		b.pvDailyPeakPowerKW = 0
		b.pvDayKey = dayKey
	}
	if monthKey != b.pvMonthKey {
		b.pvMonthlyEnergyKWh = 0
		b.pvMonthKey = monthKey
	}
	if yearKey != b.pvYearKey {
		b.pvYearlyEnergyKWh = 0
		b.pvYearKey = yearKey
	}
}

func (b *BESS) syncPVRegisters() {
	s := b.server
	powerKW := b.pvActualPowerKW

	switch {
	case !b.pvRunning:
		s.HoldingRegisters[RegPVRunStatus] = 1
	case powerKW > 0:
		s.HoldingRegisters[RegPVRunStatus] = 5
	default:
		s.HoldingRegisters[RegPVRunStatus] = 2
	}

	writeU32Holding(s, RegPVTotalEnergyHi, uint32(b.pvTotalEnergyKWh))
	writeU32Holding(s, RegPVDailyEnergyHi, uint32(b.pvDailyEnergyKWh))
	writeU32Holding(s, RegPVMonthlyEnergyHi, uint32(b.pvMonthlyEnergyKWh))
	writeU32Holding(s, RegPVYearlyEnergyHi, uint32(b.pvYearlyEnergyKWh))

	s.HoldingRegisters[RegPVRatedPower] = uint16(b.pvRatedPowerKW * 10)
	s.HoldingRegisters[RegPVFaultAlarm] = 0

	phaseRegs := []uint16{RegPVACVoltageA, RegPVACVoltageB, RegPVACVoltageC}
	for _, reg := range phaseRegs {
		jitter := 1.0 + (rand.Float64()*0.1 - 0.05)
		s.HoldingRegisters[reg] = uint16(b.gridVoltage * jitter * 10)
	}

	phasePowerKW := powerKW / 3.0
	currentRegs := []uint16{RegPVACCurrentA, RegPVACCurrentB, RegPVACCurrentC}
	for i, reg := range currentRegs {
		phaseVoltage := float64(s.HoldingRegisters[phaseRegs[i]]) * 0.1
		if phaseVoltage > 0 {
			currentA := phasePowerKW * 1000.0 / phaseVoltage
			s.HoldingRegisters[reg] = int16ToUint16(int16(currentA * 10))
		}
	}

	s.HoldingRegisters[RegPVGridFrequency] = 5000
	s.HoldingRegisters[RegPVPowerFactor] = int16ToUint16(1000)
	s.HoldingRegisters[RegPVACActivePower] = int16ToUint16(int16(powerKW * 10))
	s.HoldingRegisters[RegPVACReactivePower] = 0
	s.HoldingRegisters[RegPVInverterEfficiency] = uint16(pvInverterEfficiency * 1000)
	s.HoldingRegisters[RegPVDailyPeakPower] = int16ToUint16(int16(b.pvDailyPeakPowerKW * 10))
	s.HoldingRegisters[RegPVApparentPower] = uint16(math.Abs(powerKW) * 10)

	dcInputPowerKW := 0.0
	if powerKW > 0 {
		dcInputPowerKW = powerKW / pvInverterEfficiency
	}
	s.HoldingRegisters[RegPVDCInputPower] = int16ToUint16(int16(dcInputPowerKW * 10))
	s.HoldingRegisters[RegPVInternalTemp] = int16ToUint16(300)
	s.HoldingRegisters[RegPVDCVoltage] = uint16(b.batteryVoltageNom * 10)

	dcCurrentA := 0.0
	if b.batteryVoltageNom > 0 {
		dcCurrentA = dcInputPowerKW * 1000.0 / b.batteryVoltageNom
	}
	s.HoldingRegisters[RegPVDCCurrent] = int16ToUint16(int16(dcCurrentA * 10))
}

func writeU32Holding(s *mbserver.Server, highRegister uint16, value uint32) {
	hi, lo := uint32ToRegs(value)
	s.HoldingRegisters[highRegister] = hi
	s.HoldingRegisters[highRegister+1] = lo
}

func (b *BESS) pvMaxFixedLimitRaw() uint16 {
	maxRaw := b.pvRatedPowerKW * 10
	maxUint16 := float64(^uint16(0))
	if maxRaw > maxUint16 {
		return ^uint16(0)
	}
	return uint16(maxRaw)
}

func clampU16(value, min, max uint16) uint16 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
