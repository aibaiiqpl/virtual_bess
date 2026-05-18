package main

import (
	"math"
	"testing"
	"time"

	"aiwatt.net/ems/go-common/mbserver"
)

func TestPVNaturalPowerCurve(t *testing.T) {
	b := newPVTestBESS()

	tests := []struct {
		name string
		at   time.Time
		want float64
	}{
		{name: "before sunrise", at: localTime(2026, 5, 18, 5, 59, 0), want: 0},
		{name: "sunrise", at: localTime(2026, 5, 18, 6, 0, 0), want: 0},
		{name: "peak starts", at: localTime(2026, 5, 18, 13, 0, 0), want: 108},
		{name: "peak middle", at: localTime(2026, 5, 18, 14, 0, 0), want: 108},
		{name: "peak ends", at: localTime(2026, 5, 18, 15, 0, 0), want: 108},
		{name: "sunset", at: localTime(2026, 5, 18, 18, 0, 0), want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := b.pvNaturalPowerKW(tt.at)
			if math.Abs(got-tt.want) > 0.001 {
				t.Fatalf("pvNaturalPowerKW() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPVControlsStartStop(t *testing.T) {
	b := newPVTestBESS()
	noon := localTime(2026, 5, 18, 13, 0, 0)

	b.updatePVSimulation(noon, 1)
	if b.pvActualPowerKW == 0 {
		t.Fatal("default PV state should be running")
	}

	b.server.HoldingRegisters[RegPVShutdown] = 1
	b.processPVControls()
	b.updatePVSimulation(noon, 1)
	if b.pvActualPowerKW != 0 {
		t.Fatalf("pvActualPowerKW after shutdown = %v, want 0", b.pvActualPowerKW)
	}
	if got := b.server.HoldingRegisters[RegPVShutdown]; got != 0 {
		t.Fatalf("shutdown command register = %v, want 0", got)
	}

	b.server.HoldingRegisters[RegPVStartup] = 1
	b.processPVControls()
	b.updatePVSimulation(noon, 1)
	if b.pvActualPowerKW == 0 {
		t.Fatal("PV should resume generation after startup")
	}
	if got := b.server.HoldingRegisters[RegPVStartup]; got != 0 {
		t.Fatalf("startup command register = %v, want 0", got)
	}
}

func TestPVLatestLimitWins(t *testing.T) {
	b := newPVTestBESS()
	noon := localTime(2026, 5, 18, 13, 0, 0)

	b.recordPVLimitWrite(RegPVPercentLimit, 500)
	b.updatePVSimulation(noon, 1)
	assertFloatNear(t, b.pvActualPowerKW, 60)

	b.recordPVLimitWrite(RegPVFixedLimit, 700)
	b.updatePVSimulation(noon, 1)
	assertFloatNear(t, b.pvActualPowerKW, 70)

	b.recordPVLimitWrite(RegPVPercentLimit, 250)
	b.updatePVSimulation(noon, 1)
	assertFloatNear(t, b.pvActualPowerKW, 30)
}

func TestPVLimitWriteHandlersTrackLatestRegister(t *testing.T) {
	b := newPVTestBESS()
	noon := localTime(2026, 5, 18, 13, 0, 0)

	multiFrame := &mbserver.TCPFrame{Function: 16}
	mbserver.SetDataWithRegisterAndNumberAndValues(multiFrame, RegPVPercentLimit, 2, []uint16{500, 700})
	b.handleWriteHoldingRegisters(b.server, multiFrame)
	b.updatePVSimulation(noon, 1)
	assertFloatNear(t, b.pvActualPowerKW, 70)

	singleFrame := &mbserver.TCPFrame{Function: 6}
	mbserver.SetDataWithRegisterAndNumber(singleFrame, RegPVPercentLimit, 250)
	b.handleWriteHoldingRegister(b.server, singleFrame)
	b.updatePVSimulation(noon, 1)
	assertFloatNear(t, b.pvActualPowerKW, 30)
}

func TestPVRegisterSync(t *testing.T) {
	b := newPVTestBESS()
	b.pvActualPowerKW = 90
	b.pvTotalEnergyKWh = 70000
	b.pvDailyEnergyKWh = 123
	b.pvMonthlyEnergyKWh = 456
	b.pvYearlyEnergyKWh = 789
	b.pvDailyPeakPowerKW = 100

	b.syncPVRegisters()
	s := b.server.HoldingRegisters

	assertU32Register(t, s, RegPVTotalEnergyHi, 70000)
	assertU32Register(t, s, RegPVDailyEnergyHi, 123)
	assertU32Register(t, s, RegPVMonthlyEnergyHi, 456)
	assertU32Register(t, s, RegPVYearlyEnergyHi, 789)

	if got := s[RegPVRunStatus]; got != 5 {
		t.Fatalf("RegPVRunStatus = %v, want 5", got)
	}
	if got := s[RegPVRatedPower]; got != 1200 {
		t.Fatalf("RegPVRatedPower = %v, want 1200", got)
	}
	if got := s[RegPVGridFrequency]; got != 5000 {
		t.Fatalf("RegPVGridFrequency = %v, want 5000", got)
	}
	if got := uint16ToInt16(s[RegPVPowerFactor]); got != 1000 {
		t.Fatalf("RegPVPowerFactor = %v, want 1000", got)
	}
	if got := uint16ToInt16(s[RegPVACActivePower]); got != 900 {
		t.Fatalf("RegPVACActivePower = %v, want 900", got)
	}
	if got := s[RegPVInverterEfficiency]; got != 980 {
		t.Fatalf("RegPVInverterEfficiency = %v, want 980", got)
	}
	if got := uint16ToInt16(s[RegPVDailyPeakPower]); got != 1000 {
		t.Fatalf("RegPVDailyPeakPower = %v, want 1000", got)
	}
	if got := s[RegPVDCVoltage]; got != 8000 {
		t.Fatalf("RegPVDCVoltage = %v, want 8000", got)
	}
	if got := s[RegPVACVoltageA]; got == 0 {
		t.Fatal("RegPVACVoltageA should be non-zero")
	}
	if got := uint16ToInt16(s[RegPVACCurrentA]); got == 0 {
		t.Fatal("RegPVACCurrentA should be non-zero")
	}
}

func newPVTestBESS() *BESS {
	cfg := DefaultConfig()
	return NewBESS(&cfg, mbserver.NewServer())
}

func localTime(year int, month time.Month, day, hour, minute, second int) time.Time {
	return time.Date(year, month, day, hour, minute, second, 0, time.Local)
}

func assertFloatNear(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.001 {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func assertU32Register(t *testing.T, registers []uint16, highRegister uint16, want uint32) {
	t.Helper()
	got := uint32(registers[highRegister])<<16 | uint32(registers[highRegister+1])
	if got != want {
		t.Fatalf("U32 register %d = %v, want %v", highRegister, got, want)
	}
}
