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
	if got := sim.BatteryUnits()[0].PCSBank().ReadU16(simulator.RegPCSPowerCmd); got != uint16(123) {
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
