package simulator

import (
	"math"
	"testing"

	"virtual_bess/internal/mbserver"
)

// latvia 分支保留日本单柜场景；合入 main 的通用仿真能力时，不能覆盖机柜拓扑和互感器配置。
func TestJapanScenarioConfigAndReactiveVoltageResponse(t *testing.T) {
	cfg, err := LoadConfig("../../config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Grid.Frequency != 60 || cfg.Grid.Voltage != 3810.5 || cfg.PCS.ACVoltage != 277.1 {
		t.Fatalf("Japan grid/PCS ratings changed: grid=%+v pcs=%+v", cfg.Grid, cfg.PCS)
	}
	if len(cfg.BatteryUnits) != 1 || len(cfg.PVUnits) != 0 || len(cfg.Loads) != 0 {
		t.Fatalf("expected one battery cabinet without PV or site load")
	}
	unit := cfg.BatteryUnits[0]
	if unit.PCSSlaveID != 1 || unit.BMSSlaveID != 11 || unit.ClusterCount != 20 ||
		unit.RatedPowerKW != 2000 || unit.RatedCapacityKWh != 5016 {
		t.Fatalf("Japan battery topology changed: %+v", unit)
	}
	if len(cfg.Meters) != 2 || cfg.Meters[0].SlaveID != 31 || !cfg.Meters[0].IsMain ||
		cfg.Meters[0].PTRatio != 66 || cfg.Meters[1].SlaveID != 32 || cfg.Meters[1].PTRatio != 1 {
		t.Fatalf("Japan PCC meter topology changed: %+v", cfg.Meters)
	}
	// 正负无功分别验证，保证保留的 PT=66/1 映射与新电压反馈同时生效。
	for _, q := range []float64{500, -500} {
		sim := NewSimulator(cfg, mbserver.NewServer())
		bu := sim.batteries[0]
		bu.actualReactiveKVAr = q
		for i := 0; i < 200; i++ {
			bu.UpdateGridVoltage(1)
			sim.updateMeters(1)
		}
		bu.Sync()
		// 480V 线电压对应 277.1V 相电压，±500kvar 在 2000kVA、X=0.06 下推动 ±1.5%。
		wantPCS := 277.1 * (1 + q*0.06/2000)
		if math.Abs(bu.busVoltageV-wantPCS) > 0.01 {
			t.Fatalf("Q=%v: PCS voltage=%v, want %v", q, bu.busVoltageV, wantPCS)
		}
		gotPCS := float64(bu.pcs.ReadU16(RegPCSVoltageA)) / 10
		if math.Abs(gotPCS-wantPCS) > wantPCS*0.006+0.1 {
			t.Fatalf("Q=%v: PCS voltage register=%v, want near %v", q, gotPCS, wantPCS)
		}
		for _, agg := range sim.meters {
			m := agg.meter
			if math.Abs(m.reactiveKVar+q) > 0.01 || m.gridPowerKW != 0 {
				t.Fatalf("Q=%v: meter %s power P=%v Q=%v", q, m.name, m.gridPowerKW, m.reactiveKVar)
			}
			// 保留场景显式配置 X=0.04；关口表和低压表均应与 PCS 同向变化。
			wantMeter := m.gridVoltage * (1 + q*0.04/2000)
			if math.Abs(m.busVoltage-wantMeter) > 0.01 {
				t.Fatalf("Q=%v: meter %s voltage=%v, want %v", q, m.name, m.busVoltage, wantMeter)
			}
			m.Sync()
			got := float64(m.bank.ReadU16(RegMeterVoltageA)) / 10 * m.ptRatio
			if math.Abs(got-wantMeter) > wantMeter*0.002+0.1*m.ptRatio {
				t.Fatalf("Q=%v: meter %s primary voltage=%v, want near %v", q, m.name, got, wantMeter)
			}
		}
	}
}
