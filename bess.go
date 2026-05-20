package main

import (
	"sync"
	"time"

	"aiwatt.net/ems/go-common/mbserver"
)

// BESS models a virtual battery energy storage system.
// It maintains internal state (SOC, power, contactor status, etc.)
// and synchronizes with a Modbus server's holding registers each tick.
type BESS struct {
	mu sync.Mutex

	// Configuration (immutable after init)
	ratedCapacityKWh  float64
	ratedPowerKW      float64
	soh               float64
	batteryVoltageNom float64 // nominal DC voltage
	gridVoltage       float64
	pvRatedPowerKW    float64

	// Dynamic state
	currentEnergyKWh     float64 // current stored energy in kWh
	pcsRunning           bool    // PCS is started
	bmsHVClosed          bool    // BMS high-voltage contactor closed
	remoteMode           bool    // true=remote, false=local
	gridTied             bool    // true=grid-tied, false=off-grid
	actualPowerKW        float64 // current power: positive=charge, negative=discharge
	clusterCount         int     // number of BMS clusters
	lastPowerCmdRaw      uint16  // last synchronized value of RegPCSPowerCmd
	lastPowerCmdAliasRaw uint16  // last synchronized value of RegPCSPowerCmdAlias

	// Cumulative energy tracking (kWh)
	totalChargeKWh      float64 // lifetime cumulative charge energy
	totalDischargeKWh   float64 // lifetime cumulative discharge energy
	sessionChargeKWh    float64 // current session cumulative charge energy
	sessionDischargeKWh float64 // current session cumulative discharge energy

	// PV state
	pvRunning             bool
	pvActualPowerKW       float64
	pvTotalEnergyKWh      float64
	pvDailyEnergyKWh      float64
	pvMonthlyEnergyKWh    float64
	pvYearlyEnergyKWh     float64
	pvDailyPeakPowerKW    float64
	pvLimitMode           pvLimitMode
	lastPVPercentLimitRaw uint16
	lastPVFixedLimitRaw   uint16
	pvDayKey              int
	pvMonthKey            int
	pvYearKey             int

	// Weather state (drives PV output coefficient)
	weatherState  weatherState
	weatherRemain float64
	weatherCoeff  float64

	// Load simulation (internal only, no Modbus)
	loadRatedPowerKW  float64
	loadActualPowerKW float64

	// Meter state (point of common coupling)
	meterForwardKWh float64
	meterReverseKWh float64
	meterGridPowerKW float64

	server   *mbserver.Server
	lastTick time.Time
	nowFunc  func() time.Time
}

// NewBESS creates a BESS instance, initializes default register values,
// and syncs the initial state to modbus registers.
func NewBESS(cfg *Config, server *mbserver.Server) *BESS {
	now := time.Now()
	initialEnergy := cfg.BESS.RatedCapacityKWh * cfg.BESS.InitialSOC / 100.0
	b := &BESS{
		ratedCapacityKWh:  cfg.BESS.RatedCapacityKWh,
		ratedPowerKW:      cfg.BESS.RatedPowerKW,
		soh:               cfg.BESS.SOH,
		batteryVoltageNom: cfg.BESS.BatteryVoltage,
		gridVoltage:       cfg.BESS.GridVoltage,
		pvRatedPowerKW:   cfg.PV.RatedPowerKW,
		loadRatedPowerKW: cfg.Load.RatedPowerKW,
		currentEnergyKWh: initialEnergy,
		clusterCount:     cfg.BESS.ClusterCount,
		remoteMode:       true,
		gridTied:         true,
		pvRunning:        true,
		pvLimitMode:      pvLimitPercent,
		server:           server,
		lastTick:         now,
		nowFunc:          time.Now,
	}

	// Set default control register values
	server.HoldingRegisters[RegPCSRemoteLocal] = 1 // remote
	server.HoldingRegisters[RegPCSGridMode] = 0    // grid-tied
	server.HoldingRegisters[RegPCSRunMode] = 2     // constant power
	server.HoldingRegisters[RegPVPercentLimit] = 1000
	b.lastPVPercentLimitRaw = 1000

	b.registerPVWriteHandlers()
	b.initWeather()
	b.updateLoad(now)
	b.updatePVSimulation(now, 0)
	b.updateMeter(0)
	b.syncRegisters()
	return b
}

// Tick advances the simulation by one step: reads controls, updates physics, writes registers.
func (b *BESS) Tick() {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.nowFunc()
	dt := now.Sub(b.lastTick).Seconds()
	b.lastTick = now

	b.processBMSControls()
	b.processPCSControls()
	b.processPowerCommand()
	b.processPVControls()
	b.updateSimulation(dt)
	b.updateWeather(dt)
	b.updateLoad(now)
	b.updatePVSimulation(now, dt)
	b.updateMeter(dt)
	b.syncRegisters()
}

// soc returns the current state of charge as a percentage (0-100).
func (b *BESS) soc() float64 {
	if b.ratedCapacityKWh == 0 {
		return 0
	}
	return b.currentEnergyKWh / b.ratedCapacityKWh * 100.0
}

// batteryVoltage returns DC voltage interpolated by SOC.
// SOC 0% → 90% of nominal, SOC 100% → 110% of nominal.
func (b *BESS) batteryVoltage() float64 {
	return b.batteryVoltageNom * (0.9 + 0.2*b.soc()/100.0)
}

func boolToU16(v bool) uint16 {
	if v {
		return 1
	}
	return 0
}
