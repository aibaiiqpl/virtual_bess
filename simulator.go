package main

import (
	"sync"
	"time"

	"aiwatt.net/ems/go-common/mbserver"
	"aiwatt.net/ems/go-common/zaplog"
)

// meterAgg 描述一个电表的聚合源（按 slave_id / load name 预解析为索引）。
type meterAgg struct {
	meter   *Meter
	isMain  bool
	outflow bool // true = forward 方向为从设备回流到电网（PV 发电为正）
	pcsIdx  []int
	pvIdx   []int
	loadIdx []int
}

// Simulator 顶层调度器，持有所有 unit 和 slave 寄存器。
type Simulator struct {
	mu     sync.Mutex
	server *mbserver.Server

	banks         map[uint8]*SlaveBank
	writeHandlers map[uint8]func(addr, value uint16)

	batteries []*BatteryUnit
	pvs       []*PVUnit
	meters    []*meterAgg
	loads     []*Load
	weather   *Weather

	gridVoltage float64

	lastTick time.Time
	nowFunc  func() time.Time
}

// NewSimulator 按配置创建 Simulator，注册 modbus 处理器，初始化所有 unit
// 和寄存器默认值，最后同步一次寄存器。
func NewSimulator(cfg *Config, server *mbserver.Server) *Simulator {
	now := time.Now()
	sim := &Simulator{
		server:        server,
		banks:         make(map[uint8]*SlaveBank),
		writeHandlers: make(map[uint8]func(addr, value uint16)),
		gridVoltage:   cfg.Grid.Voltage,
		lastTick:      now,
		nowFunc:       time.Now,
	}

	// 注册 modbus 函数处理器（覆盖 mbserver 默认行为）。
	server.RegisterFunctionHandler(3, sim.handleReadHolding)
	server.RegisterFunctionHandler(4, sim.handleReadInput)
	server.RegisterFunctionHandler(6, sim.handleWriteSingleHolding)
	server.RegisterFunctionHandler(16, sim.handleWriteMultipleHolding)

	// 创建共享子系统。
	sim.weather = NewWeather()
	for _, ldCfg := range cfg.Loads {
		sim.loads = append(sim.loads, NewLoad(ldCfg.Name, ldCfg.RatedPowerKW))
	}

	// 创建 N 套电池单元。
	for _, buCfg := range cfg.BatteryUnits {
		pcsBank := NewSlaveBank(buCfg.PCSSlaveID, false)
		bmsBank := NewSlaveBank(buCfg.BMSSlaveID, true)
		sim.banks[buCfg.PCSSlaveID] = pcsBank
		sim.banks[buCfg.BMSSlaveID] = bmsBank

		bu := NewBatteryUnit(buCfg, cfg.PCS.ACVoltage, pcsBank, bmsBank)
		sim.batteries = append(sim.batteries, bu)

		sim.writeHandlers[buCfg.PCSSlaveID] = bu.OnPCSWrite
		sim.writeHandlers[buCfg.BMSSlaveID] = bu.OnBMSWrite
	}

	// 创建 M 套 PV。
	for _, pvCfg := range cfg.PVUnits {
		bank := NewSlaveBank(pvCfg.SlaveID, false)
		sim.banks[pvCfg.SlaveID] = bank
		pv := NewPVUnit(pvCfg, cfg.PCS.ACVoltage, bank)
		sim.pvs = append(sim.pvs, pv)
		sim.writeHandlers[pvCfg.SlaveID] = pv.OnPVWrite
	}

	// 创建 N 块电表（主关口 + 子电表）。
	pcsIdxByID := map[uint8]int{}
	for i, bu := range sim.batteries {
		pcsIdxByID[cfg.BatteryUnits[i].PCSSlaveID] = i
		_ = bu
	}
	pvIdxByID := map[uint8]int{}
	for i := range sim.pvs {
		pvIdxByID[cfg.PVUnits[i].SlaveID] = i
	}
	loadIdxByName := map[string]int{}
	for i, ld := range sim.loads {
		loadIdxByName[ld.Name()] = i
	}

	for _, mCfg := range cfg.Meters {
		bank := NewSlaveBank(mCfg.SlaveID, false)
		sim.banks[mCfg.SlaveID] = bank
		agg := &meterAgg{
			meter:   NewMeter(mCfg, sim.gridVoltage, bank),
			isMain:  mCfg.IsMain,
			outflow: mCfg.FlowDirection == "outflow",
		}
		if mCfg.IsMain {
			for i := range sim.batteries {
				agg.pcsIdx = append(agg.pcsIdx, i)
			}
			for i := range sim.pvs {
				agg.pvIdx = append(agg.pvIdx, i)
			}
			for i := range sim.loads {
				agg.loadIdx = append(agg.loadIdx, i)
			}
		} else {
			for _, id := range mCfg.PCSSlaveIDs {
				agg.pcsIdx = append(agg.pcsIdx, pcsIdxByID[id])
			}
			for _, id := range mCfg.PVSlaveIDs {
				agg.pvIdx = append(agg.pvIdx, pvIdxByID[id])
			}
			for _, n := range mCfg.LoadNames {
				agg.loadIdx = append(agg.loadIdx, loadIdxByName[n])
			}
		}
		sim.meters = append(sim.meters, agg)
	}

	// 初始化：跑一次 weather/load/PV/meter 同步，让寄存器有初值。
	sim.weather.Update(0)
	for _, ld := range sim.loads {
		ld.Update(now)
	}
	for _, pv := range sim.pvs {
		pv.UpdateSimulation(now, 0, sim.weather.Coeff())
	}
	sim.updateMeters(0)
	sim.syncAll()
	return sim
}

// Tick 推进一秒仿真。
func (sim *Simulator) Tick() {
	sim.mu.Lock()
	defer sim.mu.Unlock()

	now := sim.nowFunc()
	dt := now.Sub(sim.lastTick).Seconds()
	sim.lastTick = now

	sim.weather.Update(dt)
	for _, ld := range sim.loads {
		ld.Update(now)
	}

	for _, bu := range sim.batteries {
		bu.ProcessBMSControls()
		bu.ProcessPCSControls()
		bu.ProcessPowerCommand()
		bu.UpdateEnergy(dt)
	}

	weatherCoeff := sim.weather.Coeff()
	for _, pv := range sim.pvs {
		pv.ProcessControls()
		pv.UpdateSimulation(now, dt, weatherCoeff)
	}

	sim.updateMeters(dt)
	sim.syncAll()
}

func (sim *Simulator) updateMeters(dt float64) {
	for _, agg := range sim.meters {
		var pcs, pv, load float64
		for _, i := range agg.pcsIdx {
			pcs += sim.batteries[i].ActualPowerKW()
		}
		for _, i := range agg.pvIdx {
			pv += sim.pvs[i].ActualPowerKW()
		}
		for _, i := range agg.loadIdx {
			load += sim.loads[i].ActualPowerKW()
		}
		if agg.outflow {
			// 翻转方向：发电/放电变为 forward
			agg.meter.Update(dt, -load, -pcs, -pv)
		} else {
			agg.meter.Update(dt, load, pcs, pv)
		}
	}
}

func (sim *Simulator) syncAll() {
	for _, bu := range sim.batteries {
		bu.Sync()
	}
	for _, pv := range sim.pvs {
		pv.Sync()
	}
	for _, agg := range sim.meters {
		agg.meter.Sync()
	}
}

func (sim *Simulator) writeHolding(slaveID uint8, register, value uint16) error {
	sim.mu.Lock()
	defer sim.mu.Unlock()

	bank, ok := sim.banks[slaveID]
	if !ok || bank.Holding == nil {
		return errUnknownSlaveID(slaveID)
	}
	bank.Holding.UpdateUint16Data(register, value)
	if cb, ok := sim.writeHandlers[slaveID]; ok {
		cb(register, value)
	}
	return nil
}

// ---- Modbus 函数处理器 ----

func (sim *Simulator) handleReadHolding(_ *mbserver.Server, frame mbserver.Framer) ([]byte, *mbserver.Exception) {
	bank, ok := sim.banks[frame.GetDeviceId()]
	if !ok || bank.Holding == nil {
		zaplog.Debugf("[MB-SVR] slave %d not found (FC3)", frame.GetDeviceId())
		return nil, &mbserver.GatewayTargetDeviceFailedtoRespond
	}
	addr, num, _ := mbserver.RegisterAddressAndNumber(frame)
	data, exc := bank.Holding.GetData(uint16(addr), uint16(num))
	if exc != nil && exc != &mbserver.Success {
		return nil, exc
	}
	return append([]byte{byte(num * 2)}, mbserver.Uint16ToBytes(data)...), &mbserver.Success
}

func (sim *Simulator) handleReadInput(_ *mbserver.Server, frame mbserver.Framer) ([]byte, *mbserver.Exception) {
	bank, ok := sim.banks[frame.GetDeviceId()]
	if !ok || bank.Input == nil {
		zaplog.Debugf("[MB-SVR] slave %d input not found (FC4)", frame.GetDeviceId())
		return nil, &mbserver.GatewayTargetDeviceFailedtoRespond
	}
	addr, num, _ := mbserver.RegisterAddressAndNumber(frame)
	data, exc := bank.Input.GetData(uint16(addr), uint16(num))
	if exc != nil && exc != &mbserver.Success {
		return nil, exc
	}
	return append([]byte{byte(num * 2)}, mbserver.Uint16ToBytes(data)...), &mbserver.Success
}

func (sim *Simulator) handleWriteSingleHolding(_ *mbserver.Server, frame mbserver.Framer) ([]byte, *mbserver.Exception) {
	sim.mu.Lock()
	defer sim.mu.Unlock()

	slaveID := frame.GetDeviceId()
	bank, ok := sim.banks[slaveID]
	if !ok || bank.Holding == nil {
		return nil, &mbserver.GatewayTargetDeviceFailedtoRespond
	}
	register, value := mbserver.RegisterAddressAndValue(frame)
	bank.Holding.UpdateUint16Data(uint16(register), value)
	if cb, ok := sim.writeHandlers[slaveID]; ok {
		cb(uint16(register), value)
	}
	return frame.GetData()[0:4], &mbserver.Success
}

func (sim *Simulator) handleWriteMultipleHolding(_ *mbserver.Server, frame mbserver.Framer) ([]byte, *mbserver.Exception) {
	sim.mu.Lock()
	defer sim.mu.Unlock()

	slaveID := frame.GetDeviceId()
	bank, ok := sim.banks[slaveID]
	if !ok || bank.Holding == nil {
		return nil, &mbserver.GatewayTargetDeviceFailedtoRespond
	}
	register, numRegs, _ := mbserver.RegisterAddressAndNumber(frame)
	valueBytes := frame.GetData()[5:]
	if len(valueBytes)/2 != numRegs {
		return nil, &mbserver.IllegalDataAddress
	}
	values := mbserver.BytesToUint16(valueBytes)
	updated := bank.Holding.UpdateUint16Data(uint16(register), values...)
	if updated != numRegs {
		return nil, &mbserver.IllegalDataAddress
	}
	if cb, ok := sim.writeHandlers[slaveID]; ok {
		for i, v := range values {
			cb(uint16(register+i), v)
		}
	}
	return frame.GetData()[0:4], &mbserver.Success
}
