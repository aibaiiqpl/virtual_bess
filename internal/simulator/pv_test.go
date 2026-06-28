package simulator

import (
	"testing"
	"time"

	"virtual_bess/internal/mbserver"
)

func TestPVNaturalPowerCurve(t *testing.T) {
	pv := newTestPV(t)

	tests := []struct {
		name string
		at   time.Time
		want float64
	}{
		{name: "before sunrise", at: localTime(2026, 5, 18, 5, 59, 0), want: 0},
		{name: "sunrise", at: localTime(2026, 5, 18, 6, 0, 0), want: 0},
		{name: "solar noon", at: localTime(2026, 5, 18, 12, 0, 0), want: 114},
		{name: "afternoon one hour", at: localTime(2026, 5, 18, 13, 0, 0), want: 110.11542731880104},
		{name: "afternoon two hours", at: localTime(2026, 5, 18, 14, 0, 0), want: 98.726896031426},
		{name: "early evening tail", at: localTime(2026, 5, 18, 17, 0, 0), want: 29.50538183364341},
		{name: "sunset", at: localTime(2026, 5, 18, 18, 0, 0), want: 0},
		{name: "after sunset", at: localTime(2026, 5, 18, 18, 1, 0), want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pv.naturalPowerKW(tt.at, 1.0) // 强制使用晴空系数。
			assertFloatNear(t, got, tt.want)
		})
	}
}

func TestPVNaturalPowerUsesConfiguredTimezone(t *testing.T) {
	oldLocation := pvLocation
	t.Cleanup(func() {
		pvLocation = oldLocation
	})
	SetPVTimezone("Europe/Lisbon")

	pv := newTestPV(t)

	beforeSunsetUTC := time.Date(2026, 5, 18, 16, 30, 0, 0, time.UTC) // WEST 本地 17:30。
	if got := pv.naturalPowerKW(beforeSunsetUTC, 1.0); got <= 0 {
		t.Fatalf("power before local sunset = %v, want positive", got)
	}

	afterSunsetUTC := time.Date(2026, 5, 18, 17, 1, 0, 0, time.UTC) // WEST 本地 18:01。
	if got := pv.naturalPowerKW(afterSunsetUTC, 1.0); got != 0 {
		t.Fatalf("power after local sunset = %v, want 0", got)
	}
}

func TestPVControlsStartStop(t *testing.T) {
	pv := newTestPV(t)
	noon := localTime(2026, 5, 18, 13, 0, 0)

	pv.UpdateSimulation(noon, 1, 1.0)
	if pv.actualPowerKW == 0 {
		t.Fatal("default PV state should be running")
	}

	pv.bank.WriteU16(RegPVShutdown, 1)
	pv.ProcessControls()
	pv.UpdateSimulation(noon, 1, 1.0)
	if pv.actualPowerKW != 0 {
		t.Fatalf("actualPowerKW after shutdown = %v, want 0", pv.actualPowerKW)
	}
	if got := pv.bank.ReadU16(RegPVShutdown); got != 0 {
		t.Fatalf("shutdown command register = %v, want 0", got)
	}

	pv.bank.WriteU16(RegPVStartup, 1)
	pv.ProcessControls()
	pv.UpdateSimulation(noon, 1, 1.0)
	if pv.actualPowerKW == 0 {
		t.Fatal("PV should resume generation after startup")
	}
	if got := pv.bank.ReadU16(RegPVStartup); got != 0 {
		t.Fatalf("startup command register = %v, want 0", got)
	}
}

func TestPVLatestLimitWins(t *testing.T) {
	pv := newTestPV(t)
	noon := localTime(2026, 5, 18, 13, 0, 0)

	pv.OnPVWrite(RegPVPercentLimit, 500)
	pv.UpdateSimulation(noon, 1, 1.0)
	assertFloatNear(t, pv.actualPowerKW, 60)

	pv.OnPVWrite(RegPVFixedLimit, 700)
	pv.UpdateSimulation(noon, 1, 1.0)
	assertFloatNear(t, pv.actualPowerKW, 70)

	pv.OnPVWrite(RegPVPercentLimit, 250)
	pv.UpdateSimulation(noon, 1, 1.0)
	assertFloatNear(t, pv.actualPowerKW, 30)
}

// TestPVLimitWriteHandlersTrackLatestRegister 通过 Simulator 路由层模拟 modbus 写。
func TestPVLimitWriteHandlersTrackLatestRegister(t *testing.T) {
	sim := newTestSimulator(t)
	pv := sim.pvs[0]
	noon := localTime(2026, 5, 18, 13, 0, 0)

	// FC16 写 [60002,60003] = [500, 700]，所以最后写入的 60003 (fixed) 生效。
	multi := &mbserver.TCPFrame{Function: 16, Device: pv.bank.SlaveID}
	mbserver.SetDataWithRegisterAndNumberAndValues(multi, RegPVPercentLimit, 2, []uint16{500, 700})
	if _, exc := sim.handleWriteMultipleHolding(sim.server, multi); exc != &mbserver.Success {
		t.Fatalf("write multiple failed: %v", exc)
	}
	pv.UpdateSimulation(noon, 1, 1.0)
	assertFloatNear(t, pv.actualPowerKW, 70)

	// FC6 写 60002 = 250，切回 percent 模式。
	single := &mbserver.TCPFrame{Function: 6, Device: pv.bank.SlaveID}
	mbserver.SetDataWithRegisterAndNumber(single, RegPVPercentLimit, 250)
	if _, exc := sim.handleWriteSingleHolding(sim.server, single); exc != &mbserver.Success {
		t.Fatalf("write single failed: %v", exc)
	}
	pv.UpdateSimulation(noon, 1, 1.0)
	assertFloatNear(t, pv.actualPowerKW, 30)
}

func TestPVRegisterSync(t *testing.T) {
	pv := newTestPV(t)
	pv.actualPowerKW = 90
	pv.totalEnergyKWh = 70000
	pv.dailyEnergyKWh = 123
	pv.monthlyEnergyKWh = 456
	pv.yearlyEnergyKWh = 789
	pv.dailyPeakPowerKW = 100
	pv.running = true

	pv.Sync()
	b := pv.bank

	if got := readU32Bank(b.Holding, RegPVTotalEnergyHi); got != 70000 {
		t.Fatalf("RegPVTotalEnergy = %v, want 70000", got)
	}
	if got := readU32Bank(b.Holding, RegPVDailyEnergyHi); got != 123 {
		t.Fatalf("RegPVDailyEnergy = %v, want 123", got)
	}
	if got := readU32Bank(b.Holding, RegPVMonthlyEnergyHi); got != 456 {
		t.Fatalf("RegPVMonthlyEnergy = %v, want 456", got)
	}
	if got := readU32Bank(b.Holding, RegPVYearlyEnergyHi); got != 789 {
		t.Fatalf("RegPVYearlyEnergy = %v, want 789", got)
	}
	if got := b.ReadU16(RegPVRunStatus); got != 5 {
		t.Fatalf("RegPVRunStatus = %v, want 5", got)
	}
	if got := b.ReadU16(RegPVRatedPower); got != 1200 {
		t.Fatalf("RegPVRatedPower = %v, want 1200", got)
	}
	if got := b.ReadU16(RegPVGridFrequency); got != 5000 {
		t.Fatalf("RegPVGridFrequency = %v, want 5000", got)
	}
	if got := uint16ToInt16(b.ReadU16(RegPVPowerFactor)); got != 1000 {
		t.Fatalf("RegPVPowerFactor = %v, want 1000", got)
	}
	if got := uint16ToInt16(b.ReadU16(RegPVACActivePower)); got != 900 {
		t.Fatalf("RegPVACActivePower = %v, want 900", got)
	}
	if got := b.ReadU16(RegPVInverterEfficiency); got != 980 {
		t.Fatalf("RegPVInverterEfficiency = %v, want 980", got)
	}
	if got := uint16ToInt16(b.ReadU16(RegPVDailyPeakPower)); got != 1000 {
		t.Fatalf("RegPVDailyPeakPower = %v, want 1000", got)
	}
	if got := b.ReadU16(RegPVDCVoltage); got != 8000 {
		t.Fatalf("RegPVDCVoltage = %v, want 8000", got)
	}
	if got := b.ReadU16(RegPVACVoltageA); got == 0 {
		t.Fatal("RegPVACVoltageA should be non-zero")
	}
	if got := uint16ToInt16(b.ReadU16(RegPVACCurrentA)); got == 0 {
		t.Fatal("RegPVACCurrentA should be non-zero")
	}
}
