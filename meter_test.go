package main

import (
	"math"
	"testing"

	"aiwatt.net/ems/go-common/mbserver"
)

func newMeterTestBESS() *BESS {
	cfg := DefaultConfig()
	b := NewBESS(&cfg, mbserver.NewServer())
	b.weatherCoeff = 1.0
	b.pvActualPowerKW = 0
	b.loadActualPowerKW = 0
	b.actualPowerKW = 0
	b.meterForwardKWh = 0
	b.meterReverseKWh = 0
	return b
}

func readS32(regs []uint16, hi uint16) int32 {
	return int32(uint32(regs[hi])<<16 | uint32(regs[hi+1]))
}

func TestS32Roundtrip(t *testing.T) {
	server := mbserver.NewServer()
	cases := []int32{0, 1, -1, 12345, -12345, math.MaxInt32, math.MinInt32}
	for _, v := range cases {
		writeS32Holding(server, 100, v)
		got := readS32(server.HoldingRegisters, 100)
		if got != v {
			t.Errorf("S32 roundtrip: wrote %v, got %v", v, got)
		}
	}
}

func TestMeterGridPowerSign(t *testing.T) {
	tests := []struct {
		name string
		load, pv, bess float64
		want float64
	}{
		{"pure load import", 50, 0, 0, 50},
		{"pv covers load", 30, 30, 0, 0},
		{"pv exports surplus", 10, 50, 0, -40},
		{"bess charges from grid", 0, 0, 30, 30},
		{"bess discharges to grid", 0, 0, -30, -30},
		{"pv + bess discharge export", 20, 40, -30, -50},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := newMeterTestBESS()
			b.loadActualPowerKW = tc.load
			b.pvActualPowerKW = tc.pv
			b.actualPowerKW = tc.bess
			b.updateMeter(0)
			if math.Abs(b.meterGridPowerKW-tc.want) > 0.001 {
				t.Errorf("gridPowerKW = %v, want %v", b.meterGridPowerKW, tc.want)
			}
		})
	}
}

func TestMeterEnergyAccumulation(t *testing.T) {
	b := newMeterTestBESS()

	// 60 kW import for 1 hour → 60 kWh forward
	b.loadActualPowerKW = 60
	b.updateMeter(3600)
	if math.Abs(b.meterForwardKWh-60) > 0.001 {
		t.Errorf("forward energy after 1h@60kW import = %v, want 60", b.meterForwardKWh)
	}
	if b.meterReverseKWh != 0 {
		t.Errorf("reverse should still be 0, got %v", b.meterReverseKWh)
	}

	// Switch to 40 kW export for 30 min → 20 kWh reverse
	b.loadActualPowerKW = 0
	b.pvActualPowerKW = 40
	b.updateMeter(1800)
	if math.Abs(b.meterReverseKWh-20) > 0.001 {
		t.Errorf("reverse energy after 30m@40kW export = %v, want 20", b.meterReverseKWh)
	}
	if math.Abs(b.meterForwardKWh-60) > 0.001 {
		t.Errorf("forward should remain 60, got %v", b.meterForwardKWh)
	}
}

func TestMeterPFAlwaysPositive(t *testing.T) {
	// GB/T 17215: PF sign indicates inductive(+)/capacitive(-).
	// Load is inductive in our model, so PF must be >= 0 regardless of
	// active power direction.
	cases := []struct {
		name string
		load, pv, bess float64
	}{
		{"import", 50, 0, 0},
		{"export via PV", 10, 80, 0},
		{"export via BESS discharge", 0, 0, -50},
		{"balanced", 30, 30, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newMeterTestBESS()
			b.loadActualPowerKW = tc.load
			b.pvActualPowerKW = tc.pv
			b.actualPowerKW = tc.bess
			b.updateMeter(0)
			b.syncMeterRegisters()
			pf := readS32(b.server.HoldingRegisters, RegMeterPFTotalHi)
			if pf < 0 {
				t.Errorf("PF = %v, expected >= 0 (inductive)", pf)
			}
			if pf > 1000 {
				t.Errorf("PF = %v exceeds 1.000 scale", pf)
			}
		})
	}
}

func TestMeterApparentPowerCorrect(t *testing.T) {
	// S = √(P² + Q²) where Q = P_load × tan(arccos(0.95))
	b := newMeterTestBESS()
	b.loadActualPowerKW = 100 // gives Q ≈ 32.87 kVar
	b.updateMeter(0)
	b.syncMeterRegisters()

	apparent := float64(readS32(b.server.HoldingRegisters, RegMeterApparentPWTotalHi)) / 1000.0
	tanPhi := math.Sqrt(1-0.95*0.95) / 0.95
	q := 100 * tanPhi
	wantS := math.Sqrt(100*100 + q*q)
	if math.Abs(apparent-wantS) > 0.1 {
		t.Errorf("apparent power = %v, want %v (P=100, Q=%v)", apparent, wantS, q)
	}
}

func TestMeterCurrentSignFollowsActivePower(t *testing.T) {
	b := newMeterTestBESS()

	// Import: current positive
	b.loadActualPowerKW = 50
	b.updateMeter(0)
	b.syncMeterRegisters()
	iA := readS32(b.server.HoldingRegisters, RegMeterCurrentAHi)
	if iA <= 0 {
		t.Errorf("import current A = %v, want > 0", iA)
	}

	// Export: current negative
	b = newMeterTestBESS()
	b.pvActualPowerKW = 50
	b.updateMeter(0)
	b.syncMeterRegisters()
	iA = readS32(b.server.HoldingRegisters, RegMeterCurrentAHi)
	if iA >= 0 {
		t.Errorf("export current A = %v, want < 0", iA)
	}
}

func TestMeterFrequencyJitter(t *testing.T) {
	b := newMeterTestBESS()
	seen := map[uint16]bool{}
	for i := 0; i < 100; i++ {
		b.syncMeterRegisters()
		seen[b.server.HoldingRegisters[RegMeterFrequency]] = true
	}
	if len(seen) < 3 {
		t.Errorf("frequency does not vary, only saw values: %v", seen)
	}
	for v := range seen {
		// Within ±0.05 Hz of 50.00 Hz → register value within [4995, 5005]
		if v < 4990 || v > 5010 {
			t.Errorf("frequency %v outside expected jitter band", v)
		}
	}
}

func TestMeterDtNegativeIgnored(t *testing.T) {
	b := newMeterTestBESS()
	b.loadActualPowerKW = 100
	b.updateMeter(-1)
	if b.meterForwardKWh != 0 || b.meterReverseKWh != 0 {
		t.Errorf("negative dt should not accumulate: fwd=%v rev=%v",
			b.meterForwardKWh, b.meterReverseKWh)
	}
}
