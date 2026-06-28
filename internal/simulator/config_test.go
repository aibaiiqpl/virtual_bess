package simulator

import (
	"fmt"
	"testing"
)

func TestIEC61850GOOSEConfigDefaultsAndParsesCIDAddress(t *testing.T) {
	cfg := DefaultConfig()
	cfg.IEC61850.Enabled = true
	cfg.IEC61850.GOOSE.Enabled = true
	cfg.IEC61850.GOOSE.InterfaceID = "eth0"
	cfg.applyDefaults()

	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() error = %v", err)
	}
	appID, err := parseIEC61850AppID(cfg.IEC61850.GOOSE.AppID)
	if err != nil {
		t.Fatalf("parseIEC61850AppID() error = %v", err)
	}
	if appID != 0x0100 {
		t.Fatalf("APPID = %#x, want 0x0100", appID)
	}
	mac, err := parseIEC61850MAC(cfg.IEC61850.GOOSE.DstMAC)
	if err != nil {
		t.Fatalf("parseIEC61850MAC() error = %v", err)
	}
	want := [6]uint8{0x01, 0x0c, 0xcd, 0x01, 0x01, 0x00}
	if mac != want {
		t.Fatalf("dst MAC = %#v, want %#v", mac, want)
	}
}

func TestIEC61850GOOSEConfigRequiresIEC61850(t *testing.T) {
	cfg := DefaultConfig()
	cfg.IEC61850.GOOSE.Enabled = true
	cfg.IEC61850.GOOSE.InterfaceID = "eth0"
	cfg.applyDefaults()

	if err := cfg.validate(); err == nil {
		t.Fatal("validate() error = nil, want IEC 61850 enabled error")
	}
}

func TestIEC61850GOOSEConfigRejectsInvalidAddress(t *testing.T) {
	cfg := DefaultConfig()
	cfg.IEC61850.Enabled = true
	cfg.IEC61850.GOOSE.Enabled = true
	cfg.IEC61850.GOOSE.InterfaceID = "eth0"
	cfg.IEC61850.GOOSE.AppID = "not-hex"
	cfg.IEC61850.GOOSE.DstMAC = "01-0C-CD-01-01-00"
	cfg.applyDefaults()

	if err := cfg.validate(); err == nil {
		t.Fatal("validate() error = nil, want APPID error")
	}

	cfg.IEC61850.GOOSE.AppID = "0100"
	cfg.IEC61850.GOOSE.DstMAC = "01-0C-CD"
	if err := cfg.validate(); err == nil {
		t.Fatal("validate() error = nil, want dst_mac error")
	}
}

func TestIEC61850DevicesValidatePCSSlavesAndUniqueEndpoints(t *testing.T) {
	cfg := DefaultConfig()
	cfg.IEC61850.Enabled = true
	cfg.IEC61850.Devices = []IEC61850DeviceConfig{
		{PCSSlaveID: 1, Address: ":102"},
		{PCSSlaveID: 2, Address: ":1102"},
	}
	cfg.BatteryUnits = append(cfg.BatteryUnits, BatteryUnitConfig{
		PCSSlaveID:         2,
		BMSSlaveID:         12,
		RatedCapacityKWh:   261,
		RatedPowerKW:       120,
		InitialSOC:         30,
		SOH:                100,
		BatteryVoltageFull: 1400,
		ClusterCount:       1,
	})
	cfg.applyDefaults()

	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() error = %v", err)
	}

	cfg.IEC61850.Devices[1].Address = ":102"
	if err := cfg.validate(); err == nil {
		t.Fatal("validate() error = nil, want duplicate MMS address error")
	}

	cfg.IEC61850.Devices[1].Address = ":1102"
	cfg.IEC61850.Devices[1].PCSSlaveID = 9
	if err := cfg.validate(); err == nil {
		t.Fatal("validate() error = nil, want unknown PCS slave error")
	}
}

func TestIEC61850DevicesRequireUniqueGOOSEAppIDs(t *testing.T) {
	cfg := DefaultConfig()
	cfg.IEC61850.Enabled = true
	cfg.IEC61850.Devices = []IEC61850DeviceConfig{
		{
			PCSSlaveID: 1,
			Address:    ":102",
			GOOSE: IEC61850GOOSEConfig{
				Enabled:     true,
				InterfaceID: "eth0",
				AppID:       "0100",
			},
		},
		{
			PCSSlaveID: 2,
			Address:    ":1102",
			GOOSE: IEC61850GOOSEConfig{
				Enabled:     true,
				InterfaceID: "eth0",
				AppID:       "0100",
			},
		},
	}
	cfg.BatteryUnits = append(cfg.BatteryUnits, BatteryUnitConfig{
		PCSSlaveID:         2,
		BMSSlaveID:         12,
		RatedCapacityKWh:   261,
		RatedPowerKW:       120,
		InitialSOC:         30,
		SOH:                100,
		BatteryVoltageFull: 1400,
		ClusterCount:       1,
	})
	cfg.applyDefaults()

	if err := cfg.validate(); err == nil {
		t.Fatal("validate() error = nil, want duplicate GOOSE APPID error")
	}
}

// 回归：交付用多机柜配置必须给每个端点显式小写 IED 名 pcs0N，且通过唯一性校验。
// emu 点表（pcs0NCTRL/MEAS/PIGO，大小写敏感）依赖这些名字一致；留空回退大写
// PCS0N 会导致遥控 ControlObjectClient_create 失败。
func TestGeneratedBESSConfigsUseLowercasePerEndpointIEDNames(t *testing.T) {
	for _, path := range []string{
		"../../configs/bess_4_units_5mwh_2_5mw.yaml",
		"../../configs/bess_7_units_5mwh_2_5mw.yaml",
	} {
		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatalf("LoadConfig(%q) error = %v", path, err)
		}
		seen := map[string]bool{}
		for i, dev := range cfg.IEC61850.Devices {
			name := effectiveIEDName(cfg.IEC61850, dev, true)
			want := fmt.Sprintf("pcs%02d", dev.PCSSlaveID)
			if name != want {
				t.Fatalf("%s device[%d] IED name = %q, want %q", path, i, name, want)
			}
			if seen[name] {
				t.Fatalf("%s device[%d] IED name %q duplicated", path, i, name)
			}
			seen[name] = true
		}
	}
}

func TestGeneratedBESSConfigs(t *testing.T) {
	tests := []struct {
		path  string
		units int
	}{
		{path: "../../configs/bess_4_units_5mwh_2_5mw.yaml", units: 4},
		{path: "../../configs/bess_7_units_5mwh_2_5mw.yaml", units: 7},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			cfg, err := LoadConfig(tt.path)
			if err != nil {
				t.Fatalf("LoadConfig(%q) error = %v", tt.path, err)
			}
			if got := len(cfg.BatteryUnits); got != tt.units {
				t.Fatalf("battery unit count = %d, want %d", got, tt.units)
			}
			if got := len(cfg.IEC61850.Devices); got != tt.units {
				t.Fatalf("IEC 61850 device count = %d, want %d", got, tt.units)
			}
			for i, unit := range cfg.BatteryUnits {
				if unit.RatedCapacityKWh != 5000 {
					t.Fatalf("battery_units[%d].rated_capacity_kwh = %v, want 5000", i, unit.RatedCapacityKWh)
				}
				if unit.RatedPowerKW != 2500 {
					t.Fatalf("battery_units[%d].rated_power_kw = %v, want 2500", i, unit.RatedPowerKW)
				}
			}
		})
	}
}
