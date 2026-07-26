package xn3477

import (
	"fmt"

	"virtual_bess/internal/mbserver"
	"virtual_bess/internal/simulator"
	"virtual_bess/internal/zaplog"
)

// Service 管理多套 XN3477 Modbus TCP 端点。
type Service struct {
	endpoints []*endpoint
}

// StartServer 为配置中的每套 BMS 启动独立端点。
func StartServer(cfg simulator.XN3477Config, sim *simulator.Simulator) (*Service, error) {
	service := &Service{}
	if !cfg.Enabled {
		return service, nil
	}

	for i, device := range cfg.Devices {
		battery, ok := sim.BatteryUnitByBMSSlaveID(device.BMSSlaveID)
		if !ok {
			service.Close()
			return nil, fmt.Errorf(
				"xn3477.devices[%d] references unknown bms_slave_id %d",
				i,
				device.BMSSlaveID,
			)
		}
		ep := newEndpoint(device, sim, battery)
		if err := ep.listen(); err != nil {
			service.Close()
			return nil, fmt.Errorf("listen XN3477 endpoint %s: %w", device.Address, err)
		}
		service.endpoints = append(service.endpoints, ep)
		zaplog.Infof(
			"XN3477 BMS[%d] listening on %s (BAU slave %d, %d BCU slaves)",
			device.BMSSlaveID,
			device.Address,
			device.BAUSlaveID,
			len(device.ClusterSlaveIDs),
		)
	}
	service.Sync()
	return service, nil
}

// Sync 将通用电池仿真寄存器同步到 XN3477 地址空间。
func (s *Service) Sync() {
	for _, ep := range s.endpoints {
		ep.sync()
	}
}

// Close 停止全部 XN3477 TCP 监听。
func (s *Service) Close() {
	for _, ep := range s.endpoints {
		ep.server.Close()
	}
}

type endpoint struct {
	config   simulator.XN3477DeviceConfig
	sim      *simulator.Simulator
	battery  *simulator.BatteryUnit
	server   *mbserver.Server
	banks    map[uint8]*simulator.SlaveBank
	bau      *simulator.SlaveBank
	clusters []*simulator.SlaveBank
}

func newEndpoint(
	cfg simulator.XN3477DeviceConfig,
	sim *simulator.Simulator,
	battery *simulator.BatteryUnit,
) *endpoint {
	ep := &endpoint{
		config:  cfg,
		sim:     sim,
		battery: battery,
		server:  mbserver.NewServer(),
		banks:   make(map[uint8]*simulator.SlaveBank),
	}
	ep.bau = simulator.NewSlaveBank(cfg.BAUSlaveID, false)
	ep.banks[cfg.BAUSlaveID] = ep.bau
	for _, slaveID := range cfg.ClusterSlaveIDs {
		bank := simulator.NewSlaveBank(slaveID, false)
		ep.banks[slaveID] = bank
		ep.clusters = append(ep.clusters, bank)
	}
	for addr := uint16(xnBAUClusterEnableStart); addr < xnBAUClusterEnableStart+uint16(len(ep.clusters)); addr++ {
		ep.bau.WriteU16(addr, 1)
	}

	ep.server.RegisterFunctionHandler(1, illegalFunction)
	ep.server.RegisterFunctionHandler(2, illegalFunction)
	ep.server.RegisterFunctionHandler(3, ep.handleRead)
	ep.server.RegisterFunctionHandler(4, ep.handleRead)
	ep.server.RegisterFunctionHandler(5, illegalFunction)
	ep.server.RegisterFunctionHandler(6, ep.handleWriteSingle)
	ep.server.RegisterFunctionHandler(15, illegalFunction)
	ep.server.RegisterFunctionHandler(16, ep.handleWriteMultiple)
	return ep
}

func (ep *endpoint) listen() error {
	return ep.server.ListenTCP(ep.config.Address)
}

func illegalFunction(_ *mbserver.Server, _ mbserver.Framer) ([]byte, *mbserver.Exception) {
	return nil, &mbserver.IllegalFunction
}

func (ep *endpoint) handleRead(
	_ *mbserver.Server,
	frame mbserver.Framer,
) ([]byte, *mbserver.Exception) {
	bank, ok := ep.banks[frame.GetDeviceId()]
	if !ok {
		return nil, &mbserver.GatewayTargetDeviceFailedtoRespond
	}
	addr, count, _ := mbserver.RegisterAddressAndNumber(frame)
	data, exception := bank.Holding.GetData(uint16(addr), uint16(count))
	if exception != nil && exception != &mbserver.Success {
		return nil, exception
	}
	return append([]byte{byte(count * 2)}, mbserver.Uint16ToBytes(data)...), &mbserver.Success
}

func (ep *endpoint) handleWriteSingle(
	_ *mbserver.Server,
	frame mbserver.Framer,
) ([]byte, *mbserver.Exception) {
	addr, value := mbserver.RegisterAddressAndValue(frame)
	if exception := ep.write(frame.GetDeviceId(), uint16(addr), value); exception != &mbserver.Success {
		return nil, exception
	}
	return frame.GetData()[0:4], &mbserver.Success
}

func (ep *endpoint) handleWriteMultiple(
	_ *mbserver.Server,
	frame mbserver.Framer,
) ([]byte, *mbserver.Exception) {
	addr, count, _ := mbserver.RegisterAddressAndNumber(frame)
	valueBytes := frame.GetData()[5:]
	if len(valueBytes)/2 != count {
		return nil, &mbserver.IllegalDataAddress
	}
	for i, value := range mbserver.BytesToUint16(valueBytes) {
		if exception := ep.write(
			frame.GetDeviceId(),
			uint16(addr+i),
			value,
		); exception != &mbserver.Success {
			return nil, exception
		}
	}
	return frame.GetData()[0:4], &mbserver.Success
}

func (ep *endpoint) write(slaveID uint8, addr, value uint16) *mbserver.Exception {
	bank, ok := ep.banks[slaveID]
	if !ok {
		return &mbserver.GatewayTargetDeviceFailedtoRespond
	}
	if bank.Holding.UpdateUint16Data(addr, value) != 1 {
		return &mbserver.IllegalDataAddress
	}

	switch {
	case slaveID == ep.config.BAUSlaveID:
		ep.forwardBAUControl(addr, value)
	case addr == xnBCUFaultReset && value == 1:
		_ = ep.sim.WriteHoldingExternal(
			ep.battery.BMSSlaveID(),
			simulator.RegBMSFaultReset,
			1,
		)
	}
	return &mbserver.Success
}

func (ep *endpoint) forwardBAUControl(addr, value uint16) {
	var register uint16
	switch {
	case addr == xnBAUFaultReset && value == 1:
		register = simulator.RegBMSFaultReset
	case addr == xnBAUHVControl && value == 1:
		register = simulator.RegBMSCloseHV
	case addr == xnBAUHVControl && value == 2:
		register = simulator.RegBMSOpenHV
	default:
		return
	}
	_ = ep.sim.WriteHoldingExternal(ep.battery.BMSSlaveID(), register, 1)
}
