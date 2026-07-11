package main

// 本文件定义博最 BCM CAN 协议的"去复用"帧布局与编解码。
//
// 背景：真实博最协议（can-bms/docs/EMS-BMS接口通讯协议A0）在 0x35/0x36 等汇总帧里
// 用 byte1 作"包序号"复用同一个 CAN ID（同一 ID 不同包携带不同字段）。而 emu-rs 的
// CAN 南向解码器只按 (CAN ID, 字节偏移) 索引，无法区分包序号。因此本模拟器采用与
// emu-go/docs/csv-device-level.md § 四 一致的"去复用"设计：每个信号占用唯一的
// (CAN ID, 字节偏移)，每个 CAN ID 承载固定单一布局。
//
// 帧方向沿用博最寻址约定：BCM(源 0xE8) → EMS(目标 0xF4)，扩展帧 ID 形如 0x18{code}F4E8；
// 命令帧 EMS → BCM 为 0x18{code}E8F4。编码统一 MOTOROLA 大端（高字节在前），与协议一致。
//
// 每个信号的 CAN 原始整数 = 北向 Modbus 寄存器整数（源系数=目标系数、偏移=0 时
// register = raw，见点表 CSV），因此直接复用 BMS bank 已算好的寄存器值打包。

const canEFFFlag = 0x8000_0000 // CAN 扩展帧标志位（对齐 linux/can.h CAN_EFF_FLAG）

// BCM → EMS 数据帧 ID（去复用布局，code 取协议未使用的 0x50~0x59 段）。
const (
	canIDStatus    = 0x1850F4E8 // 故障/告警/系统状态/禁充
	canIDStatus2   = 0x1851F4E8 // 禁放
	canIDSocEnergy = 0x1852F4E8 // SOC/SOH/剩余充放电量
	canIDVIP       = 0x1853F4E8 // 总电压/总电流/总功率
	canIDTotalE    = 0x1854F4E8 // 累计充电量/累计放电量（各 U32）
	canIDLimits    = 0x1855F4E8 // 最大允许充/放功率、充/放电流
	canIDCellV     = 0x1856F4E8 // 单体最高/最低/平均电压、压差
	canIDCellVIdx  = 0x1857F4E8 // 单体最高/最低电压编号
	canIDCellT     = 0x1858F4E8 // 单体最高/最低/平均温度、温差
	canIDCellTIdx  = 0x1859F4E8 // 单体最高/最低温度编号
)

// EMS → BCM 命令帧（对齐博最 0x81 控制帧 0x1881E8F4）。
const canIDCommand = 0x1881E8F4

// canFrame 是一帧待发送的经典 CAN 报文（扩展帧，DLC=8）。
type canFrame struct {
	id   uint32 // 不含 EFF 标志，socket 层再或上标志
	data [8]byte
}

// bmsRegReader 抽象 BMS 寄存器读取，SlaveBank 已实现 ReadU16。
type bmsRegReader interface {
	ReadU16(addr uint16) uint16
}

// putBE16 把一个 16 位寄存器值按大端写入 data[off:off+2]。
func putBE16(data *[8]byte, off int, v uint16) {
	data[off] = byte(v >> 8)
	data[off+1] = byte(v & 0xFF)
}

// buildBMSFrames 从 BMS bank 读取标准点寄存器，打包成 10 帧去复用 CAN 报文。
// 每个信号的字节偏移与点表 CSV 的"源字节偏移"逐一对应，修改此处必须同步 CSV。
func buildBMSFrames(r bmsRegReader) []canFrame {
	frames := make([]canFrame, 0, 10)

	// 0x1850：故障 / 告警 / 系统状态 / 禁充
	var status canFrame
	status.id = canIDStatus
	putBE16(&status.data, 0, r.ReadU16(RegBMSFaultStatus))
	putBE16(&status.data, 2, r.ReadU16(RegBMSAlarmStatus))
	putBE16(&status.data, 4, r.ReadU16(RegBMSSysStatus))
	putBE16(&status.data, 6, r.ReadU16(RegBMSChargeForbid))
	frames = append(frames, status)

	// 0x1851：禁放
	var status2 canFrame
	status2.id = canIDStatus2
	putBE16(&status2.data, 0, r.ReadU16(RegBMSDischargeForbid))
	frames = append(frames, status2)

	// 0x1852：SOC / SOH / 剩余充电量 / 剩余放电量
	var socEnergy canFrame
	socEnergy.id = canIDSocEnergy
	putBE16(&socEnergy.data, 0, r.ReadU16(RegBMSSOC))
	putBE16(&socEnergy.data, 2, r.ReadU16(RegBMSSOH))
	putBE16(&socEnergy.data, 4, r.ReadU16(RegBMSRemainCharge))
	putBE16(&socEnergy.data, 6, r.ReadU16(RegBMSRemainDischarge))
	frames = append(frames, socEnergy)

	// 0x1853：总电压 / 总电流 / 总功率
	var vip canFrame
	vip.id = canIDVIP
	putBE16(&vip.data, 0, r.ReadU16(RegBMSVoltage))
	putBE16(&vip.data, 2, r.ReadU16(RegBMSCurrent))
	putBE16(&vip.data, 4, r.ReadU16(RegBMSPower))
	frames = append(frames, vip)

	// 0x1854：累计充电量（U32 高字 40112 + 低字 40113）/ 累计放电量（40114 + 40115）
	var totalE canFrame
	totalE.id = canIDTotalE
	putBE16(&totalE.data, 0, r.ReadU16(RegBMSTotalCharge))
	putBE16(&totalE.data, 2, r.ReadU16(RegBMSTotalCharge+1))
	putBE16(&totalE.data, 4, r.ReadU16(RegBMSTotalDischarge))
	putBE16(&totalE.data, 6, r.ReadU16(RegBMSTotalDischarge+1))
	frames = append(frames, totalE)

	// 0x1855：最大允许充/放功率、充/放电流
	var limits canFrame
	limits.id = canIDLimits
	putBE16(&limits.data, 0, r.ReadU16(RegBMSMaxChargePW))
	putBE16(&limits.data, 2, r.ReadU16(RegBMSMaxDischargePW))
	putBE16(&limits.data, 4, r.ReadU16(RegBMSMaxChargeI))
	putBE16(&limits.data, 6, r.ReadU16(RegBMSMaxDischargeI))
	frames = append(frames, limits)

	// 0x1856：单体最高/最低/平均电压、压差
	var cellV canFrame
	cellV.id = canIDCellV
	putBE16(&cellV.data, 0, r.ReadU16(RegBMSCellVMax))
	putBE16(&cellV.data, 2, r.ReadU16(RegBMSCellVMin))
	putBE16(&cellV.data, 4, r.ReadU16(RegBMSCellVAvg))
	putBE16(&cellV.data, 6, r.ReadU16(RegBMSCellVSpread))
	frames = append(frames, cellV)

	// 0x1857：单体最高/最低电压编号
	var cellVIdx canFrame
	cellVIdx.id = canIDCellVIdx
	putBE16(&cellVIdx.data, 0, r.ReadU16(RegBMSCellVMaxIdx))
	putBE16(&cellVIdx.data, 2, r.ReadU16(RegBMSCellVMinIdx))
	frames = append(frames, cellVIdx)

	// 0x1858：单体最高/最低/平均温度、温差
	var cellT canFrame
	cellT.id = canIDCellT
	putBE16(&cellT.data, 0, r.ReadU16(RegBMSCellTMax))
	putBE16(&cellT.data, 2, r.ReadU16(RegBMSCellTMin))
	putBE16(&cellT.data, 4, r.ReadU16(RegBMSCellTAvg))
	putBE16(&cellT.data, 6, r.ReadU16(RegBMSCellTSpread))
	frames = append(frames, cellT)

	// 0x1859：单体最高/最低温度编号
	var cellTIdx canFrame
	cellTIdx.id = canIDCellTIdx
	putBE16(&cellTIdx.data, 0, r.ReadU16(RegBMSCellTMaxIdx))
	putBE16(&cellTIdx.data, 2, r.ReadU16(RegBMSCellTMinIdx))
	frames = append(frames, cellTIdx)

	return frames
}

// decodeBMSCommand 解析 EMS → BCM 命令帧，返回要写入的 BMS 控制寄存器与值。
// 编码约定对齐点表 dzPoint（emu-rs 把"写定值"写到指定字节偏移）：
//   - data[5]==0x55 → 故障复位（对齐博最 0x81 byte6=0x55 软复位）
//   - data[1]==3    → 上高压（合闸，对齐 can-bms bms_bozui.c on 序列 data[1]=3）
//   - data[0]==1    → 下高压（分闸）
//
// 返回 ok=false 表示该帧不是可识别命令。
func decodeBMSCommand(id uint32, data []byte) (reg uint16, val uint16, ok bool) {
	if id != canIDCommand || len(data) < 8 {
		return 0, 0, false
	}
	switch {
	case data[5] == 0x55:
		return RegBMSFaultReset, 1, true
	case data[1] == 3:
		return RegBMSCloseHV, 1, true
	case data[0] == 1:
		return RegBMSOpenHV, 1, true
	default:
		return 0, 0, false
	}
}
