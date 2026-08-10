package simulator

import (
	"math"
	"testing"
)

// 默认电池单元额定 120kW；所有用例都在该容量下验证，超出部分由视在容量钳制。
const testRatedPowerKW = 120

// TestReactiveModeConstantQFollowsSetpoint 恒定无功模式（A13=0）直接跟随 A2 设定值。
func TestReactiveModeConstantQFollowsSetpoint(t *testing.T) {
	bu := newReadyBattery(t)
	bu.pcs.WriteU16(RegPCSReactiveModeCmd, ReactiveModeConstQ)
	bu.pcs.WriteU16(RegPCSReactivePowerCmd, int16ToUint16(240))

	bu.ProcessPowerCommand()

	assertPowerNear(t, bu.actualReactiveKVAr, 24)
}

// TestReactiveModeConstantPFDerivesReactiveFromActive 恒定功率因数模式（A13=1）：
// PF=0.8、有功 60kW ⇒ |Q| = 60 * tan(acos 0.8) = 45；PF 正为感性、负为容性。
func TestReactiveModeConstantPFDerivesReactiveFromActive(t *testing.T) {
	for _, tc := range []struct {
		name   string
		pfRaw  int16
		wantQ  float64
		wantPF int16
	}{
		{name: "inductive", pfRaw: 800, wantQ: 45, wantPF: 80},
		{name: "capacitive", pfRaw: -800, wantQ: -45, wantPF: -80},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bu := newReadyBattery(t)
			bu.pcs.WriteU16(RegPCSReactiveModeCmd, ReactiveModeConstPF)
			bu.pcs.WriteU16(RegPCSPowerFactorCmd, int16ToUint16(tc.pfRaw))
			// 有功设定 600 = 放电 60kW（负充正放，内部同号）。
			bu.pcs.WriteU16(RegPCSPowerCmd, int16ToUint16(600))

			bu.ProcessPowerCommand()
			assertPowerNear(t, bu.actualReactiveKVAr, tc.wantQ)

			// 回读的功率因数必须带符号，且与设定值一致，否则二级 EMS 无法确认无功方向。
			bu.Sync()
			if got := uint16ToInt16(bu.pcs.ReadU16(RegPCSPowerFactor)); got != tc.wantPF {
				t.Fatalf("power factor readback = %d, want %d", got, tc.wantPF)
			}
		})
	}
}

// TestReactiveModeConstantPFIgnoresReactiveSetpoint 恒定 PF 模式下 A2 无功设定不参与出力，
// 防止切模式后旧的无功设定继续生效。
func TestReactiveModeConstantPFIgnoresReactiveSetpoint(t *testing.T) {
	bu := newReadyBattery(t)
	bu.pcs.WriteU16(RegPCSReactiveModeCmd, ReactiveModeConstPF)
	bu.pcs.WriteU16(RegPCSReactivePowerCmd, int16ToUint16(500))
	bu.pcs.WriteU16(RegPCSPowerFactorCmd, int16ToUint16(1000)) // PF=1 ⇒ 纯有功
	bu.pcs.WriteU16(RegPCSPowerCmd, int16ToUint16(600))

	bu.ProcessPowerCommand()

	if math.Abs(bu.actualReactiveKVAr) > 0.001 {
		t.Fatalf("actualReactiveKVAr = %v, want 0", bu.actualReactiveKVAr)
	}
}

// TestReactiveModeConstantPFUnsetRegisterMeansUnityPF PF 寄存器未整定（0）时按 1.0 处理，
// 不能当成「纯无功」，否则一切到该模式就满发无功。
func TestReactiveModeConstantPFUnsetRegisterMeansUnityPF(t *testing.T) {
	bu := newReadyBattery(t)
	bu.pcs.WriteU16(RegPCSReactiveModeCmd, ReactiveModeConstPF)
	bu.pcs.WriteU16(RegPCSPowerCmd, int16ToUint16(600))

	bu.ProcessPowerCommand()

	if math.Abs(bu.actualReactiveKVAr) > 0.001 {
		t.Fatalf("actualReactiveKVAr = %v, want 0", bu.actualReactiveKVAr)
	}
}

// TestReactiveModeConstantPFWithoutActivePowerOutputsZero 恒定 PF 是「无功随有功成比例」，
// 有功为 0 时无功也必须为 0。
func TestReactiveModeConstantPFWithoutActivePowerOutputsZero(t *testing.T) {
	bu := newReadyBattery(t)
	bu.pcs.WriteU16(RegPCSReactiveModeCmd, ReactiveModeConstPF)
	bu.pcs.WriteU16(RegPCSPowerFactorCmd, int16ToUint16(800))

	bu.ProcessPowerCommand()

	if bu.actualReactiveKVAr != 0 {
		t.Fatalf("actualReactiveKVAr = %v, want 0", bu.actualReactiveKVAr)
	}
}

// TestReactiveModeQUFollowsVoltageDeviation Q-U 模式（A13=2）按并网点电压偏差下垂：
// 死区内不动作，过压吸收无功（感性，正），欠压发出无功（容性，负），超过满出力偏差后饱和。
func TestReactiveModeQUFollowsVoltageDeviation(t *testing.T) {
	// 默认曲线：死区 1%，5% 偏差满出力；额定相电压 400V。
	for _, tc := range []struct {
		name      string
		voltageV  float64
		wantKVAr  float64
		tolerance float64
	}{
		{name: "inside deadband", voltageV: 402, wantKVAr: 0, tolerance: 0.001},
		{name: "overvoltage half", voltageV: 412, wantKVAr: 0.5 * testRatedPowerKW, tolerance: 0.5},
		{name: "overvoltage saturated", voltageV: 440, wantKVAr: testRatedPowerKW, tolerance: 0.8},
		{name: "undervoltage half", voltageV: 388, wantKVAr: -0.5 * testRatedPowerKW, tolerance: 0.5},
		{name: "undervoltage saturated", voltageV: 360, wantKVAr: -testRatedPowerKW, tolerance: 0.8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bu := newReadyBattery(t)
			bu.pcs.WriteU16(RegPCSReactiveModeCmd, ReactiveModeQU)
			bu.pcs.WriteU16(RegPCSGridVoltageCmd, uint16(tc.voltageV*10))

			bu.ProcessPowerCommand()

			if math.Abs(bu.actualReactiveKVAr-tc.wantKVAr) > tc.tolerance {
				t.Fatalf("actualReactiveKVAr = %v, want %v±%v", bu.actualReactiveKVAr, tc.wantKVAr, tc.tolerance)
			}
		})
	}
}

// TestReactiveModeQUIgnoresReactiveSetpoint Q-U 模式下 A2 无功设定不参与出力。
func TestReactiveModeQUIgnoresReactiveSetpoint(t *testing.T) {
	bu := newReadyBattery(t)
	bu.pcs.WriteU16(RegPCSReactiveModeCmd, ReactiveModeQU)
	bu.pcs.WriteU16(RegPCSReactivePowerCmd, int16ToUint16(500))

	bu.ProcessPowerCommand()

	if bu.actualReactiveKVAr != 0 {
		t.Fatalf("actualReactiveKVAr = %v, want 0 (voltage at nominal)", bu.actualReactiveKVAr)
	}
}

// TestGridVoltageOverrideDrivesReportedPhaseVoltage 强制电压既驱动 Q-U，也必须反映到
// 遥测相电压上，否则联调时看到的电压和无功出力对不上。
func TestGridVoltageOverrideDrivesReportedPhaseVoltage(t *testing.T) {
	bu := newReadyBattery(t)
	bu.pcs.WriteU16(RegPCSGridVoltageCmd, 4400) // 440.0 V

	bu.Sync()

	for _, reg := range []uint16{RegPCSVoltageA, RegPCSVoltageB, RegPCSVoltageC} {
		got := float64(bu.pcs.ReadU16(reg)) * 0.1
		// Sync 会叠加 ±0.5% 采样抖动。
		if math.Abs(got-440) > 440*0.006 {
			t.Fatalf("phase voltage at %d = %v, want near 440", reg, got)
		}
	}
}

// TestReactiveOutputLimitedByApparentCapacity 有功优先：额定 120kVA 下有功已占 96kW 时，
// 无功最多只剩 sqrt(120²-96²)=72kVAr，即使设定 120kVAr 也要被钳制。
func TestReactiveOutputLimitedByApparentCapacity(t *testing.T) {
	bu := newReadyBattery(t)
	bu.pcs.WriteU16(RegPCSReactiveModeCmd, ReactiveModeConstQ)
	bu.pcs.WriteU16(RegPCSPowerCmd, int16ToUint16(960))          // 96kW 放电
	bu.pcs.WriteU16(RegPCSReactivePowerCmd, int16ToUint16(1200)) // 120kVAr

	bu.ProcessPowerCommand()

	// 有功和无功各带 ±0.5% 跟踪抖动，且余量对有功偏差敏感（d√(S²-P²)/dP 在此点约 1.3），
	// 故用绝对容差而非 assertPowerNear 的比例容差。
	if math.Abs(bu.actualReactiveKVAr-72) > 1.5 {
		t.Fatalf("actualReactiveKVAr = %v, want 72±1.5", bu.actualReactiveKVAr)
	}
}

// TestReactiveOutputZeroWhenActiveUsesFullCapacity 有功跑满额定容量时无功几乎没有余量。
// 有功带 ±0.5% 抖动，落在额定值以下时仍有 √(120²-119.4²)≈12 kVAr 的理论余量，
// 因此断言的是「余量被容量圆约束住」而不是恒为 0。
func TestReactiveOutputZeroWhenActiveUsesFullCapacity(t *testing.T) {
	bu := newReadyBattery(t)
	bu.pcs.WriteU16(RegPCSReactiveModeCmd, ReactiveModeConstQ)
	bu.pcs.WriteU16(RegPCSPowerCmd, int16ToUint16(int16(testRatedPowerKW*10)))
	bu.pcs.WriteU16(RegPCSReactivePowerCmd, int16ToUint16(600)) // 设定 60kVAr

	bu.ProcessPowerCommand()

	const maxHeadroomKVAr = 13
	if math.Abs(bu.actualReactiveKVAr) > maxHeadroomKVAr {
		t.Fatalf("actualReactiveKVAr = %v, want |Q| <= %v", bu.actualReactiveKVAr, maxHeadroomKVAr)
	}
}

// TestUnknownReactiveModeFallsBackToConstantQ 越界模式码按恒定无功处理，
// 与真机「不支持的模式不改变已有出力方式」一致。
func TestUnknownReactiveModeFallsBackToConstantQ(t *testing.T) {
	bu := newReadyBattery(t)
	bu.pcs.WriteU16(RegPCSReactiveModeCmd, 7)
	bu.pcs.WriteU16(RegPCSReactivePowerCmd, int16ToUint16(240))

	bu.ProcessPowerCommand()

	assertPowerNear(t, bu.actualReactiveKVAr, 24)
}
