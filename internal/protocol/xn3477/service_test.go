package xn3477

import (
	"encoding/binary"
	"testing"

	"virtual_bess/internal/mbserver"
	"virtual_bess/internal/simulator"
)

func TestEndpointRoutesSameRegisterByClusterSlaveID(t *testing.T) {
	cfg := simulator.DefaultConfig()
	cfg.BatteryUnits[0].ClusterCount = 2
	sim := simulator.NewSimulator(&cfg, mbserver.NewServer())
	battery := sim.BatteryUnits()[0]
	battery.BMSBank().WriteInputU16(simulator.OffClusterSOC, 111)
	battery.BMSBank().WriteInputU16(simulator.IRClusterStride+simulator.OffClusterSOC, 222)

	ep := newEndpoint(simulator.XN3477DeviceConfig{
		BMSSlaveID:      battery.BMSSlaveID(),
		Address:         ":8502",
		BAUSlaveID:      1,
		ClusterSlaveIDs: []uint8{2, 3},
	}, sim, battery)
	ep.sync()

	assertReadValue(t, ep, 2, 3, xnBCUSOC, 111)
	assertReadValue(t, ep, 3, 4, xnBCUSOC, 222)

	_, exception := ep.handleRead(nil, testFrame(t, 4, 3, xnBCUSOC, 1))
	if exception != &mbserver.GatewayTargetDeviceFailedtoRespond {
		t.Fatalf("unknown slave exception = %v, want gateway target failed", exception)
	}
}

func TestEndpointForwardsBAUHVControl(t *testing.T) {
	cfg := simulator.DefaultConfig()
	sim := simulator.NewSimulator(&cfg, mbserver.NewServer())
	battery := sim.BatteryUnits()[0]
	ep := newEndpoint(simulator.XN3477DeviceConfig{
		BMSSlaveID:      battery.BMSSlaveID(),
		Address:         ":8502",
		BAUSlaveID:      1,
		ClusterSlaveIDs: []uint8{2},
	}, sim, battery)

	_, exception := ep.handleWriteSingle(nil, testFrame(t, 1, 6, xnBAUHVControl, 2))
	if exception != &mbserver.Success {
		t.Fatalf("write exception = %v, want success", exception)
	}
	if got := battery.BMSBank().ReadU16(simulator.RegBMSOpenHV); got != 1 {
		t.Fatalf("BMS open-HV command = %d, want 1", got)
	}
}

func assertReadValue(
	t *testing.T,
	ep *endpoint,
	slaveID, function uint8,
	address, want uint16,
) {
	t.Helper()
	data, exception := ep.handleRead(nil, testFrame(t, slaveID, function, address, 1))
	if exception != &mbserver.Success {
		t.Fatalf("read slave %d exception = %v", slaveID, exception)
	}
	if len(data) != 3 {
		t.Fatalf("read slave %d data length = %d, want 3", slaveID, len(data))
	}
	if got := binary.BigEndian.Uint16(data[1:]); got != want {
		t.Fatalf("read slave %d value = %d, want %d", slaveID, got, want)
	}
}

func testFrame(t *testing.T, slaveID, function uint8, address, value uint16) mbserver.Framer {
	t.Helper()
	packet := make([]byte, 12)
	binary.BigEndian.PutUint16(packet[4:6], 6)
	packet[6] = slaveID
	packet[7] = function
	binary.BigEndian.PutUint16(packet[8:10], address)
	binary.BigEndian.PutUint16(packet[10:12], value)
	frame, err := mbserver.NewTCPFrame(packet)
	if err != nil {
		t.Fatalf("NewTCPFrame() error = %v", err)
	}
	return frame
}
