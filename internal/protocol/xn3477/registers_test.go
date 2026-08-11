package xn3477

import (
	"testing"

	"virtual_bess/internal/mbserver"
	"virtual_bess/internal/simulator"
)

// 高压闭合后静置，BAU 必须报 0x1031=0（正常/在线）。
// 现场点表把 0x1031 的 3、4 都读作「下高压/停机」，早期实现把内部待机映射成 3，
// 导致 EMS 侧 40102 恒为停机，上高压看起来永远不生效。
func TestBAUStatusAfterHVCloseIsOnline(t *testing.T) {
	sim, ep := newTestEndpoint(t)

	writeBAU(t, ep, xnBAUHVControl, 1)
	sim.Tick()
	ep.sync()

	assertReadValue(t, ep, 1, 3, xnBAUMainStatus, xnRunNormal)
	assertReadValue(t, ep, 1, 3, xnBAUWorkStatus, xnChgDsgRest)
	assertReadValue(t, ep, 2, 3, xnBCUMainStatus, xnRunNormal)
	assertReadValue(t, ep, 2, 3, xnBCUWorkStatus, xnChgDsgRest)
}

// 下高压后 BAU 报 0x1031=3（协议待机，项目语义为下高压）。
func TestBAUStatusAfterHVOpenIsStandby(t *testing.T) {
	sim, ep := newTestEndpoint(t)

	writeBAU(t, ep, xnBAUHVControl, 1)
	sim.Tick()
	writeBAU(t, ep, xnBAUHVControl, 2)
	sim.Tick()
	ep.sync()

	assertReadValue(t, ep, 1, 3, xnBAUMainStatus, xnRunStandby)
	assertReadValue(t, ep, 1, 3, xnBAUWorkStatus, xnChgDsgRest)
	assertReadValue(t, ep, 2, 3, xnBCUMainStatus, xnRunStandby)
}

func TestXNStatusMapping(t *testing.T) {
	cases := []struct {
		name                            string
		status, chargeFbd, dischargeFbd uint16
		wantMain, wantWork              uint16
	}{
		{"starting", simulator.BMSStatusStarting, 0, 0, xnRunStandby, xnChgDsgRest},
		{"standby", simulator.BMSStatusStandby, 0, 0, xnRunNormal, xnChgDsgRest},
		{"stopped", simulator.BMSStatusStopped, 0, 0, xnRunStandby, xnChgDsgRest},
		{"charging", simulator.BMSStatusCharging, 0, 0, xnRunNormal, xnChgDsgCharge},
		{"discharging", simulator.BMSStatusDischarging, 0, 0, xnRunNormal, xnChgDsgDischarge},
		{"full", simulator.BMSStatusStandby, 1, 0, xnRunChargeForbidden, xnChgDsgRest},
		{"empty", simulator.BMSStatusStandby, 0, 1, xnRunDischargeForbidden, xnChgDsgRest},
		// 满电时仍在放电：运行状态报禁充，充放电状态照常给放电
		{"full discharging", simulator.BMSStatusDischarging, 1, 0, xnRunChargeForbidden, xnChgDsgDischarge},
		// 下高压优先于禁充禁放，避免 EMS 把停机误读成在线
		{"stopped full", simulator.BMSStatusStopped, 1, 0, xnRunStandby, xnChgDsgRest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			main, work := xnStatus(c.status, c.chargeFbd, c.dischargeFbd)
			if main != c.wantMain || work != c.wantWork {
				t.Fatalf("xnStatus(%d,%d,%d) = (%d,%d), want (%d,%d)",
					c.status, c.chargeFbd, c.dischargeFbd, main, work, c.wantMain, c.wantWork)
			}
		})
	}
}

func TestXNClusterStatusMapping(t *testing.T) {
	cases := []struct {
		name               string
		status             uint16
		wantMain, wantWork uint16
	}{
		{"offline", simulator.ClusterStatusOffline, xnRunStopped, xnChgDsgRest},
		{"standby", simulator.ClusterStatusStandby, xnRunNormal, xnChgDsgRest},
		{"stopped", simulator.ClusterStatusStopped, xnRunStandby, xnChgDsgRest},
		{"charging", simulator.ClusterStatusCharging, xnRunNormal, xnChgDsgCharge},
		{"discharging", simulator.ClusterStatusDischarging, xnRunNormal, xnChgDsgDischarge},
		{"running", simulator.ClusterStatusRunning, xnRunNormal, xnChgDsgRest},
		{"fault", simulator.ClusterStatusFault, xnRunStopped, xnChgDsgRest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			main, work := xnClusterStatus(c.status, 0, 0)
			if main != c.wantMain || work != c.wantWork {
				t.Fatalf("xnClusterStatus(%d) = (%d,%d), want (%d,%d)",
					c.status, main, work, c.wantMain, c.wantWork)
			}
		})
	}
}

func newTestEndpoint(t *testing.T) (*simulator.Simulator, *endpoint) {
	t.Helper()
	cfg := simulator.DefaultConfig()
	sim := simulator.NewSimulator(&cfg, mbserver.NewServer())
	battery := sim.BatteryUnits()[0]
	ep := newEndpoint(simulator.XN3477DeviceConfig{
		BMSSlaveID:      battery.BMSSlaveID(),
		Address:         ":8502",
		BAUSlaveID:      1,
		ClusterSlaveIDs: []uint8{2},
	}, sim, battery)
	return sim, ep
}

func writeBAU(t *testing.T, ep *endpoint, address, value uint16) {
	t.Helper()
	if _, exception := ep.handleWriteSingle(
		nil,
		testFrame(t, 1, 6, address, value),
	); exception != &mbserver.Success {
		t.Fatalf("write 0x%04X=%d exception = %v", address, value, exception)
	}
}
