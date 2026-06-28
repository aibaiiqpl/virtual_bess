package simulator

import "math"

// TempHumid 温湿度虚拟设备。把温度/湿度写到 holding 寄存器 0(温度)/1(湿度)，
// S16 ×0.1，由 emu 设备级点表 th.csv 读取并投影到北向 27000+（二级 EMS map）。
// 在基准值上叠加缓慢正弦波动，便于在 Web 页观察 5s 轮询的实时刷新。
type TempHumid struct {
	bank      *SlaveBank
	baseTemp  float64
	baseHumid float64
	phase     float64 // 累计仿真时间（秒），驱动正弦相位
	curTemp   float64
	curHumid  float64
}

func NewTempHumid(cfg THConfig, bank *SlaveBank) *TempHumid {
	return &TempHumid{
		bank:      bank,
		baseTemp:  cfg.Temperature,
		baseHumid: cfg.Humidity,
		curTemp:   cfg.Temperature,
		curHumid:  cfg.Humidity,
	}
}

// Update 推进仿真：在基准值上叠加缓慢正弦波动（温度 ±0.5℃ / 湿度 ±2%）。
func (th *TempHumid) Update(dt float64) {
	th.phase += dt
	th.curTemp = th.baseTemp + math.Sin(th.phase/30.0)*0.5
	th.curHumid = th.baseHumid + math.Sin(th.phase/45.0)*2.0
}

// Sync 写温湿度 YC 到 holding 寄存器（S16 ×0.1）。
func (th *TempHumid) Sync() {
	th.bank.WriteU16(0, uint16(int16(math.Round(th.curTemp*10.0))))
	th.bank.WriteU16(1, uint16(int16(math.Round(th.curHumid*10.0))))
}
