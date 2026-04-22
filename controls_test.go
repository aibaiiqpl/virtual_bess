package main

import (
	"math"
	"testing"

	"aiwatt.net/ems/go-common/mbserver"
)

func TestPowerCommandAlias3010AppliesLike30010(t *testing.T) {
	b := newReadyTestBESS()

	b.server.HoldingRegisters[RegPCSPowerCmdAlias] = 500
	b.processPowerCommand()

	assertPowerNear(t, b.actualPowerKW, 50)
	assertPowerCommandRegisters(t, b, 500)
}

func TestPowerCommandAlias3010CanClearCanonicalSetpoint(t *testing.T) {
	b := newReadyTestBESS()

	b.server.HoldingRegisters[RegPCSPowerCmd] = 500
	b.processPowerCommand()
	b.server.HoldingRegisters[RegPCSPowerCmdAlias] = 0
	b.processPowerCommand()

	if b.actualPowerKW != 0 {
		t.Fatalf("actualPowerKW = %v, want 0", b.actualPowerKW)
	}
	assertPowerCommandRegisters(t, b, 0)
}

func TestPowerCommand30010StillAppliesAndMirrorsAlias(t *testing.T) {
	b := newReadyTestBESS()

	raw := int16ToUint16(-500)
	b.server.HoldingRegisters[RegPCSPowerCmd] = raw
	b.processPowerCommand()

	assertPowerNear(t, b.actualPowerKW, -50)
	assertPowerCommandRegisters(t, b, raw)
}

func TestPowerCommandAlias3010DoesNotApplyInLocalMode(t *testing.T) {
	b := newReadyTestBESS()
	b.remoteMode = false
	b.actualPowerKW = 12.3

	b.server.HoldingRegisters[RegPCSPowerCmdAlias] = 500
	b.processPowerCommand()

	if b.actualPowerKW != 12.3 {
		t.Fatalf("actualPowerKW = %v, want unchanged 12.3", b.actualPowerKW)
	}
	assertPowerCommandRegisters(t, b, 500)
}

func newReadyTestBESS() *BESS {
	cfg := DefaultConfig()
	b := NewBESS(&cfg, mbserver.NewServer())
	b.bmsHVClosed = true
	b.pcsRunning = true
	b.remoteMode = true
	return b
}

func assertPowerNear(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > math.Abs(want)*0.006 {
		t.Fatalf("actualPowerKW = %v, want near %v", got, want)
	}
}

func assertPowerCommandRegisters(t *testing.T, b *BESS, want uint16) {
	t.Helper()
	if got := b.server.HoldingRegisters[RegPCSPowerCmd]; got != want {
		t.Fatalf("RegPCSPowerCmd = %v, want %v", got, want)
	}
	if got := b.server.HoldingRegisters[RegPCSPowerCmdAlias]; got != want {
		t.Fatalf("RegPCSPowerCmdAlias = %v, want %v", got, want)
	}
}
