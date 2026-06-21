package main

// Fire 消防遥信虚拟设备。把 4 个遥信值写到 holding 寄存器 0..3，
// 由 emu 设备级点表 fire.csv 读取并投影到北向 22000-22003（二级 EMS map）。
// 遥信值为静态配置（由 config.yaml 给定），便于联调时手动切换正常/告警态。
type Fire struct {
	bank *SlaveBank
	cfg  FireConfig
}

func NewFire(cfg FireConfig, bank *SlaveBank) *Fire {
	return &Fire{bank: bank, cfg: cfg}
}

// Sync 写消防遥信到 holding 寄存器（源地址 0/1/2/3）。
func (f *Fire) Sync() {
	f.bank.WriteU16(0, f.cfg.FireSystem)
	f.bank.WriteU16(1, f.cfg.Smoke)
	f.bank.WriteU16(2, f.cfg.Gas)
	f.bank.WriteU16(3, f.cfg.FireDevice)
}
