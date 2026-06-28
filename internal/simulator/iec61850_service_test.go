//go:build !iec61850

package simulator

import "testing"

func TestStartIEC61850ServerRequiresBuildTagWhenEnabled(t *testing.T) {
	svc, err := StartIEC61850Server(IEC61850Config{Enabled: true, Address: ":102"}, nil)
	if err == nil {
		t.Fatalf("StartIEC61850Server() error = nil, want build tag error")
	}
	if svc != nil {
		t.Fatalf("StartIEC61850Server() service = %#v, want nil", svc)
	}
}

func TestStartIEC61850ServerNoopWhenDisabled(t *testing.T) {
	svc, err := StartIEC61850Server(IEC61850Config{Enabled: false}, nil)
	if err != nil {
		t.Fatalf("StartIEC61850Server() error = %v, want nil", err)
	}
	if svc == nil {
		t.Fatal("StartIEC61850Server() service = nil, want noop service")
	}
}
