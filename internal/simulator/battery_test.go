package simulator

import (
	"testing"

	"virtual_bess/internal/mbserver"
)

func TestPowerCommandAlias3010AppliesLike30010(t *testing.T) {
	bu := newReadyBattery(t)

	bu.pcs.WriteU16(RegPCSPowerCmdAlias, 500)
	bu.ProcessPowerCommand()

	// 命令寄存器为真机约定（正=放电），内部 actualPowerKW 取反为 -50（放电）。
	assertPowerNear(t, bu.actualPowerKW, -50)
	assertPowerCommandRegisters(t, bu, 500)
}

func TestPowerCommandAlias3010CanClearCanonicalSetpoint(t *testing.T) {
	bu := newReadyBattery(t)

	bu.pcs.WriteU16(RegPCSPowerCmd, 500)
	bu.ProcessPowerCommand()
	bu.pcs.WriteU16(RegPCSPowerCmdAlias, 0)
	bu.ProcessPowerCommand()

	if bu.actualPowerKW != 0 {
		t.Fatalf("actualPowerKW = %v, want 0", bu.actualPowerKW)
	}
	assertPowerCommandRegisters(t, bu, 0)
}

func TestPowerCommand30010StillAppliesAndMirrorsAlias(t *testing.T) {
	bu := newReadyBattery(t)

	raw := int16ToUint16(-500)
	bu.pcs.WriteU16(RegPCSPowerCmd, raw)
	bu.ProcessPowerCommand()

	// 命令寄存器为真机约定（负=充电），内部 actualPowerKW 取反为 +50（充电）。
	assertPowerNear(t, bu.actualPowerKW, 50)
	assertPowerCommandRegisters(t, bu, raw)
}

func TestPowerCommandAlias3010DoesNotApplyInLocalMode(t *testing.T) {
	bu := newReadyBattery(t)
	bu.remoteMode = false
	bu.actualPowerKW = 12.3

	bu.pcs.WriteU16(RegPCSPowerCmdAlias, 500)
	bu.ProcessPowerCommand()

	if bu.actualPowerKW != 12.3 {
		t.Fatalf("actualPowerKW = %v, want unchanged 12.3", bu.actualPowerKW)
	}
	assertPowerCommandRegisters(t, bu, 500)
}

func assertPowerCommandRegisters(t *testing.T, bu *BatteryUnit, want uint16) {
	t.Helper()
	if got := bu.pcs.ReadU16(RegPCSPowerCmd); got != want {
		t.Fatalf("RegPCSPowerCmd = %v, want %v", got, want)
	}
	if got := bu.pcs.ReadU16(RegPCSPowerCmdAlias); got != want {
		t.Fatalf("RegPCSPowerCmdAlias = %v, want %v", got, want)
	}
}

// TestMultipleBatteryUnitsRoutedBySlaveID 验证两套电池单元各自独立响应自己的 slaveId。
func TestMultipleBatteryUnitsRoutedBySlaveID(t *testing.T) {
	cfg := Config{
		Modbus: ModbusConfig{Address: ""},
		Grid:   GridConfig{Voltage: 220},
		BatteryUnits: []BatteryUnitConfig{
			{PCSSlaveID: 1, BMSSlaveID: 11, RatedCapacityKWh: 100, RatedPowerKW: 50, InitialSOC: 50, SOH: 100, BatteryVoltageFull: 1400, ClusterCount: 1},
			{PCSSlaveID: 2, BMSSlaveID: 12, RatedCapacityKWh: 100, RatedPowerKW: 50, InitialSOC: 50, SOH: 100, BatteryVoltageFull: 1400, ClusterCount: 1},
		},
		PVUnits: []PVUnitConfig{{SlaveID: 21, RatedPowerKW: 30}},
		Meters:  []MeterConfig{{SlaveID: 31, Name: "main", IsMain: true}},
		Loads:   []LoadCfg{{Name: "load", RatedPowerKW: 80}},
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}
	sim := NewSimulator(&cfg, mustNewServer())

	// 通过 modbus 写第一台 PCS 的功率指令
	frame1 := &mbserver.TCPFrame{Function: 6, Device: 1}
	mbserver.SetDataWithRegisterAndNumber(frame1, RegPCSPowerCmd, 300)
	if _, exc := sim.handleWriteSingleHolding(sim.server, frame1); exc != &mbserver.Success {
		t.Fatalf("write to slave 1 failed: %v", exc)
	}

	if got := sim.batteries[0].pcs.ReadU16(RegPCSPowerCmd); got != 300 {
		t.Errorf("battery 0 PCS cmd = %v, want 300", got)
	}
	if got := sim.batteries[1].pcs.ReadU16(RegPCSPowerCmd); got != 0 {
		t.Errorf("battery 1 PCS cmd = %v, want 0 (untouched)", got)
	}

	// 写到不存在的 slave 应返回错误
	frame3 := &mbserver.TCPFrame{Function: 6, Device: 99}
	mbserver.SetDataWithRegisterAndNumber(frame3, RegPCSPowerCmd, 100)
	if _, exc := sim.handleWriteSingleHolding(sim.server, frame3); exc == &mbserver.Success {
		t.Error("write to nonexistent slave should fail")
	}
}

func TestBatteryUnitEnergyAccumulation(t *testing.T) {
	bu := newReadyBattery(t)
	bu.currentEnergyKWh = 50.0
	bu.actualPowerKW = 60.0 // 充电 60 kW

	// 1 小时
	bu.UpdateEnergy(3600)

	wantEnergy := 50.0 + 60.0 // 110 kWh
	assertFloatNear(t, bu.currentEnergyKWh, wantEnergy)
	assertFloatNear(t, bu.totalChargeKWh, 60.0)
	if bu.totalDischargeKWh != 0 {
		t.Errorf("discharge should be 0, got %v", bu.totalDischargeKWh)
	}
}

func TestBatteryUnitSOCBoundaryStopsCharging(t *testing.T) {
	bu := newReadyBattery(t)
	bu.currentEnergyKWh = bu.ratedCapacityKWh // 100% SOC
	bu.actualPowerKW = 50

	bu.UpdateEnergy(60)
	if bu.actualPowerKW != 0 {
		t.Errorf("at 100%% SOC, charging power should clamp to 0, got %v", bu.actualPowerKW)
	}
}
