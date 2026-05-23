package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type GridConfig struct {
	Voltage float64 `yaml:"voltage"`
}

type PCSConfig struct {
	// ACVoltage 是 PCS / PV 逆变器 AC 出口的相电压（升压变低压侧），
	// 与电网电压 (Grid.Voltage, 电表所在侧) 分开配置。
	ACVoltage float64 `yaml:"ac_voltage"`
}

type BatteryUnitConfig struct {
	PCSSlaveID         uint8   `yaml:"pcs_slave_id"`
	BMSSlaveID         uint8   `yaml:"bms_slave_id"`
	RatedCapacityKWh   float64 `yaml:"rated_capacity_kwh"`
	RatedPowerKW       float64 `yaml:"rated_power_kw"`
	InitialSOC         float64 `yaml:"initial_soc"`
	SOH                float64 `yaml:"soh"`
	BatteryVoltageFull float64 `yaml:"battery_voltage_full"`
	ClusterCount       int     `yaml:"cluster_count"`
}

type PVUnitConfig struct {
	SlaveID      uint8   `yaml:"slave_id"`
	RatedPowerKW float64 `yaml:"rated_power_kw"`
}

type MeterConfig struct {
	SlaveID     uint8    `yaml:"slave_id"`
	Name        string   `yaml:"name"`           // 仅用于日志/调试
	IsMain      bool     `yaml:"is_main"`        // true = 主关口表，聚合全部 PCS/PV/负载
	PCSSlaveIDs []uint8  `yaml:"pcs_slave_ids"`  // 子电表纳入的 PCS（is_main=true 时忽略）
	PVSlaveIDs  []uint8  `yaml:"pv_slave_ids"`   // 子电表纳入的 PV
	LoadNames   []string `yaml:"load_names"`     // 子电表纳入的负载名
	// FlowDirection 决定 forward / reverse 的方向：
	//   "inflow"  (默认): forward = 从上游/电网流向下游设备（充电/消耗为正）
	//   "outflow":         forward = 从下游设备回流到上游/电网（PV 发电为正）
	FlowDirection string `yaml:"flow_direction"`
	// Voltage 一次侧线/相电压 V（用于电流计算）；为 0 时回退到 grid.voltage。
	Voltage float64 `yaml:"voltage"`
	// PTRatio 电压互感器变比（一次/二次）。寄存器存的是二次侧值（U16, 0.1V）。
	// 1.0 = 直接接入。35kV 系统典型 350，10kV 系统典型 100。
	// 不设置时自动取能让二次值落入 U16 范围的最小整数变比。
	PTRatio float64 `yaml:"pt_ratio"`
	// CTRatio 电流互感器变比（一次/二次）。寄存器存的是二次侧值（S32, 0.1A）。
	// 1.0 = 直接接入。常见 CT 二次侧 5A：典型变比 600/5=120、1000/5=200、2000/5=400。
	// 不设置或 0 = 1.0（写一次侧值）。
	CTRatio float64 `yaml:"ct_ratio"`
}

type LoadCfg struct {
	Name         string  `yaml:"name"`
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

type StateConfig struct {
	File     string `yaml:"file"`     // 持久化 JSON 路径；空 = 不持久化
	Interval int    `yaml:"interval"` // 刷盘间隔（秒），默认 15
}

type Config struct {
	Modbus       ModbusConfig        `yaml:"modbus"`
	Grid         GridConfig          `yaml:"grid"`
	PCS          PCSConfig           `yaml:"pcs"`
	BatteryUnits []BatteryUnitConfig `yaml:"battery_units"`
	PVUnits      []PVUnitConfig      `yaml:"pv_units"`
	Meters       []MeterConfig       `yaml:"meters"`
	Loads        []LoadCfg           `yaml:"loads"`
	Log          LogConfig           `yaml:"log"`
	State        StateConfig         `yaml:"state"`
	// Timezone 用于 PV 日照曲线计算，IANA 时区名（如 "Europe/Lisbon"）；空则使用系统本地时区。
	Timezone string `yaml:"timezone"`
}

func DefaultConfig() Config {
	return Config{
		Modbus: ModbusConfig{Address: ":502"},
		Grid:   GridConfig{Voltage: 220},
		PCS:    PCSConfig{ACVoltage: 400},
		BatteryUnits: []BatteryUnitConfig{{
			PCSSlaveID:         1,
			BMSSlaveID:         11,
			RatedCapacityKWh:   261,
			RatedPowerKW:       120,
			InitialSOC:         30.0,
			SOH:                100.0,
			BatteryVoltageFull: 1400,
			ClusterCount:       1,
		}},
		PVUnits: []PVUnitConfig{{
			SlaveID:      21,
			RatedPowerKW: 120,
		}},
		Meters: []MeterConfig{{SlaveID: 31, Name: "main", IsMain: true}},
		Loads:  []LoadCfg{{Name: "load", RatedPowerKW: 80}},
		Log:    LogConfig{Level: "info", Console: true},
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
	if c.PCS.ACVoltage == 0 {
		c.PCS.ACVoltage = 400
	}
	if c.State.Interval <= 0 {
		c.State.Interval = 15
	}
	for i := range c.BatteryUnits {
		bu := &c.BatteryUnits[i]
		if bu.ClusterCount < 1 {
			bu.ClusterCount = 1
		}
		if bu.BatteryVoltageFull == 0 {
			bu.BatteryVoltageFull = 1400
		}
		if bu.SOH == 0 {
			bu.SOH = 100.0
		}
	}
}

// validate 校验 slaveId 唯一且非零，至少一个电池单元、一个电表。
func (c *Config) validate() error {
	if len(c.BatteryUnits) == 0 {
		return fmt.Errorf("at least one battery_unit is required")
	}
	if len(c.Meters) == 0 {
		return fmt.Errorf("at least one meter is required")
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

	pcsIDs := map[uint8]bool{}
	pvIDs := map[uint8]bool{}
	loadNames := map[string]bool{}

	for i, bu := range c.BatteryUnits {
		if err := check(bu.PCSSlaveID, fmt.Sprintf("battery_units[%d].pcs", i)); err != nil {
			return err
		}
		if err := check(bu.BMSSlaveID, fmt.Sprintf("battery_units[%d].bms", i)); err != nil {
			return err
		}
		pcsIDs[bu.PCSSlaveID] = true
	}
	for i, pv := range c.PVUnits {
		if err := check(pv.SlaveID, fmt.Sprintf("pv_units[%d]", i)); err != nil {
			return err
		}
		pvIDs[pv.SlaveID] = true
	}
	for i, ld := range c.Loads {
		if ld.Name == "" {
			return fmt.Errorf("loads[%d].name must be set", i)
		}
		if loadNames[ld.Name] {
			return fmt.Errorf("loads[%d].name %q duplicated", i, ld.Name)
		}
		loadNames[ld.Name] = true
	}
	for i, m := range c.Meters {
		label := fmt.Sprintf("meters[%d]", i)
		if m.Name != "" {
			label = fmt.Sprintf("meter %q", m.Name)
		}
		if err := check(m.SlaveID, label); err != nil {
			return err
		}
		switch m.FlowDirection {
		case "", "inflow", "outflow":
		default:
			return fmt.Errorf("%s flow_direction must be inflow or outflow, got %q", label, m.FlowDirection)
		}
		if m.IsMain {
			continue
		}
		for _, id := range m.PCSSlaveIDs {
			if !pcsIDs[id] {
				return fmt.Errorf("%s references unknown pcs_slave_id %d", label, id)
			}
		}
		for _, id := range m.PVSlaveIDs {
			if !pvIDs[id] {
				return fmt.Errorf("%s references unknown pv_slave_id %d", label, id)
			}
		}
		for _, n := range m.LoadNames {
			if !loadNames[n] {
				return fmt.Errorf("%s references unknown load name %q", label, n)
			}
		}
	}
	return nil
}
