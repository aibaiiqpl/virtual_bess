package simulator

import (
	"math"
	"testing"
)

// TestMeterReflectsPCSReactive PCS 的无功必须体现在关口表上：此前无功只由负载按固定
// tanφ 推算，PCS 发多少无功关口表都是 0，站级无功平衡/功率因数考核无法在仿真环境验证。
func TestMeterReflectsPCSReactive(t *testing.T) {
	m := newTestMeter(t)

	// 无负载、无 PV，只有 PCS 放电 100kW + 吸收 60kvar（感性）
	m.Update(0, MeterInput{PCSKW: -100, PCSKVAr: 60})
	m.Sync()

	assertFloatNear(t, m.gridPowerKW, -100)
	assertFloatNear(t, m.reactiveKVar, 60)
	if got := readS32Bank(m.bank.Holding, RegMeterReactivePWTotalHi); got == 0 {
		t.Fatal("meter reactive power must follow PCS reactive output")
	}
}

// TestMeterApparentAndPFIncludeReactive 视在功率与功率因数要把无功算进去，
// 且 PF 带符号（正=感性、负=容性），与 PCS 侧 30060 口径一致。
func TestMeterApparentAndPFIncludeReactive(t *testing.T) {
	for _, tc := range []struct {
		name     string
		reactive float64
		wantPF   float64
	}{
		{name: "inductive", reactive: 40, wantPF: 0.6},
		{name: "capacitive", reactive: -40, wantPF: -0.6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMeter(t)
			// P=30, Q=±40 ⇒ S=50, |PF|=0.6
			m.Update(0, MeterInput{PCSKW: 30, PCSKVAr: tc.reactive})
			m.Sync()

			assertFloatNear(t, math.Hypot(m.gridPowerKW, m.reactiveKVar), 50)
			ptct := m.ptRatio * m.ctRatio
			if got := float64(readS32Bank(m.bank.Holding, RegMeterApparentPWTotalHi)) * ptct / 1000; math.Abs(got-50) > 0.1 {
				t.Fatalf("apparent power = %v, want 50", got)
			}
			if got := float64(readS32Bank(m.bank.Holding, RegMeterPFTotalHi)) / 1000; math.Abs(got-tc.wantPF) > 0.002 {
				t.Fatalf("power factor = %v, want %v", got, tc.wantPF)
			}
		})
	}
}

// TestMeterCurrentIncludesReactiveComponent 电流幅值跟视在功率，因此纯无功也要产生电流。
func TestMeterCurrentIncludesReactiveComponent(t *testing.T) {
	m := newTestMeter(t)
	m.Update(0, MeterInput{PCSKVAr: 60}) // 纯无功，有功为 0
	m.Sync()

	if got := readS32Bank(m.bank.Holding, RegMeterCurrentAHi); got == 0 {
		t.Fatal("pure reactive output must still draw current")
	}
}

// TestSimulatorAggregatesPCSReactiveIntoMeter 端到端：改电池单元的无功出力，
// 关口表无功跟着走。有功与无功在聚合时都要换向，规则一并锁住：
// 有功「负充正放 → 充电为正」，无功「按现场关口表方向取反」。
func TestSimulatorAggregatesPCSReactiveIntoMeter(t *testing.T) {
	sim := newTestSimulator(t)
	bu := sim.batteries[0]
	bu.actualPowerKW = 50      // 放电 50kW ⇒ 电表侧 -50（卖电）
	bu.actualReactiveKVAr = 30 // ⇒ 电表侧 -30

	sim.updateMeters(0)

	meter := sim.meters[0].meter
	assertFloatNear(t, meter.gridPowerKW, -50+meter.loadPowerKW-sim.pvs[0].ActualPowerKW())
	assertFloatNear(t, meter.reactiveKVar, meter.loadPowerKW*loadTanPhi()-30)
}

// TestMeterActiveSignIsChargePositive 关口表有功约定：正=充电（买电）、负=放电（卖电）。
func TestMeterActiveSignIsChargePositive(t *testing.T) {
	for _, tc := range []struct {
		name      string
		batteryKW float64 // BatteryUnit 内部值，负充正放
		wantSign  float64
	}{
		{name: "charging reads positive", batteryKW: -50, wantSign: 1},
		{name: "discharging reads negative", batteryKW: 50, wantSign: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sim := newTestSimulator(t)
			sim.loads = nil
			sim.pvs = nil
			for _, agg := range sim.meters {
				agg.loadIdx, agg.pvIdx = nil, nil
			}
			sim.batteries[0].actualPowerKW = tc.batteryKW

			sim.updateMeters(0)

			got := sim.meters[0].meter.gridPowerKW
			if math.Signbit(got) != math.Signbit(tc.wantSign) {
				t.Fatalf("battery %vkW -> meter %vkW, want sign %v", tc.batteryKW, got, tc.wantSign)
			}
			assertFloatNear(t, math.Abs(got), 50)
		})
	}
}
