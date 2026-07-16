package simulator

import (
	"math"
)

type airConditionerKind uint8

const (
	airConditionerAC airConditionerKind = iota
	airConditionerLC
)

const (
	climateStopped uint16 = iota
	climateCooling
	climateHeating
)

// AirConditioner 以温度设定值和回差驱动简化热工状态机。每个实例独占一个 Modbus slave，
// 使同型号的多台空调可以分别映射到 EMU-V2.0 的 AC/LC 地址块。
type AirConditioner struct {
	kind airConditionerKind
	bank *SlaveBank

	enabled             bool
	status              uint16
	coolingSetpoint     float64
	heatingSetpoint     float64
	coolingDifferential float64
	heatingDifferential float64
	indoorTemperature   float64
	ambientTemperature  float64
	phase               float64
}

func NewAirConditioner(kind airConditionerKind, cfg AirConditionerConfig, bank *SlaveBank) *AirConditioner {
	return &AirConditioner{
		kind: kind, bank: bank,
		coolingSetpoint: cfg.CoolingSetpoint, heatingSetpoint: cfg.HeatingSetpoint,
		coolingDifferential: cfg.CoolingDifferential, heatingDifferential: cfg.HeatingDifferential,
		indoorTemperature: cfg.IndoorTemperature, ambientTemperature: cfg.AmbientTemperature,
	}
}

// OnWrite 接收真实设备点表定义的源寄存器写入，而不是 EMU 北向标准地址。
func (ac *AirConditioner) OnWrite(addr, value uint16) {
	if ac.kind == airConditionerAC {
		ac.onACWrite(addr, value)
		return
	}
	ac.onLCWrite(addr, value)
}

func (ac *AirConditioner) onACWrite(addr, value uint16) {
	v := float64(uint16ToInt16(value)) / 10
	switch addr {
	case 0x100:
		ac.coolingSetpoint = v
	case 0x101:
		ac.coolingDifferential = positiveSetting(v, ac.coolingDifferential)
	case 0x104:
		ac.heatingSetpoint = v
	case 0x105:
		ac.heatingDifferential = positiveSetting(v, ac.heatingDifferential)
	case 0x106:
		ac.enabled = value == 1
	}
}

func (ac *AirConditioner) onLCWrite(addr, value uint16) {
	switch addr {
	case 0x1003:
		ac.coolingSetpoint = float64(uint16ToInt16(value))
	case 0x1004:
		ac.coolingDifferential = positiveSetting(float64(value), ac.coolingDifferential)
	case 0x1005:
		ac.heatingSetpoint = float64(uint16ToInt16(value))
	case 0x1006:
		ac.heatingDifferential = positiveSetting(float64(value), ac.heatingDifferential)
	case 0x300:
		switch value {
		case 1:
			ac.enabled = true
		case 2:
			ac.enabled = false
		case 4: // 模拟器没有可注入故障；复位后维持当前运行条件。
			ac.status = climateStopped
		}
	}
}

func positiveSetting(value, fallback float64) float64 {
	if value > 0 {
		return value
	}
	return fallback
}

// Update 按温度回差决定制冷、制热或待机，并让室温缓慢趋向对应热源。
func (ac *AirConditioner) Update(dt float64) {
	ac.phase += dt
	ambient := ac.ambientTemperature + math.Sin(ac.phase/90)*1.5
	ac.updateStatus()
	target := ambient
	switch ac.status {
	case climateCooling:
		target = ac.coolingSetpoint - ac.coolingDifferential/2
	case climateHeating:
		target = ac.heatingSetpoint + ac.heatingDifferential/2
	}
	alpha := 1 - math.Exp(-math.Max(dt, 0)/90)
	ac.indoorTemperature += (target - ac.indoorTemperature) * alpha
}

func (ac *AirConditioner) updateStatus() {
	if !ac.enabled {
		ac.status = climateStopped
		return
	}
	switch ac.status {
	case climateCooling:
		if ac.indoorTemperature <= ac.coolingSetpoint-ac.coolingDifferential {
			ac.status = climateStopped
		}
	case climateHeating:
		if ac.indoorTemperature >= ac.heatingSetpoint+ac.heatingDifferential {
			ac.status = climateStopped
		}
	default:
		if ac.indoorTemperature >= ac.coolingSetpoint {
			ac.status = climateCooling
		} else if ac.indoorTemperature <= ac.heatingSetpoint {
			ac.status = climateHeating
		}
	}
}

func (ac *AirConditioner) Sync() {
	if ac.kind == airConditionerAC {
		ac.syncAC()
		return
	}
	ac.syncLC()
}

func (ac *AirConditioner) syncAC() {
	ac.bank.WriteU16(0x100, signedTenths(ac.coolingSetpoint))
	ac.bank.WriteU16(0x101, signedTenths(ac.coolingDifferential))
	ac.bank.WriteU16(0x104, signedTenths(ac.heatingSetpoint))
	ac.bank.WriteU16(0x105, signedTenths(ac.heatingDifferential))
	ac.bank.WriteU16(0x106, boolToU16(ac.enabled))
	ac.bank.WriteU16(0x10B, signedTenths(ac.indoorTemperature))
	ac.bank.WriteU16(0x10C, signedTenths(ac.indoorTemperature-4))
	ac.bank.WriteU16(0x10D, signedTenths(ac.ambientTemperature))
	ac.bank.WriteU16(0x10E, signedTenths(ac.indoorTemperature+8))
	ac.bank.WriteU16(0x10F, signedTenths(ac.indoorTemperature+12))
	ac.bank.WriteU16(0x116, ac.acStatus())
}

func (ac *AirConditioner) syncLC() {
	supply, returned := ac.waterTemperatures()
	ac.bank.WriteU16(0x0000, ac.lcStatus()<<8)
	ac.bank.WriteU16(0x0003, signedTenths(supply))
	ac.bank.WriteU16(0x0004, signedTenths(returned))
	ac.bank.WriteU16(0x0008, signedTenths(ac.indoorTemperature))
	ac.bank.WriteU16(0x0012, uint16(2200)) // 220.0 kPa，源侧系数 0.1。
	ac.bank.WriteU16(0x0013, uint16(1800)) // 180.0 kPa，源侧系数 0.1。
	ac.bank.WriteU16(0x1003, uint16(math.Round(ac.coolingSetpoint)))
	ac.bank.WriteU16(0x1004, uint16(math.Round(ac.coolingDifferential)))
	ac.bank.WriteU16(0x1005, uint16(math.Round(ac.heatingSetpoint)))
	ac.bank.WriteU16(0x1006, uint16(math.Round(ac.heatingDifferential)))
}

func (ac *AirConditioner) acStatus() uint16 {
	switch ac.status {
	case climateCooling:
		return 2
	case climateHeating:
		return 3
	default:
		return 0
	}
}

func (ac *AirConditioner) lcStatus() uint16 {
	if !ac.enabled {
		return 0
	}
	return ac.status
}

func (ac *AirConditioner) waterTemperatures() (float64, float64) {
	switch ac.status {
	case climateCooling:
		return ac.indoorTemperature - 6, ac.indoorTemperature - 3
	case climateHeating:
		return ac.indoorTemperature + 6, ac.indoorTemperature + 3
	default:
		return ac.indoorTemperature, ac.indoorTemperature
	}
}

func signedTenths(value float64) uint16 {
	return uint16(int16(math.Round(value * 10)))
}
