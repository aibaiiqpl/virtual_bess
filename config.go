package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type GridConfig struct {
	Voltage float64 `yaml:"voltage"`
}

type BatteryUnitConfig struct {
	PCSSlaveID       uint8   `yaml:"pcs_slave_id"`
	BMSSlaveID       uint8   `yaml:"bms_slave_id"`
	RatedCapacityKWh float64 `yaml:"rated_capacity_kwh"`
	RatedPowerKW     float64 `yaml:"rated_power_kw"`
	InitialSOC       float64 `yaml:"initial_soc"`
	SOH              float64 `yaml:"soh"`
	BatteryVoltage   float64 `yaml:"battery_voltage"`
	ClusterCount     int     `yaml:"cluster_count"`
}

type PVUnitConfig struct {
	SlaveID      uint8   `yaml:"slave_id"`
	RatedPowerKW float64 `yaml:"rated_power_kw"`
}

type MeterConfig struct {
	SlaveID uint8 `yaml:"slave_id"`
}

type LoadCfg struct {
	RatedPowerKW float64 `yaml:"rated_power_kw"`
}

type ModbusConfig struct {
	Address string `yaml:"address"`
}

type LogConfig struct {
	File    string `yaml:"file"`
	Level   string `yaml:"level"`
	Console bool   `yaml:"console"`
}

type Config struct {
	Modbus        ModbusConfig        `yaml:"modbus"`
	Grid          GridConfig          `yaml:"grid"`
	BatteryUnits  []BatteryUnitConfig `yaml:"battery_units"`
	PVUnits       []PVUnitConfig      `yaml:"pv_units"`
	Meter         MeterConfig         `yaml:"meter"`
	Load          LoadCfg             `yaml:"load"`
	Log           LogConfig           `yaml:"log"`
}

func DefaultConfig() Config {
	return Config{
		Modbus: ModbusConfig{Address: ":502"},
		Grid:   GridConfig{Voltage: 220},
		BatteryUnits: []BatteryUnitConfig{{
			PCSSlaveID:       1,
			BMSSlaveID:       11,
			RatedCapacityKWh: 261,
			RatedPowerKW:     120,
			InitialSOC:       30.0,
			SOH:              100.0,
			BatteryVoltage:   800,
			ClusterCount:     1,
		}},
		PVUnits: []PVUnitConfig{{
			SlaveID:      21,
			RatedPowerKW: 120,
		}},
		Meter: MeterConfig{SlaveID: 31},
		Load:  LoadCfg{RatedPowerKW: 80},
		Log:   LogConfig{Level: "info", Console: true},
	}
}

// LoadConfig 加载 YAML 配置；path 为空或文件不存在时使用 DefaultConfig。
func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		if err := cfg.validate(); err != nil {
			return nil, err
		}
		return &cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if err := cfg.validate(); err != nil {
				return nil, err
			}
			return &cfg, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Grid.Voltage == 0 {
		c.Grid.Voltage = 220
	}
	if c.Modbus.Address == "" {
		c.Modbus.Address = ":502"
	}
	for i := range c.BatteryUnits {
		bu := &c.BatteryUnits[i]
		if bu.ClusterCount < 1 {
			bu.ClusterCount = 1
		}
		if bu.BatteryVoltage == 0 {
			bu.BatteryVoltage = 800
		}
		if bu.SOH == 0 {
			bu.SOH = 100.0
		}
	}
}

// validate 校验 slaveId 唯一且非零，至少一个电池单元。
func (c *Config) validate() error {
	if len(c.BatteryUnits) == 0 {
		return fmt.Errorf("at least one battery_unit is required")
	}
	if c.Meter.SlaveID == 0 {
		return fmt.Errorf("meter.slave_id must be non-zero")
	}

	seen := map[uint8]string{}
	check := func(id uint8, name string) error {
		if id == 0 {
			return fmt.Errorf("%s slave_id must be non-zero", name)
		}
		if existing, ok := seen[id]; ok {
			return fmt.Errorf("slave_id %d conflict: %s vs %s", id, existing, name)
		}
		seen[id] = name
		return nil
	}

	for i, bu := range c.BatteryUnits {
		if err := check(bu.PCSSlaveID, fmt.Sprintf("battery_units[%d].pcs", i)); err != nil {
			return err
		}
		if err := check(bu.BMSSlaveID, fmt.Sprintf("battery_units[%d].bms", i)); err != nil {
			return err
		}
	}
	for i, pv := range c.PVUnits {
		if err := check(pv.SlaveID, fmt.Sprintf("pv_units[%d]", i)); err != nil {
			return err
		}
	}
	if err := check(c.Meter.SlaveID, "meter"); err != nil {
		return err
	}
	return nil
}
