package main

// CAN 帧编解码单元测试：验证寄存器值按大端正确落到指定 (CAN ID, 字节偏移)，
// 以及命令帧解码。字节偏移必须与点表 CSV 逐一对齐。

import "testing"

// fakeBank 用固定 map 模拟 BMS bank 寄存器读取。
type fakeBank map[uint16]uint16

func (f fakeBank) ReadU16(addr uint16) uint16 { return f[addr] }

func findFrame(frames []canFrame, id uint32) (canFrame, bool) {
	for _, fr := range frames {
		if fr.id == id {
			return fr, true
		}
	}
	return canFrame{}, false
}

func be16(f canFrame, off int) uint16 {
	return uint16(f.data[off])<<8 | uint16(f.data[off+1])
}

func TestBuildBMSFrames(t *testing.T) {
	bank := fakeBank{
		RegBMSFaultStatus:        1,
		RegBMSAlarmStatus:        0,
		RegBMSSysStatus:          3,
		RegBMSChargeForbid:       0,
		RegBMSDischargeForbid:    1,
		RegBMSSOC:                555,  // 55.5%
		RegBMSSOH:                1000, // 100.0%
		RegBMSRemainCharge:       1234,
		RegBMSRemainDischarge:    5678,
		RegBMSVoltage:            12240, // 1224.0V
		RegBMSCurrent:            int16ToUint16(-320),
		RegBMSPower:              int16ToUint16(-500),
		RegBMSTotalCharge:        0x0001,
		RegBMSTotalCharge + 1:    0x86A0, // 0x000186A0 = 100000
		RegBMSTotalDischarge:     0x0000,
		RegBMSTotalDischarge + 1: 0x2710, // 10000
		RegBMSMaxChargePW:        25000,
		RegBMSMaxDischargePW:     25000,
		RegBMSMaxChargeI:         2040,
		RegBMSMaxDischargeI:      2040,
		RegBMSCellVMax:           3320,
		RegBMSCellVMin:           3280,
		RegBMSCellVAvg:           3300,
		RegBMSCellVSpread:        40,
		RegBMSCellVMaxIdx:        12,
		RegBMSCellVMinIdx:        7,
		RegBMSCellTMax:           int16ToUint16(320),
		RegBMSCellTMin:           int16ToUint16(280),
		RegBMSCellTAvg:           int16ToUint16(300),
		RegBMSCellTSpread:        40,
		RegBMSCellTMaxIdx:        5,
		RegBMSCellTMinIdx:        9,
	}

	frames := buildBMSFrames(bank)
	if len(frames) != 10 {
		t.Fatalf("expected 10 frames, got %d", len(frames))
	}

	cases := []struct {
		id   uint32
		off  int
		want uint16
	}{
		{canIDStatus, 0, 1},
		{canIDStatus, 4, 3},
		{canIDStatus, 6, 0},
		{canIDStatus2, 0, 1},
		{canIDSocEnergy, 0, 555},
		{canIDSocEnergy, 2, 1000},
		{canIDSocEnergy, 4, 1234},
		{canIDSocEnergy, 6, 5678},
		{canIDVIP, 0, 12240},
		{canIDVIP, 2, int16ToUint16(-320)},
		{canIDVIP, 4, int16ToUint16(-500)},
		{canIDTotalE, 0, 0x0001},
		{canIDTotalE, 2, 0x86A0},
		{canIDTotalE, 4, 0x0000},
		{canIDTotalE, 6, 0x2710},
		{canIDLimits, 0, 25000},
		{canIDLimits, 4, 2040},
		{canIDCellV, 0, 3320},
		{canIDCellV, 2, 3280},
		{canIDCellV, 4, 3300},
		{canIDCellV, 6, 40},
		{canIDCellVIdx, 0, 12},
		{canIDCellVIdx, 2, 7},
		{canIDCellT, 0, int16ToUint16(320)},
		{canIDCellT, 2, int16ToUint16(280)},
		{canIDCellTIdx, 0, 5},
		{canIDCellTIdx, 2, 9},
	}
	for _, c := range cases {
		fr, ok := findFrame(frames, c.id)
		if !ok {
			t.Errorf("frame 0x%X missing", c.id)
			continue
		}
		if got := be16(fr, c.off); got != c.want {
			t.Errorf("frame 0x%X off %d: got %d want %d", c.id, c.off, got, c.want)
		}
	}
}

func TestDecodeBMSCommand(t *testing.T) {
	cases := []struct {
		name    string
		data    []byte
		wantReg uint16
		wantVal uint16
		wantOK  bool
	}{
		{"reset", []byte{0, 0, 0, 0, 0, 0x55, 0, 0}, RegBMSFaultReset, 1, true},
		{"on", []byte{0, 3, 0, 3, 0, 0, 0, 0}, RegBMSCloseHV, 1, true},
		{"off", []byte{1, 0, 0, 0, 0, 0, 0, 0}, RegBMSOpenHV, 1, true},
		{"empty", []byte{0, 0, 0, 0, 0, 0, 0, 0}, 0, 0, false},
	}
	for _, c := range cases {
		reg, val, ok := decodeBMSCommand(canIDCommand, c.data)
		if ok != c.wantOK || reg != c.wantReg || val != c.wantVal {
			t.Errorf("%s: got (reg=%d val=%d ok=%v) want (reg=%d val=%d ok=%v)",
				c.name, reg, val, ok, c.wantReg, c.wantVal, c.wantOK)
		}
	}
	// 非命令 ID 不应被识别。
	if _, _, ok := decodeBMSCommand(canIDStatus, []byte{0, 0, 0, 0, 0, 0x55, 0, 0}); ok {
		t.Errorf("non-command id should not decode")
	}
}
