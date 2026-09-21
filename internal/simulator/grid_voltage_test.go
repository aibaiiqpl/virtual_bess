package simulator

import (
	"math"
	"testing"

	"virtual_bess/internal/mbserver"
)

// 默认 PCS 等效阻抗 R=0.015 / X=0.06（标幺，基准=单元额定 120kW），额定相电压 400V。
// 满功率放电抬升 1.5%、满额感性无功压低 6%。

// newCoupledBattery 构造一个自定义等效阻抗的电池单元，用于放大电压响应以便断言。
func newCoupledBattery(t *testing.T, coupling GridCouplingConfig) *BatteryUnit {
	t.Helper()
	cfg := DefaultConfig()
	cfg.PCS.Coupling = coupling
	sim := NewSimulator(&cfg, mbserver.NewServer())
	return sim.batteries[0]
}

// settleBusVoltage 反复推进电压惯性直到稳态，返回最后 settleTailTicks 拍的峰峰值，
// 供调用方断言闭环没有自激。
func settleBusVoltage(bu *BatteryUnit, ticks int) float64 {
	const tail = 20
	minV, maxV := math.Inf(1), math.Inf(-1)
	for i := 0; i < ticks; i++ {
		bu.UpdateGridVoltage(1)
		if i >= ticks-tail {
			minV = math.Min(minV, bu.busVoltageV)
			maxV = math.Max(maxV, bu.busVoltageV)
		}
	}
	return maxV - minV
}

// TestBusVoltageFollowsActivePower 充电压低母线电压、放电抬高，这是现场最直观的因果：
// 有功从电网流入（充电）在等效电阻上产生压降，反向注入（放电）则把电压顶起来。
func TestBusVoltageFollowsActivePower(t *testing.T) {
	for _, tc := range []struct {
		name    string
		powerKW float64
		wantV   float64
	}{
		// 负充正放：120kW 满功率，偏移 = ±120*0.015/120 = ±1.5%
		{name: "discharge raises", powerKW: 120, wantV: 406},
		{name: "charge drops", powerKW: -120, wantV: 394},
		{name: "idle stays nominal", powerKW: 0, wantV: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bu := newReadyBattery(t)
			bu.actualPowerKW = tc.powerKW
			settleBusVoltage(bu, 200)

			if math.Abs(bu.busVoltageV-tc.wantV) > 0.05 {
				t.Fatalf("busVoltageV = %v, want %v", bu.busVoltageV, tc.wantV)
			}
		})
	}
}

// TestBusVoltageFollowsReactivePower 无功方向与电压的因果：30014 正=感性=从电网吸收无功，
// 压低电压；负=容性=向电网发出无功，抬高电压。方向搞反会让 Q-U 变成正反馈。
func TestBusVoltageFollowsReactivePower(t *testing.T) {
	for _, tc := range []struct {
		name       string
		reactiveKV float64
		wantV      float64
	}{
		// 偏移 = ±120*0.06/120 = ±6%；IES900 原生口径正=容性。
		{name: "capacitive raises", reactiveKV: 120, wantV: 424},
		{name: "inductive drops", reactiveKV: -120, wantV: 376},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bu := newReadyBattery(t)
			bu.actualReactiveKVAr = tc.reactiveKV
			settleBusVoltage(bu, 200)

			if math.Abs(bu.busVoltageV-tc.wantV) > 0.05 {
				t.Fatalf("busVoltageV = %v, want %v", bu.busVoltageV, tc.wantV)
			}
		})
	}
}

// TestPhaseVoltageTelemetryFollowsBusVoltage 母线电压必须上送到 30070~30072：
// 二级 EMS 只能看到遥测，内部状态变了而遥测不动等于没变。
func TestPhaseVoltageTelemetryFollowsBusVoltage(t *testing.T) {
	bu := newReadyBattery(t)
	bu.actualPowerKW = -120 // 满功率充电
	settleBusVoltage(bu, 200)
	bu.Sync()

	for _, reg := range []uint16{RegPCSVoltageA, RegPCSVoltageB, RegPCSVoltageC} {
		got := float64(bu.pcs.ReadU16(reg)) * 0.1
		// Sync 会叠加 ±0.5% 采样抖动。
		if math.Abs(got-394) > 394*0.006 {
			t.Fatalf("phase voltage at %d = %v, want near 394", reg, got)
		}
	}
}

// TestQUDroopClosesLoopThroughBusVoltage Q-U 下垂闭环回归：
// 放电把电压顶到死区之上，PCS 必须自动吸收感性无功把电压压回来，并稳定在解析解上。
//
// 取 R=0.05 放大有功的电压贡献，使 60kW 放电产生 2.5% 偏移（默认 R 下只有 0.75%，
// 落在 1% 死区内，下垂根本不动作，测不出闭环）。
//
// 稳态解：dev = 0.025 - 0.06·ratio 且 ratio = (dev-0.01)/0.04
//
//	⇒ dev = 1.6%，ratio = 0.15 ⇒ Q = 18 kVAr，U = 406.4 V。
func TestQUDroopClosesLoopThroughBusVoltage(t *testing.T) {
	bu := newCoupledBattery(t, GridCouplingConfig{
		ResistancePU: 0.05, ReactancePU: 0.06, ResponseSeconds: 2,
	})
	bu.pcs.WriteU16(RegPCSReactiveModeCmd, ReactiveModeQU)
	bu.pcs.WriteU16(RegPCSPowerCmd, int16ToUint16(600)) // 60kW 放电

	var minV, maxV = math.Inf(1), math.Inf(-1)
	const ticks = 300
	for i := 0; i < ticks; i++ {
		bu.UpdateGridVoltage(1)
		bu.ProcessPowerCommand()
		if i >= ticks-20 {
			minV = math.Min(minV, bu.busVoltageV)
			maxV = math.Max(maxV, bu.busVoltageV)
		}
	}

	if math.Abs(bu.actualReactiveKVAr+18) > 1.0 {
		t.Fatalf("actualReactiveKVAr = %v, want -18±1 (inductive, pulling voltage back down)", bu.actualReactiveKVAr)
	}
	if math.Abs(bu.busVoltageV-406.4) > 0.5 {
		t.Fatalf("busVoltageV = %v, want 406.4±0.5", bu.busVoltageV)
	}
	// 环路增益 1.5 > 1，没有一阶惯性就会在相邻 tick 之间来回翻转。
	if maxV-minV > 0.2 {
		t.Fatalf("bus voltage oscillates over last 20 ticks: peak-to-peak %v V", maxV-minV)
	}
}

// TestBusVoltageDoesNotOscillateAtDefaultCoupling 默认整定下闭环同样要收敛。
func TestBusVoltageDoesNotOscillateAtDefaultCoupling(t *testing.T) {
	bu := newReadyBattery(t)
	bu.pcs.WriteU16(RegPCSReactiveModeCmd, ReactiveModeQU)
	bu.pcs.WriteU16(RegPCSPowerCmd, int16ToUint16(600))

	var minV, maxV = math.Inf(1), math.Inf(-1)
	const ticks = 300
	for i := 0; i < ticks; i++ {
		bu.UpdateGridVoltage(1)
		bu.ProcessPowerCommand()
		if i >= ticks-20 {
			minV = math.Min(minV, bu.busVoltageV)
			maxV = math.Max(maxV, bu.busVoltageV)
		}
	}
	if maxV-minV > 0.2 {
		t.Fatalf("bus voltage oscillates over last 20 ticks: peak-to-peak %v V", maxV-minV)
	}
}

// TestGridVoltageOverrideBypassesBusVoltage 强制电压是仿真旁路：写了它就应当完全接管，
// 本机自身的电压响应被屏蔽，否则联调时无法把电压钉在某一点单独检验下垂曲线。
func TestGridVoltageOverrideBypassesBusVoltage(t *testing.T) {
	bu := newReadyBattery(t)
	bu.actualPowerKW = -120 // 充电本应把电压压到 394
	settleBusVoltage(bu, 200)
	bu.pcs.WriteU16(RegPCSGridVoltageCmd, 4400) // 强制 440.0 V

	if got := bu.gridPhaseVoltage(); math.Abs(got-440) > 0.05 {
		t.Fatalf("gridPhaseVoltage = %v, want 440 (override wins)", got)
	}
}

// TestCouplingConfigRejectsInvalidImpedance 负阻抗会把充放电对电压的方向整个反过来，
// 运行时不报任何错，只能在启动时拦住。
func TestCouplingConfigRejectsInvalidImpedance(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  GridCouplingConfig
	}{
		{name: "negative resistance", cfg: GridCouplingConfig{ResistancePU: -0.01, ReactancePU: 0.04}},
		{name: "negative reactance", cfg: GridCouplingConfig{ResistancePU: 0.01, ReactancePU: -0.04}},
		{name: "saturating impedance", cfg: GridCouplingConfig{ResistancePU: 0.2, ReactancePU: 0.2}},
		{name: "negative base", cfg: GridCouplingConfig{ResistancePU: 0.01, BaseKVA: -1}},
		{name: "negative response", cfg: GridCouplingConfig{ResistancePU: 0.01, ResponseSeconds: -1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.cfg.validate("test"); err == nil {
				t.Fatalf("validate(%+v) = nil, want error", tc.cfg)
			}
		})
	}
}

// TestResolveCouplingKeepsExplicitZero 只想单独观察电抗贡献时要能把 R 显式整定成 0；
// 逐字段兜底会把这个 0 又填回默认值，故整块判空。
func TestResolveCouplingKeepsExplicitZero(t *testing.T) {
	got := resolveCoupling(GridCouplingConfig{ReactancePU: 0.04}, defaultPCSCoupling(), 120)

	if got.ResistancePU != 0 {
		t.Fatalf("ResistancePU = %v, want 0 (explicit zero preserved)", got.ResistancePU)
	}
	if got.BaseKVA != 120 {
		t.Fatalf("BaseKVA = %v, want 120 (auto base filled in)", got.BaseKVA)
	}
	if got.ResponseSeconds != defaultPCSCoupling().ResponseSeconds {
		t.Fatalf("ResponseSeconds = %v, want default", got.ResponseSeconds)
	}
}

// 默认并网点等效阻抗 R=0.01 / X=0.04（标幺，基准=站内 PCS 额定容量之和 120kW），额定 220V。

// settleMeterVoltage 反复推进电表电压惯性直到稳态。
func settleMeterVoltage(m *Meter, in MeterInput, ticks int) {
	for i := 0; i < ticks; i++ {
		m.Update(1, in)
	}
}

// TestMeterVoltageFollowsGridFlow 关口电压跟真实潮流走：
// 充电/带负载是从电网买电，压低关口电压；放电/光伏倒送是向电网卖电，抬高关口电压。
// 无功同理，感性（从电网吸收）压低、容性（向电网发出）抬高。
func TestMeterVoltageFollowsGridFlow(t *testing.T) {
	for _, tc := range []struct {
		name  string
		in    MeterInput
		wantV float64
	}{
		// 有功偏移 = ∓120*0.01/120 = ∓1%；PCSKW 充电为正。
		{name: "charging drops", in: MeterInput{PCSKW: 120}, wantV: 217.8},
		{name: "discharging raises", in: MeterInput{PCSKW: -120}, wantV: 222.2},
		{name: "pv export raises", in: MeterInput{PVKW: 120}, wantV: 222.2},
		// 无功偏移 = ∓120*0.12/120 = ∓12%；PCSKVAr 感性为正。
		// 无功灵敏度刻意远大于有功：关口电压要能被无功推得动，上游 Q(U) 闭环才测得出过冲。
		{name: "inductive drops", in: MeterInput{PCSKVAr: 120}, wantV: 193.6},
		{name: "capacitive raises", in: MeterInput{PCSKVAr: -120}, wantV: 246.4},
		{name: "idle stays nominal", in: MeterInput{}, wantV: 220},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMeter(t)
			settleMeterVoltage(m, tc.in, 300)

			if math.Abs(m.busVoltage-tc.wantV) > 0.05 {
				t.Fatalf("busVoltage = %v, want %v", m.busVoltage, tc.wantV)
			}
		})
	}
}

// TestMeterVoltageTelemetryFollowsBusVoltage 关口电压必须上送到电压寄存器，
// 二级 EMS 和现场只看得到遥测。
func TestMeterVoltageTelemetryFollowsBusVoltage(t *testing.T) {
	m := newTestMeter(t)
	settleMeterVoltage(m, MeterInput{PCSKW: 120}, 300) // 满功率充电
	m.Sync()

	// 默认 220V 对应 PT=1，寄存器即一次侧 ×10。
	for _, reg := range []uint16{RegMeterVoltageA, RegMeterVoltageB, RegMeterVoltageC} {
		got := float64(m.bank.ReadU16(reg)) * 0.1
		// Sync 会叠加 ±0.5% 采样抖动。
		if math.Abs(got-217.8) > 217.8*0.006 {
			t.Fatalf("meter voltage at %d = %v, want near 217.8", reg, got)
		}
	}
}

// TestMeterVoltageIgnoresDisplayFlowDirection flow_direction 只反转 forward/reverse 的
// 显示方向，不改变物理潮流。outflow 表上充电依然是从电网买电，电压依然要跌。
func TestMeterVoltageIgnoresDisplayFlowDirection(t *testing.T) {
	m := NewMeter(
		MeterConfig{SlaveID: 31, Name: "outflow", FlowDirection: "outflow"},
		220, GridCouplingConfig{}, 120, NewSlaveBank(31, false),
	)
	// updateMeters 对 outflow 表会把所有输入取反后再交给 Update，这里照做：
	// 物理上是 120kW 充电（买电），送进来的是 -120。
	settleMeterVoltage(m, MeterInput{PCSKW: -120}, 300)

	if math.Abs(m.busVoltage-217.8) > 0.05 {
		t.Fatalf("busVoltage = %v, want 217.8 (charging still sags the PCC)", m.busVoltage)
	}
}
