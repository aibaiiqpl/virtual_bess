//go:build !iec61850

package iec61850sim

import (
	"testing"

	"virtual_bess/internal/simulator"
)

func TestStartIEC61850ServerRequiresBuildTagWhenEnabled(t *testing.T) {
	svc, err := StartServer(simulator.IEC61850Config{Enabled: true, Address: ":102"}, nil)
	if err == nil {
		t.Fatalf("StartServer() error = nil, want build tag error")
	}
	if svc != nil {
		t.Fatalf("StartServer() service = %#v, want nil", svc)
	}
}

func TestStartIEC61850ServerNoopWhenDisabled(t *testing.T) {
	svc, err := StartServer(simulator.IEC61850Config{Enabled: false}, nil)
	if err != nil {
		t.Fatalf("StartServer() error = %v, want nil", err)
	}
	if svc == nil {
		t.Fatal("StartServer() service = nil, want noop service")
	}
}
