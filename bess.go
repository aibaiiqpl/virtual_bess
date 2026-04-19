package main

import (
	"math"
	"math/rand"
	"sync"
	"time"

	"aiwatt.net/ems/go-common/mbserver"
	"aiwatt.net/ems/go-common/zaplog"
)

type BESS struct {
	mu sync.Mutex

	// Configuration (immutable after init)
	ratedCapacityKWh float64
	ratedPowerKW     float64
	soh              float64
	batteryVoltage   float64
	gridVoltage      float64

	// Dynamic state
	currentEnergyKWh float64 // current stored energy
	pcsRunning       bool    // PCS started
	bmsHVClosed      bool    // BMS high-voltage contactor closed
	remoteMode       bool    // true=remote, false=local
	gridTied         bool    // true=grid-tied, false=off-grid
	actualPowerKW    float64 // current actual power (positive=charge, negative=discharge)

	server   *mbserver.Server
	lastTick time.Time
}

func NewBESS(cfg *Config, server *mbserver.Server) *BESS {
	initialEnergy := cfg.BESS.RatedCapacityKWh * cfg.BESS.InitialSOC / 100.0
	b := &BESS{
		ratedCapacityKWh: cfg.BESS.RatedCapacityKWh,
		ratedPowerKW:     cfg.BESS.RatedPowerKW,
		soh:              cfg.BESS.SOH,
		batteryVoltage:   cfg.BESS.BatteryVoltage,
		gridVoltage:      cfg.BESS.GridVoltage,
		currentEnergyKWh: initialEnergy,
		remoteMode:       true,
		gridTied:         true,
		server:           server,
		lastTick:         time.Now(),
	}

	// Set default control register values
	server.HoldingRegisters[RegPCSRemoteLocal] = 1 // remote
	server.HoldingRegisters[RegPCSGridMode] = 0    // grid-tied
	server.HoldingRegisters[RegPCSRunMode] = 2     // constant power

	// Sync initial state
	b.syncRegisters()
	return b
}

func (b *BESS) Tick() {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	dt := now.Sub(b.lastTick).Seconds()
	b.lastTick = now

	b.processControls()
	b.updateSimulation(dt)
	b.syncRegisters()
}

// processControls reads control registers and updates internal state.
func (b *BESS) processControls() {
	s := b.server

	// BMS contactor control
	if s.HoldingRegisters[RegBMSCloseHV] == 1 {
		if !b.bmsHVClosed {
			zaplog.Info("BMS high-voltage contactor closed")
		}
		b.bmsHVClosed = true
		s.HoldingRegisters[RegBMSCloseHV] = 0 // auto-clear
	}
	if s.HoldingRegisters[RegBMSOpenHV] == 1 {
		if b.bmsHVClosed {
			zaplog.Info("BMS high-voltage contactor opened")
		}
		b.bmsHVClosed = false
		b.actualPowerKW = 0
		s.HoldingRegisters[RegBMSOpenHV] = 0
	}

	// BMS fault reset
	if s.HoldingRegisters[RegBMSFaultReset] == 1 {
		zaplog.Info("BMS fault reset")
		s.HoldingRegisters[RegBMSFaultReset] = 0
	}

	// PCS remote/local
	b.remoteMode = s.HoldingRegisters[RegPCSRemoteLocal] == 1

	// PCS grid mode
	b.gridTied = s.HoldingRegisters[RegPCSGridMode] == 0

	// PCS startup
	if s.HoldingRegisters[RegPCSStartup] == 1 {
		if !b.bmsHVClosed {
			zaplog.Warn("PCS startup failed: BMS high-voltage not closed, DC undervoltage fault")
		} else {
			if !b.pcsRunning {
				zaplog.Info("PCS started")
			}
			b.pcsRunning = true
		}
		s.HoldingRegisters[RegPCSStartup] = 0
	}

	// PCS shutdown
	if s.HoldingRegisters[RegPCSShutdown] == 1 {
		if b.pcsRunning {
			zaplog.Info("PCS shutdown")
		}
		b.pcsRunning = false
		b.actualPowerKW = 0
		s.HoldingRegisters[RegPCSShutdown] = 0
	}

	// PCS emergency stop
	if s.HoldingRegisters[RegPCSEStop] == 1 {
		zaplog.Warn("PCS emergency stop")
		b.pcsRunning = false
		b.actualPowerKW = 0
		s.HoldingRegisters[RegPCSEStop] = 0
	}

	// PCS fault reset
	if s.HoldingRegisters[RegPCSFaultReset] == 1 {
		zaplog.Info("PCS fault reset")
		s.HoldingRegisters[RegPCSFaultReset] = 0
	}

	// Power command - only effective when PCS running + remote mode + BMS HV closed
	if b.pcsRunning && b.remoteMode && b.bmsHVClosed {
		cmdRaw := uint16ToInt16(s.HoldingRegisters[RegPCSPowerCmd])
		cmdPowerKW := float64(cmdRaw) * 0.1

		// Clamp to rated power
		if cmdPowerKW > b.ratedPowerKW {
			cmdPowerKW = b.ratedPowerKW
		}
		if cmdPowerKW < -b.ratedPowerKW {
			cmdPowerKW = -b.ratedPowerKW
		}
		b.actualPowerKW = cmdPowerKW
	} else if !b.remoteMode {
		// Local mode: ignore power command, keep current power
	} else {
		b.actualPowerKW = 0
	}
}

// updateSimulation updates energy/SOC based on actual power and time delta.
func (b *BESS) updateSimulation(dtSeconds float64) {
	if b.actualPowerKW == 0 {
		return
	}

	soc := b.soc()

	// Check SOC boundaries
	if b.actualPowerKW > 0 && soc >= 100.0 {
		b.actualPowerKW = 0
		return
	}
	if b.actualPowerKW < 0 && soc <= 0.0 {
		b.actualPowerKW = 0
		return
	}

	deltaEnergy := b.actualPowerKW * dtSeconds / 3600.0
	b.currentEnergyKWh += deltaEnergy

	// Clamp energy
	if b.currentEnergyKWh < 0 {
		b.currentEnergyKWh = 0
		b.actualPowerKW = 0
	}
	if b.currentEnergyKWh > b.ratedCapacityKWh {
		b.currentEnergyKWh = b.ratedCapacityKWh
		b.actualPowerKW = 0
	}
}

func (b *BESS) soc() float64 {
	if b.ratedCapacityKWh == 0 {
		return 0
	}
	return b.currentEnergyKWh / b.ratedCapacityKWh * 100.0
}

// syncRegisters writes all computed values to the modbus holding registers.
func (b *BESS) syncRegisters() {
	s := b.server
	soc := b.soc()
	powerKW := b.actualPowerKW
	ratedCurrentA := b.ratedPowerKW * 1000.0 / b.batteryVoltage // rated current in A

	// --- PCS status ---
	s.HoldingRegisters[RegPCSRemoteStatus] = boolToU16(b.remoteMode)
	s.HoldingRegisters[RegPCSGridStatus] = boolToU16(!b.gridTied)

	// PCS system status
	if !b.pcsRunning {
		s.HoldingRegisters[RegPCSSysStatus] = 1 // stopped
	} else if powerKW > 0 {
		s.HoldingRegisters[RegPCSSysStatus] = 3 // charging
	} else if powerKW < 0 {
		s.HoldingRegisters[RegPCSSysStatus] = 4 // discharging
	} else {
		s.HoldingRegisters[RegPCSSysStatus] = 2 // standby
	}

	// PCS alarm/fault
	s.HoldingRegisters[RegPCSAlarmStatus] = 0
	s.HoldingRegisters[RegPCSFaultStatus] = 0

	// DC undervoltage fault: PCS attempted start without BMS HV
	if b.pcsRunning && !b.bmsHVClosed {
		s.HoldingRegisters[RegPCSDCUnderVolt] = 1
		s.HoldingRegisters[RegPCSFaultStatus] = 1
	} else {
		s.HoldingRegisters[RegPCSDCUnderVolt] = 0
	}

	// PCS power values
	s.HoldingRegisters[RegPCSTotalActivePW] = int16ToUint16(int16(powerKW * 10))
	s.HoldingRegisters[RegPCSTotalReactPW] = 0
	s.HoldingRegisters[RegPCSTotalApparent] = uint16(math.Abs(powerKW) * 10)
	s.HoldingRegisters[RegPCSPowerFactor] = int16ToUint16(100) // 1.00

	// Three-phase power (equal split)
	phasePower := powerKW / 3.0
	s.HoldingRegisters[RegPCSActivePWA] = int16ToUint16(int16(phasePower * 10))
	s.HoldingRegisters[RegPCSActivePWB] = int16ToUint16(int16(phasePower * 10))
	s.HoldingRegisters[RegPCSActivePWC] = int16ToUint16(int16(phasePower * 10))
	s.HoldingRegisters[RegPCSReactPWA] = 0
	s.HoldingRegisters[RegPCSReactPWB] = 0
	s.HoldingRegisters[RegPCSReactPWC] = 0

	// Three-phase voltage with ±5% fluctuation
	for _, reg := range []uint16{RegPCSVoltageA, RegPCSVoltageB, RegPCSVoltageC} {
		jitter := 1.0 + (rand.Float64()*0.1 - 0.05)
		s.HoldingRegisters[reg] = uint16(b.gridVoltage * jitter * 10)
	}

	// Three-phase current = phase power / phase voltage
	for i, reg := range []uint16{RegPCSCurrentA, RegPCSCurrentB, RegPCSCurrentC} {
		phaseVoltage := float64(s.HoldingRegisters[RegPCSVoltageA+uint16(i)]) * 0.1
		if phaseVoltage > 0 {
			currentA := phasePower / phaseVoltage * 1000.0 // kW to W, then / V = A
			s.HoldingRegisters[reg] = int16ToUint16(int16(currentA * 10))
		}
	}

	// Grid frequency: 50.00 Hz
	s.HoldingRegisters[RegPCSFrequency] = 5000

	// DC side
	s.HoldingRegisters[RegPCSDCVoltage] = int16ToUint16(int16(b.batteryVoltage * 10))
	dcCurrentA := 0.0
	if b.batteryVoltage > 0 {
		dcCurrentA = powerKW * 1000.0 / b.batteryVoltage
	}
	s.HoldingRegisters[RegPCSDCCurrent] = int16ToUint16(int16(dcCurrentA * 10))
	s.HoldingRegisters[RegPCSDCPower] = int16ToUint16(int16(powerKW * 10))

	// Temperatures: fixed 30.0°C
	s.HoldingRegisters[RegPCSInternalTemp] = int16ToUint16(300)
	s.HoldingRegisters[RegPCSIGBTTempA] = int16ToUint16(300)
	s.HoldingRegisters[RegPCSIGBTTempB] = int16ToUint16(300)
	s.HoldingRegisters[RegPCSIGBTTempC] = int16ToUint16(300)

	// --- BMS status ---
	s.HoldingRegisters[RegBMSFaultStatus] = 0
	s.HoldingRegisters[RegBMSAlarmStatus] = 0

	// BMS system status
	if !b.bmsHVClosed {
		s.HoldingRegisters[RegBMSSysStatus] = 2 // stopped (open contactor)
	} else if powerKW > 0 {
		s.HoldingRegisters[RegBMSSysStatus] = 3 // charging
	} else if powerKW < 0 {
		s.HoldingRegisters[RegBMSSysStatus] = 4 // discharging
	} else {
		s.HoldingRegisters[RegBMSSysStatus] = 1 // standby
	}

	// Charge/discharge forbidden
	chargeForbid := uint16(0)
	dischargeForbid := uint16(0)
	if soc >= 100.0 {
		chargeForbid = 1
	}
	if soc <= 0.0 {
		dischargeForbid = 1
	}
	s.HoldingRegisters[RegBMSChargeForbid] = chargeForbid
	s.HoldingRegisters[RegBMSDischargeForbid] = dischargeForbid

	// SOC, SOH
	s.HoldingRegisters[RegBMSSOC] = uint16(soc * 10)
	s.HoldingRegisters[RegBMSSOH] = uint16(b.soh * 10)

	// Remaining charge/discharge energy
	remainCharge := b.ratedCapacityKWh - b.currentEnergyKWh
	remainDischarge := b.currentEnergyKWh
	s.HoldingRegisters[RegBMSRemainCharge] = uint16(remainCharge * 10)
	s.HoldingRegisters[RegBMSRemainDischarge] = uint16(remainDischarge * 10)

	// Battery voltage, current, power
	s.HoldingRegisters[RegBMSVoltage] = uint16(b.batteryVoltage * 10)
	s.HoldingRegisters[RegBMSCurrent] = int16ToUint16(int16(dcCurrentA * 10))
	s.HoldingRegisters[RegBMSPower] = int16ToUint16(int16(powerKW * 10))

	// Max allowed charge/discharge power and current
	maxChargePW := b.ratedPowerKW
	maxDischargePW := b.ratedPowerKW
	maxChargeI := ratedCurrentA
	maxDischargeI := ratedCurrentA
	if soc >= 100.0 {
		maxChargePW = 0
		maxChargeI = 0
	}
	if soc <= 0.0 {
		maxDischargePW = 0
		maxDischargeI = 0
	}
	s.HoldingRegisters[RegBMSMaxChargePW] = uint16(maxChargePW * 10)
	s.HoldingRegisters[RegBMSMaxDischargePW] = uint16(maxDischargePW * 10)
	s.HoldingRegisters[RegBMSMaxChargeI] = uint16(maxChargeI * 10)
	s.HoldingRegisters[RegBMSMaxDischargeI] = uint16(maxDischargeI * 10)
}

func boolToU16(v bool) uint16 {
	if v {
		return 1
	}
	return 0
}
