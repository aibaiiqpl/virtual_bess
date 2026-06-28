package simulator

import "virtual_bess/internal/mbserver"

// SlaveBank 持有单个 slaveId 的寄存器存储。
// Holding 用于 FC 3/6/16；Input 用于 FC 4，按需创建（BMS 簇用）。
// 底层复用 mbserver.Registers，自带 sync.Mutex，可被 modbus handler
// 和 Tick 循环并发访问。
type SlaveBank struct {
	SlaveID uint8
	Holding *mbserver.Registers
	Input   *mbserver.Registers
}

// NewSlaveBank 创建一个新的 SlaveBank。
// withInput=true 时同时创建 Input 寄存器（BMS 簇 IR 需要）。
func NewSlaveBank(slaveID uint8, withInput bool) *SlaveBank {
	b := &SlaveBank{
		SlaveID: slaveID,
		Holding: mbserver.NewRegisters(make([]uint16, 65536)),
	}
	if withInput {
		b.Input = mbserver.NewRegisters(make([]uint16, 65536))
	}
	return b
}

// WriteU16 写单个 holding 寄存器。
func (b *SlaveBank) WriteU16(addr, v uint16) {
	b.Holding.UpdateUint16Data(addr, v)
}

// WriteU32 写一对 holding 寄存器（高位在前）。
func (b *SlaveBank) WriteU32(addr uint16, v uint32) {
	b.Holding.UpdateUint32Data(addr, v)
}

// WriteS32 写一对有符号 32 位的 holding 寄存器（高位在前）。
func (b *SlaveBank) WriteS32(addr uint16, v int32) {
	b.Holding.UpdateUint32Data(addr, uint32(v))
}

// ReadU16 读单个 holding 寄存器，地址非法返回 0。
func (b *SlaveBank) ReadU16(addr uint16) uint16 {
	d, _ := b.Holding.GetData(addr, 1)
	if len(d) == 0 {
		return 0
	}
	return d[0]
}

// WriteInputU16 写单个 input 寄存器。
func (b *SlaveBank) WriteInputU16(addr, v uint16) {
	b.Input.UpdateUint16Data(addr, v)
}

// WriteInputU32 写一对 input 寄存器（高位在前）。
func (b *SlaveBank) WriteInputU32(addr uint16, v uint32) {
	b.Input.UpdateUint32Data(addr, v)
}

// WriteInputS32 写一对有符号 32 位的 input 寄存器（高位在前）。
func (b *SlaveBank) WriteInputS32(addr uint16, v int32) {
	b.Input.UpdateUint32Data(addr, uint32(v))
}
