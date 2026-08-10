package simulator

import (
	"testing"

	"virtual_bess/internal/mbserver"
)

func TestPowerCommandAlias3010AppliesLike30010(t *testing.T) {
	bu := newReadyBattery(t)

	bu.pcs.WriteU16(RegPCSPowerCmdAlias, 500)
	bu.ProcessPowerCommand()

	// 命令与内部功率同为「负充正放」：+500 ⇒ 放电 50kW。
	assertPowerNear(t, bu.actualPowerKW, 50)
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

	// 命令与内部功率同为「负充正放」：-500 ⇒ 充电 50kW。
	assertPowerNear(t, bu.actualPowerKW, -50)
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

func TestReactivePowerCommandWritesTotalAndPhaseOutputs(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  int16
		want float64
	}{
		{name: "inductive", raw: 240, want: 24},
		{name: "capacitive", raw: -240, want: -24},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bu := newReadyBattery(t)
			bu.pcs.WriteU16(RegPCSReactivePowerCmd, int16ToUint16(tc.raw))

			bu.ProcessPowerCommand()
			assertPowerNear(t, bu.actualReactiveKVAr, tc.want)
			bu.Sync()

			totalRaw := uint16ToInt16(bu.pcs.ReadU16(RegPCSTotalReactPW))
			if totalRaw != int16(bu.actualReactiveKVAr*10) {
				t.Fatalf("total reactive power = %d, want %d", totalRaw, int16(bu.actualReactiveKVAr*10))
			}
			phaseRaw := int16(bu.actualReactiveKVAr / 3 * 10)
			for _, reg := range []uint16{RegPCSReactPWA, RegPCSReactPWB, RegPCSReactPWC} {
				if got := uint16ToInt16(bu.pcs.ReadU16(reg)); got != phaseRaw {
					t.Fatalf("phase reactive power at %d = %d, want %d", reg, got, phaseRaw)
				}
			}
			if got := bu.pcs.ReadU16(RegPCSPowerFactor); got != 0 {
				t.Fatalf("power factor for reactive-only output = %d, want 0", got)
			}
		})
	}
}

func TestReactivePowerCommandDoesNotApplyWhenPCSStopped(t *testing.T) {
	bu := newReadyBattery(t)
	bu.pcsRunning = false
	bu.actualReactiveKVAr = 12.3
	bu.pcs.WriteU16(RegPCSReactivePowerCmd, 240)

	bu.ProcessPowerCommand()
	bu.Sync()

	if bu.actualReactiveKVAr != 0 {
		t.Fatalf("actualReactiveKVAr = %v, want 0", bu.actualReactiveKVAr)
	}
	if got := bu.pcs.ReadU16(RegPCSTotalReactPW); got != 0 {
		t.Fatalf("total reactive power = %d, want 0", got)
	}
}

func TestReactivePowerUpdatesApparentPowerAndPowerFactor(t *testing.T) {
	bu := newReadyBattery(t)
	bu.actualPowerKW = 30
	bu.actualReactiveKVAr = 40

	bu.Sync()

	if got := bu.pcs.ReadU16(RegPCSTotalApparent); got != 500 {
		t.Fatalf("apparent power = %d, want 500", got)
	}
	if got := bu.pcs.ReadU16(RegPCSPowerFactor); got != 60 {
		t.Fatalf("power factor = %d, want 60", got)
	}
	if got := bu.pcs.ReadU16(RegSysRunning); got != 1 {
		t.Fatalf("system running = %d, want 1", got)
	}
}

func TestSimulatorRoutesReactivePowerWriteToPCSOutput(t *testing.T) {
	sim := newTestSimulator(t)
	raw := int16ToUint16(-250)
	if err := sim.WriteHolding(1, RegPCSReactivePowerCmd, raw); err != nil {
		t.Fatalf("write reactive power: %v", err)
	}

	sim.Tick()

	pcs := sim.BatteryUnits()[0].PCSBank()
	if got := pcs.ReadU16(RegPCSReactivePowerCmd); got != raw {
		t.Fatalf("reactive command = %d, want %d", got, raw)
	}
	if got := uint16ToInt16(pcs.ReadU16(RegPCSTotalReactPW)); got >= 0 {
		t.Fatalf("reactive output = %d, want negative capacitive output", got)
	}
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
	bu.actualPowerKW = -60.0 // 负充正放：充电 60 kW

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
	bu.actualPowerKW = -50                    // 充电

	bu.UpdateEnergy(60)
	if bu.actualPowerKW != 0 {
		t.Errorf("at 100%% SOC, charging power should clamp to 0, got %v", bu.actualPowerKW)
	}
}
