package simulator

import (
	"strings"
	"testing"
)

func TestXN3477ConfigAcceptsIndependentEndpoints(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BatteryUnits[0].ClusterCount = 2
	cfg.XN3477 = XN3477Config{
		Enabled: true,
		Devices: []XN3477DeviceConfig{{
			BMSSlaveID:      cfg.BatteryUnits[0].BMSSlaveID,
			Address:         ":8502",
			BAUSlaveID:      1,
			ClusterSlaveIDs: []uint8{2, 3},
		}},
	}

	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() error = %v", err)
	}
}

func TestXN3477AWSConfigLoadsSixIndependentEndpoints(t *testing.T) {
	cfg, err := LoadConfig("../../configs/bess_6_units_5mwh_2_5mw_xn3477.yaml")
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if len(cfg.XN3477.Devices) != 6 {
		t.Fatalf("XN3477 endpoint count = %d, want 6", len(cfg.XN3477.Devices))
	}
	for i, device := range cfg.XN3477.Devices {
		if device.BAUSlaveID != 1 {
			t.Fatalf("devices[%d] BAU slave = %d, want 1", i, device.BAUSlaveID)
		}
		if len(device.ClusterSlaveIDs) != 12 {
			t.Fatalf(
				"devices[%d] cluster slave count = %d, want 12",
				i,
				len(device.ClusterSlaveIDs),
			)
		}
	}
}

func TestXN3477ConfigRejectsIncompleteClusterMapping(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BatteryUnits[0].ClusterCount = 2
	cfg.XN3477 = XN3477Config{
		Enabled: true,
		Devices: []XN3477DeviceConfig{{
			BMSSlaveID:      cfg.BatteryUnits[0].BMSSlaveID,
			Address:         ":8502",
			BAUSlaveID:      1,
			ClusterSlaveIDs: []uint8{2},
		}},
	}

	err := cfg.validate()
	if err == nil || !strings.Contains(err.Error(), "has 1 entries, want 2") {
		t.Fatalf("validate() error = %v, want incomplete cluster mapping error", err)
	}
}

func TestXN3477ConfigRejectsDuplicateSlaveIDWithinEndpoint(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BatteryUnits[0].ClusterCount = 2
	cfg.XN3477 = XN3477Config{
		Enabled: true,
		Devices: []XN3477DeviceConfig{{
			BMSSlaveID:      cfg.BatteryUnits[0].BMSSlaveID,
			Address:         ":8502",
			BAUSlaveID:      1,
			ClusterSlaveIDs: []uint8{2, 2},
		}},
	}

	err := cfg.validate()
	if err == nil || !strings.Contains(err.Error(), "slave_id 2 is duplicated") {
		t.Fatalf("validate() error = %v, want duplicate slave error", err)
	}
}
