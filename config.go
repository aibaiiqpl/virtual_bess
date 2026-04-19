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

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
