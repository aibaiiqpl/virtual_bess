package simulator

import "fmt"

// XN3477Config 描述协能 XN3477 BMS 的独立 Modbus TCP 端点。
// 每套 BMS 使用独立端口，因此不同电池单元可以重复使用 BAU/BCU slaveId。
type XN3477Config struct {
	Enabled bool                 `yaml:"enabled"`
	Devices []XN3477DeviceConfig `yaml:"devices"`
}

// XN3477DeviceConfig 将一套内部 BMS 仿真状态映射到一个 XN3477 TCP 端点。
type XN3477DeviceConfig struct {
	BMSSlaveID      uint8   `yaml:"bms_slave_id"`
	Address         string  `yaml:"address"`
	BAUSlaveID      uint8   `yaml:"bau_slave_id"`
	ClusterSlaveIDs []uint8 `yaml:"cluster_slave_ids"`
}

func (c XN3477Config) validate(modbusAddress string, bmsClusterCounts map[uint8]int) error {
	if !c.Enabled {
		return nil
	}
	if len(c.Devices) == 0 {
		return fmt.Errorf("xn3477.enabled is true but no xn3477.devices configured")
	}

	addresses := map[string]bool{modbusAddress: true}
	bmsDevices := map[uint8]bool{}
	for i, device := range c.Devices {
		label := fmt.Sprintf("xn3477.devices[%d]", i)
		clusterCount, ok := bmsClusterCounts[device.BMSSlaveID]
		if !ok {
			return fmt.Errorf("%s references unknown bms_slave_id %d", label, device.BMSSlaveID)
		}
		if bmsDevices[device.BMSSlaveID] {
			return fmt.Errorf("%s duplicates bms_slave_id %d", label, device.BMSSlaveID)
		}
		bmsDevices[device.BMSSlaveID] = true
		if device.Address == "" {
			return fmt.Errorf("%s.address must be set", label)
		}
		if addresses[device.Address] {
			return fmt.Errorf("%s.address %q conflicts with another Modbus endpoint", label, device.Address)
		}
		addresses[device.Address] = true
		if device.BAUSlaveID == 0 {
			return fmt.Errorf("%s.bau_slave_id must be non-zero", label)
		}
		if len(device.ClusterSlaveIDs) != clusterCount {
			return fmt.Errorf(
				"%s.cluster_slave_ids has %d entries, want %d",
				label,
				len(device.ClusterSlaveIDs),
				clusterCount,
			)
		}
		slaves := map[uint8]bool{device.BAUSlaveID: true}
		for cluster, slaveID := range device.ClusterSlaveIDs {
			if slaveID == 0 {
				return fmt.Errorf("%s.cluster_slave_ids[%d] must be non-zero", label, cluster)
			}
			if slaves[slaveID] {
				return fmt.Errorf("%s slave_id %d is duplicated", label, slaveID)
			}
			slaves[slaveID] = true
		}
	}
	return nil
}
