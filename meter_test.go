package main

import (
	"math"
	"testing"
)

func TestMeterGridPowerSign(t *testing.T) {
	tests := []struct {
		name           string
		load, pv, bess float64
		want           float64
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
			m := newTestMeter(t)
			m.forwardKWh, m.reverseKWh = 0, 0
			m.Update(0, tc.load, tc.bess, tc.pv)
			if math.Abs(m.gridPowerKW-tc.want) > 0.001 {
				t.Errorf("gridPowerKW = %v, want %v", m.gridPowerKW, tc.want)
			}
		})
	}
}

func TestMeterEnergyAccumulation(t *testing.T) {
	m := newTestMeter(t)
	m.forwardKWh, m.reverseKWh = 0, 0

	// 60 kW import for 1 hour → 60 kWh forward
	m.Update(3600, 60, 0, 0)
	if math.Abs(m.forwardKWh-60) > 0.001 {
		t.Errorf("forward energy after 1h@60kW import = %v, want 60", m.forwardKWh)
	}
	if m.reverseKWh != 0 {
		t.Errorf("reverse should still be 0, got %v", m.reverseKWh)
	}

	// 40 kW export for 30 min → 20 kWh reverse
	m.Update(1800, 0, 0, 40)
	if math.Abs(m.reverseKWh-20) > 0.001 {
		t.Errorf("reverse energy after 30m@40kW export = %v, want 20", m.reverseKWh)
	}
	if math.Abs(m.forwardKWh-60) > 0.001 {
		t.Errorf("forward should remain 60, got %v", m.forwardKWh)
	}
}

func TestMeterPFAlwaysPositive(t *testing.T) {
	cases := []struct {
		name           string
		load, pv, bess float64
	}{
		{"import", 50, 0, 0},
		{"export via PV", 10, 80, 0},
		{"export via BESS discharge", 0, 0, -50},
		{"balanced", 30, 30, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMeter(t)
			m.Update(0, tc.load, tc.bess, tc.pv)
			m.Sync()
			pf := readS32Bank(m.bank.Holding, RegMeterPFTotalHi)
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
	m := newTestMeter(t)
	m.Update(0, 100, 0, 0)
	m.Sync()

	apparent := float64(readS32Bank(m.bank.Holding, RegMeterApparentPWTotalHi)) / 1000.0
	tanPhi := math.Sqrt(1-0.95*0.95) / 0.95
	q := 100 * tanPhi
	wantS := math.Sqrt(100*100 + q*q)
	if math.Abs(apparent-wantS) > 0.1 {
		t.Errorf("apparent power = %v, want %v (P=100, Q=%v)", apparent, wantS, q)
	}
}

func TestMeterCurrentSignFollowsActivePower(t *testing.T) {
	// Import: current positive
	m := newTestMeter(t)
	m.Update(0, 50, 0, 0)
	m.Sync()
	iA := readS32Bank(m.bank.Holding, RegMeterCurrentAHi)
	if iA <= 0 {
		t.Errorf("import current A = %v, want > 0", iA)
	}

	// Export: current negative
	m = newTestMeter(t)
	m.Update(0, 0, 0, 50)
	m.Sync()
	iA = readS32Bank(m.bank.Holding, RegMeterCurrentAHi)
	if iA >= 0 {
		t.Errorf("export current A = %v, want < 0", iA)
	}
}

func TestMeterFrequencyJitter(t *testing.T) {
	m := newTestMeter(t)
	seen := map[uint16]bool{}
	for i := 0; i < 100; i++ {
		m.Sync()
		seen[m.bank.ReadU16(RegMeterFrequency)] = true
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
	m := newTestMeter(t)
	m.forwardKWh, m.reverseKWh = 0, 0
	m.Update(-1, 100, 0, 0)
	if m.forwardKWh != 0 || m.reverseKWh != 0 {
		t.Errorf("negative dt should not accumulate: fwd=%v rev=%v", m.forwardKWh, m.reverseKWh)
	}
}

// TestSimulatorAggregatesMultiplePCSAndPV 验证多套电池+多套 PV 在电表处正确聚合。
func TestSimulatorAggregatesMultiplePCSAndPV(t *testing.T) {
	cfg := Config{
		Modbus: ModbusConfig{Address: ""},
		Grid:   GridConfig{Voltage: 220},
		BatteryUnits: []BatteryUnitConfig{
			{PCSSlaveID: 1, BMSSlaveID: 11, RatedCapacityKWh: 100, RatedPowerKW: 50, InitialSOC: 50, SOH: 100, BatteryVoltageFull: 1400, ClusterCount: 1},
			{PCSSlaveID: 2, BMSSlaveID: 12, RatedCapacityKWh: 100, RatedPowerKW: 50, InitialSOC: 50, SOH: 100, BatteryVoltageFull: 1400, ClusterCount: 1},
		},
		PVUnits: []PVUnitConfig{
			{SlaveID: 21, RatedPowerKW: 30},
			{SlaveID: 22, RatedPowerKW: 30},
		},
		Meters: []MeterConfig{{SlaveID: 31, Name: "main", IsMain: true}},
		Loads:  []LoadCfg{{Name: "load", RatedPowerKW: 80}},
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}

	sim := NewSimulator(&cfg, mustNewServer())

	// 强制各 unit 的功率以便确定性聚合
	sim.batteries[0].actualPowerKW = 10
	sim.batteries[1].actualPowerKW = -5
	sim.pvs[0].actualPowerKW = 12
	sim.pvs[1].actualPowerKW = 8
	sim.loads[0].actualPowerKW = 30

	sim.updateMeters(0)
	want := 30.0 + (10.0 + -5.0) - (12.0 + 8.0) // = 15
	if math.Abs(sim.meters[0].meter.gridPowerKW-want) > 0.001 {
		t.Fatalf("meter gridPower = %v, want %v", sim.meters[0].meter.gridPowerKW, want)
	}
}
