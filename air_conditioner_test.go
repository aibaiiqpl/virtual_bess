package main

import "testing"

func TestAirConditionerACControlsAndHysteresis(t *testing.T) {
	cfg := AirConditionerConfig{IndoorTemperature: 30, AmbientTemperature: 30, CoolingSetpoint: 25, HeatingSetpoint: 15, CoolingDifferential: 2, HeatingDifferential: 2}
	bank := NewSlaveBank(41, false)
	ac := NewAirConditioner(airConditionerAC, cfg, bank)

	ac.OnWrite(0x106, 1)
	ac.Update(0)
	if ac.status != climateCooling {
		t.Fatalf("status = %d, want cooling", ac.status)
	}
	ac.OnWrite(0x100, uint16(int16(28*10)))
	if ac.coolingSetpoint != 28 {
		t.Fatalf("cooling setpoint = %v, want 28", ac.coolingSetpoint)
	}
	ac.indoorTemperature = 26
	ac.Update(0)
	if ac.status != climateStopped {
		t.Fatalf("status = %d, want stopped after cooling differential", ac.status)
	}
	ac.OnWrite(0x106, 0)
	ac.Update(0)
	if ac.status != climateStopped || ac.enabled {
		t.Fatalf("off command did not stop AC: enabled=%v status=%d", ac.enabled, ac.status)
	}

	ac.Sync()
	if got := bank.ReadU16(0x116); got != 0 {
		t.Fatalf("AC status register = %d, want 0", got)
	}
}

func TestAirConditionerLCProtocolAndStatusEncoding(t *testing.T) {
	cfg := AirConditionerConfig{IndoorTemperature: 30, AmbientTemperature: 30, CoolingSetpoint: 25, HeatingSetpoint: 15, CoolingDifferential: 2, HeatingDifferential: 2}
	bank := NewSlaveBank(51, false)
	lc := NewAirConditioner(airConditionerLC, cfg, bank)

	lc.OnWrite(0x1003, 27)
	lc.OnWrite(0x1004, 3)
	lc.OnWrite(0x300, 1)
	lc.Update(0)
	lc.Sync()
	if lc.status != climateCooling {
		t.Fatalf("status = %d, want cooling", lc.status)
	}
	if got := bank.ReadU16(0x0000); got != 0x0100 {
		t.Fatalf("LC status source register = %#x, want 0x0100", got)
	}
	if got := bank.ReadU16(0x1003); got != 27 {
		t.Fatalf("LC cooling setpoint register = %d, want 27", got)
	}
	lc.OnWrite(0x300, 2)
	lc.Update(0)
	lc.Sync()
	if got := bank.ReadU16(0x0000); got != 0 {
		t.Fatalf("LC off status source register = %#x, want 0", got)
	}
}

func TestDefaultConfigContainsTwoACAndTwoLC(t *testing.T) {
	cfg := DefaultConfig()
	if len(cfg.ACUnits) != 2 || len(cfg.LCUnits) != 2 {
		t.Fatalf("default air conditioners = %d AC, %d LC; want 2 each", len(cfg.ACUnits), len(cfg.LCUnits))
	}
	if err := cfg.validate(); err != nil {
		t.Fatalf("default config validation: %v", err)
	}
}

func TestMainConfigCreatesIndependentAirConditionerSlaves(t *testing.T) {
	cfg, err := LoadConfig("config.yaml")
	if err != nil {
		t.Fatalf("LoadConfig(config.yaml): %v", err)
	}
	sim := NewSimulator(cfg, mustNewServer())
	for _, slaveID := range []uint8{41, 42, 51, 52} {
		if sim.BankForSlave(slaveID) == nil {
			t.Fatalf("air conditioner slave %d was not created", slaveID)
		}
	}
	if len(sim.airConditioners) != 4 {
		t.Fatalf("air conditioners = %d, want 4", len(sim.airConditioners))
	}
}

func TestSimulatorRoutesAirConditionerWrites(t *testing.T) {
	sim := newTestSimulator(t)
	if err := sim.writeHolding(41, 0x100, 280); err != nil {
		t.Fatalf("write AC cooling setpoint: %v", err)
	}
	if err := sim.writeHolding(41, 0x106, 1); err != nil {
		t.Fatalf("start AC: %v", err)
	}
	if err := sim.writeHolding(51, 0x1003, 27); err != nil {
		t.Fatalf("write LC cooling setpoint: %v", err)
	}
	if err := sim.writeHolding(51, 0x300, 1); err != nil {
		t.Fatalf("start LC: %v", err)
	}
	sim.Tick()

	if got := sim.BankForSlave(41).ReadU16(0x116); got != 2 {
		t.Fatalf("AC status = %d, want cooling", got)
	}
	if got := sim.BankForSlave(41).ReadU16(0x100); got != 280 {
		t.Fatalf("AC cooling setpoint = %d, want 280", got)
	}
	if got := sim.BankForSlave(51).ReadU16(0x0000); got != 0x0100 {
		t.Fatalf("LC status = %#x, want 0x0100", got)
	}
	if got := sim.BankForSlave(51).ReadU16(0x1003); got != 27 {
		t.Fatalf("LC cooling setpoint = %d, want 27", got)
	}
}
