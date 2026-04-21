package main

import (
	"os"

	"gopkg.in/yaml.v3"
)

type BESSConfig struct {
	RatedCapacityKWh float64 `yaml:"rated_capacity_kwh"`
	RatedPowerKW     float64 `yaml:"rated_power_kw"`
	InitialSOC       float64 `yaml:"initial_soc"`
	SOH              float64 `yaml:"soh"`
	BatteryVoltage   float64 `yaml:"battery_voltage"`
	GridVoltage      float64 `yaml:"grid_voltage"`
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
	BESS   BESSConfig   `yaml:"bess"`
	Modbus ModbusConfig `yaml:"modbus"`
	Log    LogConfig    `yaml:"log"`
}

func DefaultConfig() Config {
	return Config{
		BESS: BESSConfig{
			RatedCapacityKWh: 261,
			RatedPowerKW:     120,
			InitialSOC:       30.0,
			SOH:              100.0,
			BatteryVoltage:   800,
			GridVoltage:      220,
		},
		Modbus: ModbusConfig{
			Address: ":502",
		},
		Log: LogConfig{
			Level:   "info",
			Console: true,
		},
	}
}

// LoadConfig loads configuration from a YAML file.
// If path is empty or the file does not exist, returns default config.
func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		return &cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &cfg, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
