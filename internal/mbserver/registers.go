package mbserver

import (
	"sync"
)

type Registers struct {
	regs []uint16
	size uint32
	mu   sync.Mutex
}

func NewRegisters(data []uint16) *Registers {
	if len(data) > 65536 {
		panic("registers size must be small than 65536")
	}
	size := len(data)
	return &Registers{
		regs: data,
		size: uint32(size),
	}
}

func (r *Registers) UpdateUint16Data(addr uint16, values ...uint16) int {
	if len(values) == 0 {
		return 0
	}

	if addr < 0 || uint32(addr+uint16(len(values))) > r.size {
		return 0
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	return copy(r.regs[addr:], values)
}

func (r *Registers) UpdateUint32Data(addr uint16, v uint32) {
	if addr < 0 || uint32(addr+1) > r.size {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.regs[addr] = uint16(v >> 16)
	r.regs[addr+1] = uint16(v & 0xffff)
}

func (r *Registers) GetData(addr, cnt uint16) ([]uint16, *Exception) {
	if addr < 0 || uint32(addr+cnt) > r.size {
		return nil, &IllegalDataAddress
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	data := make([]uint16, cnt)
	copy(data, r.regs[addr:addr+cnt])

	return data, nil
}
