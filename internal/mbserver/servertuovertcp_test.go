package mbserver

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// 联调保障：RTU over TCP 监听必须按 RTU 帧收发（无 MBAP 头），
// 并与 MBAP 端口共用同一份寄存器区。
func TestListenRTUOverTCPServesRTUFrames(t *testing.T) {
	s := NewServer()
	defer s.Close()
	if err := s.ListenRTUOverTCP("127.0.0.1:0"); err != nil {
		t.Fatalf("listen: %v", err)
	}
	address := s.listeners[len(s.listeners)-1].Addr().String()
	s.HoldingRegisters[100] = 0x1234
	s.HoldingRegisters[101] = 0x5678

	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	request := []byte{0x07, 0x03, 0x00, 0x64, 0x00, 0x02, 0x00, 0x00}
	binary.LittleEndian.PutUint16(request[6:], crcModbus(request[:6]))
	if _, err := conn.Write(request); err != nil {
		t.Fatalf("write: %v", err)
	}

	// 正常响应：addr + fc + byteCount + 4 数据 + CRC2 = 9 字节。
	response := make([]byte, 9)
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	if _, err := io.ReadFull(conn, response); err != nil {
		t.Fatalf("read: %v", err)
	}
	if response[0] != 0x07 || response[1] != 0x03 || response[2] != 4 {
		t.Fatalf("unexpected response header: %v", response)
	}
	if got := binary.BigEndian.Uint16(response[3:5]); got != 0x1234 {
		t.Fatalf("register 100 = %#04x", got)
	}
	if got := binary.BigEndian.Uint16(response[5:7]); got != 0x5678 {
		t.Fatalf("register 101 = %#04x", got)
	}
	wantCRC := crcModbus(response[:7])
	if got := binary.LittleEndian.Uint16(response[7:]); got != wantCRC {
		t.Fatalf("crc = %#04x, want %#04x", got, wantCRC)
	}
}

// 写多寄存器是变长请求：服务端必须按「字节数」字段续读，否则帧会被截断。
func TestListenRTUOverTCPHandlesVariableLengthWrite(t *testing.T) {
	s := NewServer()
	defer s.Close()
	if err := s.ListenRTUOverTCP("127.0.0.1:0"); err != nil {
		t.Fatalf("listen: %v", err)
	}
	address := s.listeners[len(s.listeners)-1].Addr().String()

	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// FC16 写 2 个寄存器到 addr 200。
	request := []byte{0x01, 0x10, 0x00, 0xC8, 0x00, 0x02, 0x04, 0x00, 0x0A, 0x00, 0x14, 0x00, 0x00}
	binary.LittleEndian.PutUint16(request[11:], crcModbus(request[:11]))
	if _, err := conn.Write(request); err != nil {
		t.Fatalf("write: %v", err)
	}

	// FC16 回显：addr + fc + 起始地址2 + 数量2 + CRC2 = 8 字节。
	response := make([]byte, 8)
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	if _, err := io.ReadFull(conn, response); err != nil {
		t.Fatalf("read: %v", err)
	}
	if response[1] != 0x10 {
		t.Fatalf("unexpected function: %v", response)
	}
	if s.HoldingRegisters[200] != 10 || s.HoldingRegisters[201] != 20 {
		t.Fatalf("registers not written: %d %d", s.HoldingRegisters[200], s.HoldingRegisters[201])
	}
}
