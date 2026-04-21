package main

import (
	"math/rand"

	"aiwatt.net/ems/go-common/zaplog"
)

// processBMSControls handles BMS contactor and fault reset commands.
// Command registers are auto-cleared after processing.
func (b *BESS) processBMSControls() {
	s := b.server

	// Close high-voltage contactor
	if s.HoldingRegisters[RegBMSCloseHV] == 1 {
		if !b.bmsHVClosed {
			zaplog.Info("BMS high-voltage contactor closed")
		}
		b.bmsHVClosed = true
		s.HoldingRegisters[RegBMSCloseHV] = 0
	}

	// Open high-voltage contactor
	if s.HoldingRegisters[RegBMSOpenHV] == 1 {
		if b.bmsHVClosed {
			zaplog.Info("BMS high-voltage contactor opened")
		}
		b.bmsHVClosed = false
		b.actualPowerKW = 0
		s.HoldingRegisters[RegBMSOpenHV] = 0
	}

	// Fault reset
	if s.HoldingRegisters[RegBMSFaultReset] == 1 {
		zaplog.Info("BMS fault reset")
		s.HoldingRegisters[RegBMSFaultReset] = 0
	}
}

// processPCSControls handles PCS startup, shutdown, emergency stop,
// fault reset, and mode settings.
func (b *BESS) processPCSControls() {
	s := b.server

	b.remoteMode = s.HoldingRegisters[RegPCSRemoteLocal] == 1
	b.gridTied = s.HoldingRegisters[RegPCSGridMode] == 0

	// Startup: requires BMS HV closed, otherwise triggers DC undervoltage fault
	if s.HoldingRegisters[RegPCSStartup] == 1 {
		if !b.bmsHVClosed {
			zaplog.Warn("PCS startup failed: BMS HV not closed, DC undervoltage fault")
		} else if !b.pcsRunning {
			zaplog.Info("PCS started")
			b.pcsRunning = true
		}
		s.HoldingRegisters[RegPCSStartup] = 0
	}

	// Shutdown
	if s.HoldingRegisters[RegPCSShutdown] == 1 {
		if b.pcsRunning {
			zaplog.Info("PCS shutdown")
		}
		b.pcsRunning = false
		b.actualPowerKW = 0
		s.HoldingRegisters[RegPCSShutdown] = 0
	}

	// Emergency stop
	if s.HoldingRegisters[RegPCSEStop] == 1 {
		zaplog.Warn("PCS emergency stop")
		b.pcsRunning = false
		b.actualPowerKW = 0
		s.HoldingRegisters[RegPCSEStop] = 0
	}

	// Fault reset
	if s.HoldingRegisters[RegPCSFaultReset] == 1 {
		zaplog.Info("PCS fault reset")
		s.HoldingRegisters[RegPCSFaultReset] = 0
	}
}

// processPowerCommand reads the power command register and applies it.
// Power command only takes effect when: PCS running + remote mode + BMS HV closed.
func (b *BESS) processPowerCommand() {
	if b.pcsRunning && b.remoteMode && b.bmsHVClosed {
		cmdRaw := uint16ToInt16(b.server.HoldingRegisters[RegPCSPowerCmd])
		cmdPowerKW := float64(cmdRaw) * 0.1

		// Clamp to rated power
		if cmdPowerKW > b.ratedPowerKW {
			cmdPowerKW = b.ratedPowerKW
		}
		if cmdPowerKW < -b.ratedPowerKW {
			cmdPowerKW = -b.ratedPowerKW
		}
		// Apply ±0.5% random fluctuation to simulate real-world power tracking error
		jitter := 1.0 + (rand.Float64()*0.01 - 0.005)
		b.actualPowerKW = cmdPowerKW * jitter
	} else if !b.remoteMode {
		// Local mode: ignore power command, keep current power unchanged
	} else {
		b.actualPowerKW = 0
	}
}
