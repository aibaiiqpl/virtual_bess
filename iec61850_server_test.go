//go:build iec61850

package main

import (
	"fmt"
	"net"
	"testing"

	"github.com/go-bindings/iec61850"
)

func TestIEC61850ModelContainsCoreCIDReferences(t *testing.T) {
	model, err := loadIEC61850Model()
	if err != nil {
		t.Fatalf("loadIEC61850Model() error = %v", err)
	}
	defer model.Destroy()

	for _, ref := range []string{
		"TEMPLATECTRL/GGIO1.APCS1.setMag.f",
		"TEMPLATECTRL/GGIO1.APCS2.setMag.f",
		"TEMPLATECTRL/GGIO1.APCS9.setMag.f",
		"TEMPLATECTRL/GGIO1.APCS10.setMag.f",
		"TEMPLATEPIGO/GGIO1.AnIn1.mag.f",
		"TEMPLATEPIGO/GGIO1.AnIn2.mag.f",
		"TEMPLATEPIGO/GGIO1.AnIn3.mag.i",
		"TEMPLATEPIGO/GGIO1.AnIn9.mag.f",
	} {
		if model.GetModelNodeByObjectReference(ref) == nil {
			t.Fatalf("model node %s not found", ref)
		}
	}
}

func TestIEC61850ActivePowerWriteWritesPCSCommand(t *testing.T) {
	sim := NewSimulator(singleBatteryConfig(), mustNewServer())
	svc := &iec61850Server{sim: sim}

	result := svc.handleActivePowerWrite(nil, &iec61850.MmsValue{Type: iec61850.Float, Value: float32(12.3)})
	if result != iec61850.DATA_ACCESS_ERROR_SUCCESS {
		t.Fatalf("handleActivePowerWrite() = %v, want SUCCESS", result)
	}
	if got := sim.batteries[0].pcs.ReadU16(RegPCSPowerCmd); got != uint16(123) {
		t.Fatalf("PCS power command = %d, want 123", got)
	}
}

func TestIEC61850ActivePowerWriteRejectsOutOfRangeCommand(t *testing.T) {
	sim := NewSimulator(singleBatteryConfig(), mustNewServer())
	svc := &iec61850Server{sim: sim}

	result := svc.handleActivePowerWrite(nil, &iec61850.MmsValue{Type: iec61850.Float, Value: float32(4000)})
	if result != iec61850.DATA_ACCESS_ERROR_OBJECT_VALUE_INVALID {
		t.Fatalf("handleActivePowerWrite() = %v, want VALUE_INVALID", result)
	}
	if got := sim.batteries[0].pcs.ReadU16(RegPCSPowerCmd); got != 0 {
		t.Fatalf("PCS power command = %d, want unchanged zero", got)
	}
}

func TestIEC61850PCSCommandRejectsUnknownCommand(t *testing.T) {
	sim := NewSimulator(singleBatteryConfig(), mustNewServer())
	svc := &iec61850Server{sim: sim}

	result := svc.handlePCSCommandWrite(nil, &iec61850.MmsValue{Type: iec61850.Float, Value: float32(99)})
	if result != iec61850.DATA_ACCESS_ERROR_OBJECT_VALUE_INVALID {
		t.Fatalf("handlePCSCommandWrite() = %v, want VALUE_INVALID", result)
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
	sim := NewSimulator(singleBatteryConfig(), mustNewServer())
	svc, err := startIEC61850Server(IEC61850Config{Enabled: true, Address: fmt.Sprintf("127.0.0.1:%d", port)}, sim)
	if err != nil {
		t.Fatalf("startIEC61850Server() error = %v", err)
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

	ratedPower, err := client.ReadFloat("TEMPLATEPIGO/GGIO1.AnIn1.mag.f", iec61850.MX)
	if err != nil {
		t.Fatalf("ReadFloat(ratedPower) error = %v", err)
	}
	if ratedPower != float32(sim.batteries[0].ratedPowerKW) {
		t.Fatalf("ratedPower = %v, want %v", ratedPower, sim.batteries[0].ratedPowerKW)
	}

	if err := client.Write("TEMPLATECTRL/GGIO1.APCS1.setMag.f", iec61850.SP, float32(12)); err != nil {
		t.Fatalf("Write(APCS1.setMag.f) error = %v", err)
	}
	if got := sim.batteries[0].pcs.ReadU16(RegPCSPowerCmd); got != 120 {
		t.Fatalf("PCS power command = %d, want 120", got)
	}
}

func TestIEC61850MultipleMMSEndpointsControlDifferentPCSUnits(t *testing.T) {
	port1 := freeTCPPort(t)
	port2 := freeTCPPort(t)
	sim := NewSimulator(twoBatteryConfig(), mustNewServer())
	svc, err := startIEC61850Server(IEC61850Config{
		Enabled: true,
		Devices: []IEC61850DeviceConfig{
			{PCSSlaveID: 1, Address: fmt.Sprintf("127.0.0.1:%d", port1)},
			{PCSSlaveID: 2, Address: fmt.Sprintf("127.0.0.1:%d", port2)},
		},
	}, sim)
	if err != nil {
		t.Fatalf("startIEC61850Server() error = %v", err)
	}
	defer svc.Close()
	svc.Sync()

	client1 := newIEC61850TestClient(t, port1)
	defer client1.Close()
	client2 := newIEC61850TestClient(t, port2)
	defer client2.Close()

	if err := client1.Write("TEMPLATECTRL/GGIO1.APCS1.setMag.f", iec61850.SP, float32(11)); err != nil {
		t.Fatalf("client1 Write(APCS1.setMag.f) error = %v", err)
	}
	if err := client2.Write("TEMPLATECTRL/GGIO1.APCS1.setMag.f", iec61850.SP, float32(22)); err != nil {
		t.Fatalf("client2 Write(APCS1.setMag.f) error = %v", err)
	}

	if got := sim.batteries[0].pcs.ReadU16(RegPCSPowerCmd); got != 110 {
		t.Fatalf("PCS1 power command = %d, want 110", got)
	}
	if got := sim.batteries[1].pcs.ReadU16(RegPCSPowerCmd); got != 220 {
		t.Fatalf("PCS2 power command = %d, want 220", got)
	}
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

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen free TCP port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func singleBatteryConfig() *Config {
	cfg := DefaultConfig()
	cfg.PVUnits = nil
	return &cfg
}

func twoBatteryConfig() *Config {
	cfg := DefaultConfig()
	cfg.PVUnits = nil
	cfg.BatteryUnits = []BatteryUnitConfig{
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
