package mbserver

import (
	"fmt"
	"io"
	"net"
	"strings"

	"virtual_bess/internal/zaplog"
)

// RTU ADU 最大长度 256 字节（与 MBAP 的 260 不同：没有 7 字节头，多 2 字节 CRC）。
const maxRTUFrameLength = 256

// RTU 请求帧固定部分：Address(1) + Function(1) + 起始地址(2) + 数量/值(2) + CRC(2)。
// FC1/2/3/4/5/6 都是这个长度；FC15/16 在此之后还有「字节数(1) + 数据(N)」。
const rtuFixedRequestLength = 8

// FC15/16 读到「字节数」字段所需的前缀长度：Address+Function+起始地址+数量+字节数。
const rtuWriteMultiplePrefixLength = 7

// ListenRTUOverTCP 启动 Modbus RTU over TCP 监听（串口服务器 / 透传网关模拟）。
//
// 与 ListenTCP 的唯一区别是帧格式：报文没有 MBAP 头，直接是 RTU 帧
// `Address|Function|Data|CRC16`。用于联调 emu-rs 的 rtu_tcp 端口类型：
// 同一个模拟器可以同时开 MBAP 端口和 RTU 透传端口，共用同一份寄存器区。
func (s *Server) ListenRTUOverTCP(addressPort string) error {
	listen, err := net.Listen("tcp", addressPort)
	if err != nil {
		zaplog.Errorf("[MB-SVR] failed to listen rtu-over-tcp on %s: %v", addressPort, err)
		return err
	}
	s.listeners = append(s.listeners, listen)
	go s.acceptRTUOverTCP(listen)
	return nil
}

func (s *Server) acceptRTUOverTCP(listen net.Listener) {
	for {
		conn, err := listen.Accept()
		if err != nil {
			if strings.Contains(err.Error(), "use of closed network connection") {
				return
			}
			zaplog.Errorf("[MB-SVR] unable to accept rtu-over-tcp connections: %v", err)
			return
		}
		go func(conn net.Conn) {
			defer conn.Close()
			s.serveRTUOverTCPConn(conn)
		}(conn)
	}
}

// serveRTUOverTCPConn 在一条连接上循环收发 RTU 帧。
//
// RTU 帧没有长度字段，必须按功能码推算请求长度；任何读取或 CRC 失败都关闭连接，
// 让客户端重连复位——继续读会把残帧当成下一帧的帧头，之后每帧都错位。
func (s *Server) serveRTUOverTCPConn(conn net.Conn) {
	for {
		packet, err := readRTURequest(conn)
		if err != nil {
			if err != io.EOF {
				zaplog.Errorf("[MB-SVR] rtu-over-tcp read error: %v", err)
			}
			return
		}
		frame, err := NewRTUFrame(packet)
		if err != nil {
			zaplog.Errorf("[MB-SVR] bad rtu-over-tcp frame, closing connection: %v", err)
			return
		}
		s.requestChan <- &Request{conn, frame}
	}
}

// readRTURequest 从字节流里精确读出一整个 RTU 请求帧。
func readRTURequest(conn net.Conn) ([]byte, error) {
	packet := make([]byte, rtuFixedRequestLength, maxRTUFrameLength)
	if _, err := io.ReadFull(conn, packet[:rtuWriteMultiplePrefixLength]); err != nil {
		return nil, err
	}

	switch packet[1] {
	case 15, 16:
		// 变长写：第 7 字节是数据字节数，之后还有 N 字节数据 + 2 字节 CRC。
		byteCount := int(packet[rtuWriteMultiplePrefixLength-1])
		total := rtuWriteMultiplePrefixLength + byteCount + 2
		if total > maxRTUFrameLength {
			return nil, fmt.Errorf("rtu request byte count %d exceeds max frame length", byteCount)
		}
		packet = packet[:total]
		if _, err := io.ReadFull(conn, packet[rtuWriteMultiplePrefixLength:]); err != nil {
			return nil, err
		}
	default:
		// 定长请求：还差 1 字节补满 8 字节。
		if _, err := io.ReadFull(conn, packet[rtuWriteMultiplePrefixLength:rtuFixedRequestLength]); err != nil {
			return nil, err
		}
	}
	return packet, nil
}
