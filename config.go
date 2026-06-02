package main

import (
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

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
	Name        string   `yaml:"name"`          // 仅用于日志/调试
	IsMain      bool     `yaml:"is_main"`       // true = 主关口表，聚合全部 PCS/PV/负载
	PCSSlaveIDs []uint8  `yaml:"pcs_slave_ids"` // 子电表纳入的 PCS（is_main=true 时忽略）
	PVSlaveIDs  []uint8  `yaml:"pv_slave_ids"`  // 子电表纳入的 PV
	LoadNames   []string `yaml:"load_names"`    // 子电表纳入的负载名
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

type IEC61850Config struct {
	Enabled bool                   `yaml:"enabled"`
	Address string                 `yaml:"address"`
	GOOSE   IEC61850GOOSEConfig    `yaml:"goose"`
	Devices []IEC61850DeviceConfig `yaml:"devices"`
}

type IEC61850DeviceConfig struct {
	PCSSlaveID uint8               `yaml:"pcs_slave_id"`
	Address    string              `yaml:"address"`
	GOOSE      IEC61850GOOSEConfig `yaml:"goose"`
}

type IEC61850GOOSEConfig struct {
	Enabled             bool   `yaml:"enabled"`
	InterfaceID         string `yaml:"interface"`
	AppID               string `yaml:"appid"`
	DstMAC              string `yaml:"dst_mac"`
	VLANPriority        uint8  `yaml:"vlan_priority"`
	VLANID              uint16 `yaml:"vlan_id"`
	DisableVLAN         bool   `yaml:"disable_vlan"`
	IntervalMS          int    `yaml:"interval_ms"`
	TimeAllowedToLiveMS uint32 `yaml:"time_allowed_to_live_ms"`
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
	IEC61850     IEC61850Config      `yaml:"iec61850"`
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
		Modbus:   ModbusConfig{Address: ":502"},
		IEC61850: IEC61850Config{Address: ":102"},
		Grid:     GridConfig{Voltage: 220},
		PCS:      PCSConfig{ACVoltage: 400},
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
	if c.IEC61850.Address == "" {
		c.IEC61850.Address = ":102"
	}
	applyIEC61850GOOSEDefaults(&c.IEC61850.GOOSE)
	for i := range c.IEC61850.Devices {
		applyIEC61850GOOSEDefaults(&c.IEC61850.Devices[i].GOOSE)
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
	if err := c.IEC61850.validate(pcsIDs); err != nil {
		return err
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

func (c IEC61850Config) validate(pcsIDs map[uint8]bool) error {
	if c.GOOSE.Enabled && !c.Enabled {
		return fmt.Errorf("iec61850.enabled must be true when iec61850.goose.enabled is true")
	}
	for i, device := range c.Devices {
		if device.GOOSE.Enabled && !c.Enabled {
			return fmt.Errorf("iec61850.enabled must be true when iec61850.devices[%d].goose.enabled is true", i)
		}
	}
	if !c.Enabled {
		return nil
	}
	if len(c.Devices) == 0 {
		return validateIEC61850GOOSEConfig("iec61850.goose", c.GOOSE)
	}

	seenAddresses := map[string]bool{}
	seenGOOSEAppIDs := map[uint16]bool{}
	for i, device := range c.Devices {
		label := fmt.Sprintf("iec61850.devices[%d]", i)
		if device.PCSSlaveID == 0 {
			return fmt.Errorf("%s.pcs_slave_id must be set", label)
		}
		if !pcsIDs[device.PCSSlaveID] {
			return fmt.Errorf("%s references unknown pcs_slave_id %d", label, device.PCSSlaveID)
		}
		if device.Address == "" {
			return fmt.Errorf("%s.address must be set", label)
		}
		if _, _, err := splitIEC61850Address(device.Address); err != nil {
			return fmt.Errorf("%s.address: %w", label, err)
		}
		if seenAddresses[device.Address] {
			return fmt.Errorf("%s.address %q duplicated", label, device.Address)
		}
		seenAddresses[device.Address] = true

		if err := validateIEC61850GOOSEConfig(label+".goose", device.GOOSE); err != nil {
			return err
		}
		if device.GOOSE.Enabled {
			appID, err := parseIEC61850AppID(device.GOOSE.AppID)
			if err != nil {
				return fmt.Errorf("%s.goose.appid: %w", label, err)
			}
			if seenGOOSEAppIDs[appID] {
				return fmt.Errorf("%s.goose.appid %q duplicated", label, device.GOOSE.AppID)
			}
			seenGOOSEAppIDs[appID] = true
		}
	}
	return nil
}

func validateIEC61850GOOSEConfig(label string, cfg IEC61850GOOSEConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if cfg.InterfaceID == "" {
		return fmt.Errorf("%s.interface must be set", label)
	}
	if _, err := parseIEC61850AppID(cfg.AppID); err != nil {
		return fmt.Errorf("%s.appid: %w", label, err)
	}
	if _, err := parseIEC61850MAC(cfg.DstMAC); err != nil {
		return fmt.Errorf("%s.dst_mac: %w", label, err)
	}
	if cfg.IntervalMS <= 0 {
		return fmt.Errorf("%s.interval_ms must be positive", label)
	}
	if cfg.TimeAllowedToLiveMS == 0 {
		return fmt.Errorf("%s.time_allowed_to_live_ms must be positive", label)
	}
	return nil
}

func applyIEC61850GOOSEDefaults(cfg *IEC61850GOOSEConfig) {
	if cfg.AppID == "" {
		cfg.AppID = "0100"
	}
	if cfg.DstMAC == "" {
		cfg.DstMAC = "01-0C-CD-01-01-00"
	}
	if cfg.VLANPriority == 0 {
		cfg.VLANPriority = 4
	}
	if cfg.IntervalMS == 0 {
		cfg.IntervalMS = 1000
	}
	if cfg.TimeAllowedToLiveMS == 0 {
		cfg.TimeAllowedToLiveMS = 5000
	}
}

func parseIEC61850AppID(value string) (uint16, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return 0, fmt.Errorf("must be set")
	}
	text = strings.TrimPrefix(strings.TrimPrefix(text, "0x"), "0X")
	appID, err := strconv.ParseUint(text, 16, 16)
	if err != nil {
		return 0, fmt.Errorf("must be a hex uint16, got %q", value)
	}
	return uint16(appID), nil
}

func parseIEC61850MAC(value string) ([6]uint8, error) {
	var mac [6]uint8
	text := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(value), "-", ""), ":", "")
	if len(text) != 12 {
		return mac, fmt.Errorf("must contain 6 octets, got %q", value)
	}
	bytes, err := hex.DecodeString(text)
	if err != nil {
		return mac, fmt.Errorf("must be hex bytes, got %q", value)
	}
	parsed := net.HardwareAddr(bytes)
	if len(parsed) != len(mac) {
		return mac, fmt.Errorf("must contain 6 octets, got %q", value)
	}
	copy(mac[:], parsed)
	return mac, nil
}

func splitIEC61850Address(address string) (string, int, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, fmt.Errorf("invalid IEC 61850 address %q: %w", address, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("invalid IEC 61850 port %q", portText)
	}
	return host, port, nil
}
