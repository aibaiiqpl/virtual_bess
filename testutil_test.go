package main

import (
	"math"
	"testing"
	"time"

	"aiwatt.net/ems/go-common/mbserver"
)

// newTestSimulator 构造一个 1 套电池 + 1 PV + 1 电表 + 1 负载的默认仿真器，
// 不启动 TCP 监听，仅用于单元测试。
func newTestSimulator(t *testing.T) *Simulator {
	t.Helper()
	cfg := DefaultConfig()
	return NewSimulator(&cfg, mustNewServer())
}

// mustNewServer 构造一个未监听任何端口的 mbserver.Server。
func mustNewServer() *mbserver.Server {
	return mbserver.NewServer()
}

func newReadyBattery(t *testing.T) *BatteryUnit {
	t.Helper()
	sim := newTestSimulator(t)
	bu := sim.batteries[0]
	bu.bmsHVClosed = true
	bu.pcsRunning = true
	bu.remoteMode = true
	return bu
}

func newTestPV(t *testing.T) *PVUnit {
	t.Helper()
	sim := newTestSimulator(t)
	return sim.pvs[0]
}

func newTestMeter(t *testing.T) *Meter {
	t.Helper()
	sim := newTestSimulator(t)
	return sim.meter
}

func localTime(year int, month time.Month, day, hour, minute, second int) time.Time {
	return time.Date(year, month, day, hour, minute, second, 0, time.Local)
}

func assertFloatNear(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.001 {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func assertPowerNear(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > math.Abs(want)*0.006 {
		t.Fatalf("actualPowerKW = %v, want near %v", got, want)
	}
}

// 从 mbserver.Registers 读 U32（hi+lo 一对）。
func readU32Bank(reg *mbserver.Registers, hi uint16) uint32 {
	d, _ := reg.GetData(hi, 2)
	return uint32(d[0])<<16 | uint32(d[1])
}

func readS32Bank(reg *mbserver.Registers, hi uint16) int32 {
	return int32(readU32Bank(reg, hi))
}
