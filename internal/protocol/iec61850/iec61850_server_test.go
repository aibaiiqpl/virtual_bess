//go:build iec61850

package iec61850sim

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/go-bindings/iec61850"
	"virtual_bess/internal/mbserver"
	"virtual_bess/internal/simulator"
)

func TestIEC61850ModelContainsCoreCIDReferences(t *testing.T) {
	model, err := loadIEC61850Model("TEMPLATE")
	if err != nil {
		t.Fatalf("loadIEC61850Model() error = %v", err)
	}
	defer model.Destroy()

	// 对齐现场 CID：遥调走 setGGIO1 APC（Oper.ctlVal.f 写、mxVal.f 回读），
	// 遥控走 ctlGAPC1 SPC（Oper.ctlVal），遥测在 MEAS/PIGO measGGIO 下。
	for _, ref := range []string{
		"TEMPLATECTRL/setGGIO1.APCS1.Oper.ctlVal.f",
		"TEMPLATECTRL/setGGIO1.APCS1.mxVal.f",
		"TEMPLATECTRL/setGGIO1.APCS2.Oper.ctlVal.f",
		"TEMPLATECTRL/setGGIO1.APCS6.Oper.ctlVal.f",
		"TEMPLATECTRL/setGGIO1.APCS6.mxVal.f",
		"TEMPLATECTRL/setGGIO1.APCS13.Oper.ctlVal.f",
		// 北向 5137「无功模式回读」的源点，emu 的 statusPoint 直接挂在这里。
		"TEMPLATECTRL/setGGIO1.APCS13.mxVal.f",
		"TEMPLATECTRL/setGGIO1.APCS9.Oper.ctlVal.f",
		"TEMPLATECTRL/setGGIO1.APCS10.Oper.ctlVal.f",
		"TEMPLATECTRL/ctlGAPC1.SPCSO2.Oper.ctlVal",
		"TEMPLATECTRL/ctlGAPC1.SPCSO5.Oper.ctlVal",
		"TEMPLATEMEAS/measGGIO1.AnIn7.mag.f",
		"TEMPLATEMEAS/measGGIO2.AnIn1.mag.f",
		"TEMPLATEPIGO/measGGIO1.AnIn1.mag.f",
		"TEMPLATEPIGO/measGGIO1.AnIn3.mag.i",
		"TEMPLATEPIGO/measGGIO1.AnIn9.mag.f",
	} {
		if model.GetModelNodeByObjectReference(ref) == nil {
			t.Fatalf("model node %s not found", ref)
		}
	}
}

func TestIEC61850ConfiguredIEDNameRenamesObjectReferences(t *testing.T) {
	model, err := loadIEC61850Model("pcs01")
	if err != nil {
		t.Fatalf("loadIEC61850Model(pcs01) error = %v", err)
	}
	defer model.Destroy()

	// 配置 ied_name=pcs01 后，对象引用前缀应整体从 TEMPLATE 变为 pcs01。
	if model.GetModelNodeByObjectReference("pcs01CTRL/setGGIO1.APCS1.Oper.ctlVal.f") == nil {
		t.Fatal("pcs01CTRL/setGGIO1.APCS1.Oper.ctlVal.f not found")
	}
	if model.GetModelNodeByObjectReference("pcs01PIGO/measGGIO1.AnIn1.mag.f") == nil {
		t.Fatal("pcs01PIGO/measGGIO1.AnIn1.mag.f not found")
	}
	// 旧前缀不应再存在。
	if model.GetModelNodeByObjectReference("TEMPLATECTRL/setGGIO1.APCS1.Oper.ctlVal.f") != nil {
		t.Fatal("TEMPLATE prefix should be gone after rename")
	}
}

func TestIEC61850MultiEndpointDistinctIEDNamesControlIndependently(t *testing.T) {
	port1 := freeTCPPort(t)
	port2 := freeTCPPort(t)
	sim := simulator.NewSimulator(twoBatteryConfig(), mustNewServer())
	svc, err := StartServer(simulator.IEC61850Config{
		Enabled: true,
		Devices: []simulator.IEC61850DeviceConfig{
			{PCSSlaveID: 1, Address: fmt.Sprintf("127.0.0.1:%d", port1), IEDName: "pcs01"},
			{PCSSlaveID: 2, Address: fmt.Sprintf("127.0.0.1:%d", port2), IEDName: "pcs02"},
		},
	}, sim)
	if err != nil {
		t.Fatalf("StartServer() error = %v", err)
	}
	defer svc.Close()
	svc.Sync()

	client1 := newIEC61850TestClient(t, port1)
	defer client1.Close()
	client2 := newIEC61850TestClient(t, port2)
	defer client2.Close()

	// 每个端点用自己的 IED 名前缀寻址，证明多 BESS 单元各自独立建模。
	if err := client1.ControlByControlModelAPC("pcs01CTRL/setGGIO1.APCS1",
		iec61850.CONTROL_MODEL_DIRECT_NORMAL, iec61850.NewControlObjectParamAPC(11)); err != nil {
		t.Fatalf("client1 Control(pcs01) error = %v", err)
	}
	if err := client2.ControlByControlModelAPC("pcs02CTRL/setGGIO1.APCS1",
		iec61850.CONTROL_MODEL_DIRECT_NORMAL, iec61850.NewControlObjectParamAPC(22)); err != nil {
		t.Fatalf("client2 Control(pcs02) error = %v", err)
	}

	waitRegister(t, sim.BatteryUnits()[0].PCSBank(), simulator.RegPCSPowerCmd, 110)
	waitRegister(t, sim.BatteryUnits()[1].PCSBank(), simulator.RegPCSPowerCmd, 220)
}

func TestIEC61850ActivePowerControlWritesPCSCommand(t *testing.T) {
	sim := simulator.NewSimulator(singleBatteryConfig(), mustNewServer())
	svc := &iec61850Server{sim: sim}

	result := svc.ctlActivePower(nil, nil, &iec61850.MmsValue{Type: iec61850.Float, Value: float32(12.3)}, false)
	if result != iec61850.CONTROL_RESULT_OK {
		t.Fatalf("ctlActivePower() = %v, want OK", result)
	}
	if got := registerInt16(sim.BatteryUnits()[0].PCSBank().ReadU16(simulator.RegPCSPowerCmd)); got != 123 {
		t.Fatalf("PCS power command = %d, want 123", got)
	}
}

func TestIEC61850ActivePowerControlAcceptsAnalogueStruct(t *testing.T) {
	sim := simulator.NewSimulator(singleBatteryConfig(), mustNewServer())
	svc := &iec61850Server{sim: sim}

	// APC 控制下发时 ctlVal 是 AnalogueValue 结构体，取首元素 f。
	ctlVal := &iec61850.MmsValue{Type: iec61850.Structure, Value: []*iec61850.MmsValue{
		{Type: iec61850.Float, Value: float32(-50)},
	}}
	if result := svc.ctlActivePower(nil, nil, ctlVal, false); result != iec61850.CONTROL_RESULT_OK {
		t.Fatalf("ctlActivePower(struct) = %v, want OK", result)
	}
	if got := registerInt16(sim.BatteryUnits()[0].PCSBank().ReadU16(simulator.RegPCSPowerCmd)); got != -500 {
		t.Fatalf("PCS power command = %d, want -500", got)
	}
}

// TestIEC61850ActivePowerSignMatchesTelemetry 锁住「设定与遥测同号」这个不变量：
// 真机 IES900 的 61850 有功是负充正放，仿真器全线同一套约定，链路上不做任何取反。
// 此前遥测取自内部「正充负放」的实际功率，与设定反号，表现为下发放电、回读充电。
func TestIEC61850ActivePowerSignMatchesTelemetry(t *testing.T) {
	for _, tc := range []struct {
		name       string
		setpointKW float32
		wantCharge bool
	}{
		{name: "negative setpoint charges", setpointKW: -100, wantCharge: true},
		{name: "positive setpoint discharges", setpointKW: 100, wantCharge: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sim := simulator.NewSimulator(singleBatteryConfig(), mustNewServer())
			svc := &iec61850Server{sim: sim}
			bu := sim.BatteryUnits()[0]

			if result := svc.ctlActivePower(nil, nil,
				&iec61850.MmsValue{Type: iec61850.Float, Value: tc.setpointKW}, false); result != iec61850.CONTROL_RESULT_OK {
				t.Fatalf("ctlActivePower() = %v, want OK", result)
			}
			bu.ProcessPowerCommand()
			bu.Sync()

			// 内部功率同为「负充正放」：负=充电。
			if charging := bu.ActualPowerKW() < 0; charging != tc.wantCharge {
				t.Fatalf("actual power = %v kW, want charging=%v", bu.ActualPowerKW(), tc.wantCharge)
			}
			values := svc.gooseValues(bu, 0)
			if sameSign := (values.activeKW > 0) == (tc.setpointKW > 0); !sameSign {
				t.Fatalf("telemetry activeKW = %v, setpoint = %v: signs must match",
					values.activeKW, tc.setpointKW)
			}
			if got := values.activeSetpointKW; got != tc.setpointKW {
				t.Fatalf("GOOSE active setpoint = %v, want %v", got, tc.setpointKW)
			}
		})
	}
}

func TestIEC61850ActivePowerControlRejectsOutOfRange(t *testing.T) {
	sim := simulator.NewSimulator(singleBatteryConfig(), mustNewServer())
	svc := &iec61850Server{sim: sim}

	result := svc.ctlActivePower(nil, nil, &iec61850.MmsValue{Type: iec61850.Float, Value: float32(4000)}, false)
	if result != iec61850.CONTROL_RESULT_FAILED {
		t.Fatalf("ctlActivePower() = %v, want FAILED", result)
	}
	if got := sim.BatteryUnits()[0].PCSBank().ReadU16(simulator.RegPCSPowerCmd); got != 0 {
		t.Fatalf("PCS power command = %d, want unchanged zero", got)
	}
}

func TestIEC61850ReactivePowerControlWritesPCSCommand(t *testing.T) {
	sim := simulator.NewSimulator(singleBatteryConfig(), mustNewServer())
	svc := &iec61850Server{sim: sim}

	result := svc.ctlReactivePower(nil, nil, &iec61850.MmsValue{Type: iec61850.Float, Value: float32(-12.3)}, false)
	if result != iec61850.CONTROL_RESULT_OK {
		t.Fatalf("ctlReactivePower() = %v, want OK", result)
	}
	raw := sim.BatteryUnits()[0].PCSBank().ReadU16(simulator.RegPCSReactivePowerCmd)
	if got := registerInt16(raw); got != -123 {
		t.Fatalf("PCS reactive power command = %d, want -123", got)
	}
}

func TestIEC61850ReactivePowerControlRejectsOutOfRange(t *testing.T) {
	sim := simulator.NewSimulator(singleBatteryConfig(), mustNewServer())
	svc := &iec61850Server{sim: sim}

	result := svc.ctlReactivePower(nil, nil, &iec61850.MmsValue{Type: iec61850.Float, Value: float32(4000)}, false)
	if result != iec61850.CONTROL_RESULT_FAILED {
		t.Fatalf("ctlReactivePower() = %v, want FAILED", result)
	}
	if got := sim.BatteryUnits()[0].PCSBank().ReadU16(simulator.RegPCSReactivePowerCmd); got != 0 {
		t.Fatalf("PCS reactive power command = %d, want unchanged zero", got)
	}
}

// A6「恒定功率因数设定值」：浮点 PF 按 0.001 标度落到仿真寄存器。
func TestIEC61850PowerFactorControlWritesPCSCommand(t *testing.T) {
	sim := simulator.NewSimulator(singleBatteryConfig(), mustNewServer())
	svc := &iec61850Server{sim: sim}

	result := svc.ctlPowerFactor(nil, nil, &iec61850.MmsValue{Type: iec61850.Float, Value: float32(-0.85)}, false)
	if result != iec61850.CONTROL_RESULT_OK {
		t.Fatalf("ctlPowerFactor() = %v, want OK", result)
	}
	raw := sim.BatteryUnits()[0].PCSBank().ReadU16(simulator.RegPCSPowerFactorCmd)
	if got := registerInt16(raw); got != -850 {
		t.Fatalf("PCS power factor command = %d, want -850", got)
	}
}

// |PF| > 1 无物理意义，必须拒绝而不是截断，否则点表标度写错时不会暴露。
func TestIEC61850PowerFactorControlRejectsOutOfRange(t *testing.T) {
	sim := simulator.NewSimulator(singleBatteryConfig(), mustNewServer())
	svc := &iec61850Server{sim: sim}

	result := svc.ctlPowerFactor(nil, nil, &iec61850.MmsValue{Type: iec61850.Float, Value: float32(1.2)}, false)
	if result != iec61850.CONTROL_RESULT_FAILED {
		t.Fatalf("ctlPowerFactor() = %v, want FAILED", result)
	}
	if got := sim.BatteryUnits()[0].PCSBank().ReadU16(simulator.RegPCSPowerFactorCmd); got != 0 {
		t.Fatalf("PCS power factor command = %d, want unchanged zero", got)
	}
}

// A13「无功功率设定模式」只接受 IES900 原生码 0~2。
func TestIEC61850ReactiveModeControlWritesNativeCode(t *testing.T) {
	for _, mode := range []uint16{
		simulator.ReactiveModeConstQ,
		simulator.ReactiveModeConstPF,
		simulator.ReactiveModeQU,
	} {
		sim := simulator.NewSimulator(singleBatteryConfig(), mustNewServer())
		svc := &iec61850Server{sim: sim}

		result := svc.ctlReactiveMode(nil, nil, &iec61850.MmsValue{Type: iec61850.Float, Value: float32(mode)}, false)
		if result != iec61850.CONTROL_RESULT_OK {
			t.Fatalf("ctlReactiveMode(%d) = %v, want OK", mode, result)
		}
		if got := sim.BatteryUnits()[0].PCSBank().ReadU16(simulator.RegPCSReactiveModeCmd); got != mode {
			t.Fatalf("PCS reactive mode = %d, want %d", got, mode)
		}
	}
}

// 二级 EMS 的 4(Q-U) 必须经 emu 点表 dzPoint 换码成原生 2 才下发；
// 未换码直接透传到这里要被拒绝，否则点表换码写错会被仿真器悄悄吞掉。
func TestIEC61850ReactiveModeControlRejectsEMSEnumeration(t *testing.T) {
	sim := simulator.NewSimulator(singleBatteryConfig(), mustNewServer())
	svc := &iec61850Server{sim: sim}

	result := svc.ctlReactiveMode(nil, nil, &iec61850.MmsValue{Type: iec61850.Float, Value: float32(4)}, false)
	if result != iec61850.CONTROL_RESULT_FAILED {
		t.Fatalf("ctlReactiveMode(4) = %v, want FAILED", result)
	}
	if got := sim.BatteryUnits()[0].PCSBank().ReadU16(simulator.RegPCSReactiveModeCmd); got != 0 {
		t.Fatalf("PCS reactive mode = %d, want unchanged zero", got)
	}
}

func TestIEC61850GooseValuesReadReactiveCommandAndOutput(t *testing.T) {
	sim := simulator.NewSimulator(singleBatteryConfig(), mustNewServer())
	battery := sim.BatteryUnits()[0]
	battery.PCSBank().WriteU16(simulator.RegPCSReactivePowerCmd, 0xFF85)
	battery.PCSBank().WriteU16(simulator.RegPCSTotalReactPW, 456)
	svc := &iec61850Server{sim: sim}

	values := svc.gooseValues(battery, 0)
	if values.reactSetpointKVAr != float32(-12.3) {
		t.Fatalf("reactive setpoint = %v, want -12.3", values.reactSetpointKVAr)
	}
	if values.reactiveKVAr != float32(45.6) {
		t.Fatalf("reactive output = %v, want 45.6", values.reactiveKVAr)
	}
}

func TestIEC61850PCSCommandRejectsUnknownCommand(t *testing.T) {
	sim := simulator.NewSimulator(singleBatteryConfig(), mustNewServer())
	svc := &iec61850Server{sim: sim}

	result := svc.ctlPCSCommand(nil, nil, &iec61850.MmsValue{Type: iec61850.Float, Value: float32(99)}, false)
	if result != iec61850.CONTROL_RESULT_FAILED {
		t.Fatalf("ctlPCSCommand() = %v, want FAILED", result)
	}
}

func TestIEC61850StartStopControlWritesStartup(t *testing.T) {
	sim := simulator.NewSimulator(singleBatteryConfig(), mustNewServer())
	svc := &iec61850Server{sim: sim}

	if result := svc.ctlStartStop(nil, nil, &iec61850.MmsValue{Type: iec61850.Boolean, Value: true}, false); result != iec61850.CONTROL_RESULT_OK {
		t.Fatalf("ctlStartStop(true) = %v, want OK", result)
	}
	if got := sim.BatteryUnits()[0].PCSBank().ReadU16(simulator.RegPCSStartup); got != 1 {
		t.Fatalf("PCS startup = %d, want 1", got)
	}
}

// TestIEC61850CellVoltageAlarmIgnoresNormalSpread 单体电压告警不能被正常工作区的随机
// 极差撞出来：SOC 88.7% 时均值 3.51V，最高电芯随机上浮 6~64mV 可到 3.574V，
// 旧阈值 3.55V 会让 emu 侧每隔几秒报一次三级故障，把 EMU 系统故障一直拉起来。
func TestIEC61850CellVoltageAlarmIgnoresNormalSpread(t *testing.T) {
	sim := simulator.NewSimulator(singleBatteryConfig(), mustNewServer())
	svc := &iec61850Server{sim: sim}
	bu := sim.BatteryUnits()[0]

	for _, mv := range []uint16{3510, 3550, 3574, 3600} {
		bu.BMSBank().WriteU16(simulator.RegBMSCellVMax, mv)
		if svc.evaluateAlarms(bu).cellVHigh {
			t.Fatalf("cell voltage %dmV should not raise alarm", mv)
		}
	}
}

// TestIEC61850CellVoltageAlarmHasHysteresis 置位后要回落到满充电压以下才消除，
// 避免真到满充时在阈值附近反复置位/消除。
func TestIEC61850CellVoltageAlarmHasHysteresis(t *testing.T) {
	sim := simulator.NewSimulator(singleBatteryConfig(), mustNewServer())
	svc := &iec61850Server{sim: sim}
	bu := sim.BatteryUnits()[0]
	bms := bu.BMSBank()

	bms.WriteU16(simulator.RegBMSCellVMax, alarmCellVMaxHighMV)
	if !svc.evaluateAlarms(bu).cellVHigh {
		t.Fatal("cell voltage at raise threshold should alarm")
	}
	// 回差带内（clear < v < raise）保持置位
	bms.WriteU16(simulator.RegBMSCellVMax, alarmCellVMaxClearMV+10)
	if !svc.evaluateAlarms(bu).cellVHigh {
		t.Fatal("alarm must stay latched inside the hysteresis band")
	}
	bms.WriteU16(simulator.RegBMSCellVMax, alarmCellVMaxClearMV)
	if svc.evaluateAlarms(bu).cellVHigh {
		t.Fatal("alarm must clear at or below the clear threshold")
	}
}

// TestAlarmHysteresis 回差判定本身：达到 raise 置位，回落到 clear 以下才消除，
// 两者之间保持原状态。SOC 与单体电压两个告警共用这套逻辑。
func TestAlarmHysteresis(t *testing.T) {
	latched := false
	for _, tc := range []struct {
		value float64
		want  bool
		desc  string
	}{
		{value: 90, want: false, desc: "below raise"},
		{value: 97.9, want: false, desc: "just below raise"},
		{value: 98, want: true, desc: "at raise"},
		{value: 97, want: true, desc: "inside band stays latched"},
		{value: 96.1, want: true, desc: "just above clear stays latched"},
		{value: 96, want: false, desc: "at clear"},
		{value: 97, want: false, desc: "inside band stays cleared"},
	} {
		if got := alarmWithHysteresis(&latched, tc.value, 98, 96); got != tc.want {
			t.Fatalf("%s: value %v -> %v, want %v", tc.desc, tc.value, got, tc.want)
		}
	}
}

// TestIEC61850SOCAlarmUsesFullChargeThreshold SOC 满充告警只在接近满充时触发。
func TestIEC61850SOCAlarmUsesFullChargeThreshold(t *testing.T) {
	for _, tc := range []struct {
		soc  float64
		want bool
	}{
		{soc: 90, want: false},
		{soc: 97, want: false},
		{soc: alarmSOCHighPercent, want: true},
	} {
		cfg := singleBatteryConfig()
		cfg.BatteryUnits[0].InitialSOC = tc.soc
		sim := simulator.NewSimulator(cfg, mustNewServer())
		svc := &iec61850Server{sim: sim}
		if got := svc.evaluateAlarms(sim.BatteryUnits()[0]).socHigh; got != tc.want {
			t.Fatalf("SOC %v%% -> alarm %v, want %v", tc.soc, got, tc.want)
		}
	}
}

// TestIEC61850AlarmLatchIsPerServer 每套 PCS 一个 iec61850Server，锁存位不能串。
func TestIEC61850AlarmLatchIsPerServer(t *testing.T) {
	sim := simulator.NewSimulator(twoBatteryConfig(), mustNewServer())
	svc1 := &iec61850Server{sim: sim}
	svc2 := &iec61850Server{sim: sim}
	bu1, bu2 := sim.BatteryUnits()[0], sim.BatteryUnits()[1]

	bu1.BMSBank().WriteU16(simulator.RegBMSCellVMax, alarmCellVMaxHighMV)
	bu2.BMSBank().WriteU16(simulator.RegBMSCellVMax, alarmCellVMaxClearMV)
	if !svc1.evaluateAlarms(bu1).cellVHigh {
		t.Fatal("PCS1 should alarm")
	}
	if svc2.evaluateAlarms(bu2).cellVHigh {
		t.Fatal("PCS2 must not inherit PCS1 latch")
	}
}

func TestIEC61850GooseDataSetContainsCIDTelemetryValues(t *testing.T) {
	values := iec61850TelemetryValues{
		ratedPowerKW:      2500,
		socPercent:        31.2,
		pcsStatus:         5,
		activeKW:          -123.4,
		reactiveKVAr:      0,
		maxChargeKW:       2000,
		maxDischargeKW:    2100,
		activeSetpointKW:  -100,
		reactSetpointKVAr: 15,
	}
	dataSet, err := buildIEC61850GooseDataSet(values)
	if err != nil {
		t.Fatalf("buildIEC61850GooseDataSet() error = %v", err)
	}
	defer dataSet.Destroy()

	if got := dataSet.Size(); got != 9 {
		t.Fatalf("GOOSE data set size = %d, want 9", got)
	}
}

func TestIEC61850ServerMMSReadAndControlSmoke(t *testing.T) {
	port := freeTCPPort(t)
	sim := simulator.NewSimulator(singleBatteryConfig(), mustNewServer())
	svc, err := StartServer(simulator.IEC61850Config{Enabled: true, Address: fmt.Sprintf("127.0.0.1:%d", port)}, sim)
	if err != nil {
		t.Fatalf("StartServer() error = %v", err)
	}
	defer svc.Close()
	svc.Sync()

	client, err := iec61850.NewClient(iec61850.Settings{
		Host:           "127.0.0.1",
		Port:           port,
		ConnectTimeout: 1000,
		RequestTimeout: 1000,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()

	ratedPower, err := client.ReadFloat("TEMPLATEPIGO/measGGIO1.AnIn1.mag.f", iec61850.MX)
	if err != nil {
		t.Fatalf("ReadFloat(ratedPower) error = %v", err)
	}
	if ratedPower != float32(sim.BatteryUnits()[0].RatedPowerKW()) {
		t.Fatalf("ratedPower = %v, want %v", ratedPower, sim.BatteryUnits()[0].RatedPowerKW())
	}

	if err := client.ControlByControlModelAPC("TEMPLATECTRL/setGGIO1.APCS1",
		iec61850.CONTROL_MODEL_DIRECT_NORMAL, iec61850.NewControlObjectParamAPC(12)); err != nil {
		t.Fatalf("Control(APCS1) error = %v", err)
	}
	// 服务端控制回调在 MMS server 线程异步落寄存器，与真实设备下发延迟一致，轮询回读。
	waitRegister(t, sim.BatteryUnits()[0].PCSBank(), simulator.RegPCSPowerCmd, 120)
}

// TestIEC61850ReactiveModeRoundTripThroughMMS 走完整 MMS 链路验证无功模式：
// Operate 下发 A13 原生码 → 落到仿真寄存器 → Sync 后从 mxVal.f 回读。
// mxVal.f 正是 emu 设备级点表里北向 5137「无功模式回读」的源点，
// 回读缺失会让二级 EMS 永远读到 0（模式未知）。
func TestIEC61850ReactiveModeRoundTripThroughMMS(t *testing.T) {
	port := freeTCPPort(t)
	sim := simulator.NewSimulator(singleBatteryConfig(), mustNewServer())
	svc, err := StartServer(simulator.IEC61850Config{Enabled: true, Address: fmt.Sprintf("127.0.0.1:%d", port)}, sim)
	if err != nil {
		t.Fatalf("StartServer() error = %v", err)
	}
	defer svc.Close()
	svc.Sync()

	client, err := iec61850.NewClient(iec61850.Settings{
		Host:           "127.0.0.1",
		Port:           port,
		ConnectTimeout: 1000,
		RequestTimeout: 1000,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()

	if err := client.ControlByControlModelAPC("TEMPLATECTRL/setGGIO1.APCS13",
		iec61850.CONTROL_MODEL_DIRECT_NORMAL,
		iec61850.NewControlObjectParamAPC(float32(simulator.ReactiveModeQU))); err != nil {
		t.Fatalf("Control(APCS13) error = %v", err)
	}
	waitRegister(t, sim.BatteryUnits()[0].PCSBank(), simulator.RegPCSReactiveModeCmd, simulator.ReactiveModeQU)

	if err := client.ControlByControlModelAPC("TEMPLATECTRL/setGGIO1.APCS6",
		iec61850.CONTROL_MODEL_DIRECT_NORMAL, iec61850.NewControlObjectParamAPC(float32(0.9))); err != nil {
		t.Fatalf("Control(APCS6) error = %v", err)
	}
	waitRegister(t, sim.BatteryUnits()[0].PCSBank(), simulator.RegPCSPowerFactorCmd, 900)

	svc.Sync()

	mode, err := client.ReadFloat("TEMPLATECTRL/setGGIO1.APCS13.mxVal.f", iec61850.MX)
	if err != nil {
		t.Fatalf("ReadFloat(APCS13.mxVal.f) error = %v", err)
	}
	if mode != float32(simulator.ReactiveModeQU) {
		t.Fatalf("reactive mode readback = %v, want %v", mode, simulator.ReactiveModeQU)
	}

	pf, err := client.ReadFloat("TEMPLATECTRL/setGGIO1.APCS6.mxVal.f", iec61850.MX)
	if err != nil {
		t.Fatalf("ReadFloat(APCS6.mxVal.f) error = %v", err)
	}
	if pf != float32(0.9) {
		t.Fatalf("power factor readback = %v, want 0.9", pf)
	}
}

// waitRegister 轮询等待某寄存器达到期望值，超时则失败；用于覆盖控制下发的异步延迟。
func waitRegister(t *testing.T, bank *simulator.SlaveBank, register, want uint16) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if bank.ReadU16(register) == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("register %d = %d, want %d (timeout)", register, bank.ReadU16(register), want)
}

func TestIEC61850MultipleMMSEndpointsControlDifferentPCSUnits(t *testing.T) {
	port1 := freeTCPPort(t)
	port2 := freeTCPPort(t)
	sim := simulator.NewSimulator(twoBatteryConfig(), mustNewServer())
	svc, err := StartServer(simulator.IEC61850Config{
		Enabled: true,
		Devices: []simulator.IEC61850DeviceConfig{
			{PCSSlaveID: 1, Address: fmt.Sprintf("127.0.0.1:%d", port1)},
			{PCSSlaveID: 2, Address: fmt.Sprintf("127.0.0.1:%d", port2)},
		},
	}, sim)
	if err != nil {
		t.Fatalf("StartServer() error = %v", err)
	}
	defer svc.Close()
	svc.Sync()

	client1 := newIEC61850TestClient(t, port1)
	defer client1.Close()
	client2 := newIEC61850TestClient(t, port2)
	defer client2.Close()

	// 多端点未显式配 ied_name 时按 PCS<slaveID> 自动取唯一名（PCS01 / PCS02）。
	if err := client1.ControlByControlModelAPC("PCS01CTRL/setGGIO1.APCS1",
		iec61850.CONTROL_MODEL_DIRECT_NORMAL, iec61850.NewControlObjectParamAPC(11)); err != nil {
		t.Fatalf("client1 Control(APCS1) error = %v", err)
	}
	if err := client2.ControlByControlModelAPC("PCS02CTRL/setGGIO1.APCS1",
		iec61850.CONTROL_MODEL_DIRECT_NORMAL, iec61850.NewControlObjectParamAPC(22)); err != nil {
		t.Fatalf("client2 Control(APCS1) error = %v", err)
	}

	waitRegister(t, sim.BatteryUnits()[0].PCSBank(), simulator.RegPCSPowerCmd, 110)
	waitRegister(t, sim.BatteryUnits()[1].PCSBank(), simulator.RegPCSPowerCmd, 220)
}

func newIEC61850TestClient(t *testing.T, port int) *iec61850.Client {
	t.Helper()
	client, err := iec61850.NewClient(iec61850.Settings{
		Host:           "127.0.0.1",
		Port:           port,
		ConnectTimeout: 1000,
		RequestTimeout: 1000,
	})
	if err != nil {
		t.Fatalf("NewClient(%d) error = %v", port, err)
	}
	return client
}

func mustNewServer() *mbserver.Server {
	return mbserver.NewServer()
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen free TCP port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func singleBatteryConfig() *simulator.Config {
	cfg := simulator.DefaultConfig()
	cfg.PVUnits = nil
	return &cfg
}

func twoBatteryConfig() *simulator.Config {
	cfg := simulator.DefaultConfig()
	cfg.PVUnits = nil
	cfg.BatteryUnits = []simulator.BatteryUnitConfig{
		{
			PCSSlaveID:         1,
			BMSSlaveID:         11,
			RatedCapacityKWh:   261,
			RatedPowerKW:       120,
			InitialSOC:         30,
			SOH:                100,
			BatteryVoltageFull: 1400,
			ClusterCount:       1,
		},
		{
			PCSSlaveID:         2,
			BMSSlaveID:         12,
			RatedCapacityKWh:   261,
			RatedPowerKW:       120,
			InitialSOC:         30,
			SOH:                100,
			BatteryVoltageFull: 1400,
			ClusterCount:       1,
		},
	}
	return &cfg
}
