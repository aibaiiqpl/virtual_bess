package mbserver

import (
	"crypto/tls"
	"encoding/binary"
	"io"
	"log"
	"net"
	"strings"

	"virtual_bess/internal/zaplog"
)

const (
	MBAPHeaderLength  = 7
	MaxTCPFrameLength = 260
)

func (s *Server) accept(listen net.Listener) error {
	for {
		conn, err := listen.Accept()
		if err != nil {
			if strings.Contains(err.Error(), "use of closed network connection") {
				return nil
			}
			zaplog.Errorf("[MB-SVR] Unable to accept connections: %#v\n", err)
			return err
		}

		go func(conn net.Conn) {
			defer conn.Close()
			for {
				packet := make([]byte, 512)
				// 先读取MBAPHeaderLength长度的数据
				bytesRead, err := io.ReadFull(conn, packet[:MBAPHeaderLength])
				if err != nil {
					if err != io.EOF {
						zaplog.Errorf("[MB-SVR] read error %v\n", err)
					}
					return
				}
				// determine how many more bytes we need to read
				bytesNeeded := binary.BigEndian.Uint16(packet[4:6])
				// the byte count includes the unit ID field, which we already have
				bytesNeeded--

				// never read more than the max allowed frame length
				if bytesNeeded+MBAPHeaderLength > MaxTCPFrameLength {
					zaplog.Errorf("[MB-SVR] frame error: bytesNeeded %v > MaxTCPFrameLength %v\n",
						bytesNeeded+MBAPHeaderLength, MaxTCPFrameLength)
					return
				}

				// an MBAP length of 0 is illegal
				if bytesNeeded <= 0 {
					zaplog.Errorf("[MB-SVR] frame error: bytesNeeded %v <= 0\n", bytesNeeded)
					return
				}
				bytesRead2, err := io.ReadFull(conn, packet[MBAPHeaderLength:MBAPHeaderLength+bytesNeeded])
				if err != nil {
					if err != io.EOF {
						zaplog.Errorf("[MB-SVR] read %d error %v\n", bytesNeeded, err)
					}
					return
				}
				// Set the length of the packet to the number of read bytes.
				packet = packet[:bytesRead+bytesRead2]

				frame, err := NewTCPFrame(packet)
				if err != nil {
					zaplog.Errorf("[MB-SVR] bad packet error %v\n", err)
					return
				}

				request := &Request{conn, frame}
				//if len(s.requestChan) == cap(s.requestChan) {
				//	zaplog.Errorf("[MB-SVR] too many requests (%d)\n", len(s.requestChan))
				//	s.handleErrorRequest(request, &SlaveDeviceBusy)
				//	return
				//}

				s.requestChan <- request
			}
		}(conn)
	}
}

// ListenTCP 启动 Modbus TCP 监听，地址格式为 "address:port"。
func (s *Server) ListenTCP(addressPort string) (err error) {
	listen, err := net.Listen("tcp", addressPort)
	if err != nil {
		log.Printf("Failed to Listen: %v\n", err)
		return err
	}
	s.listeners = append(s.listeners, listen)
	go s.accept(listen)
	return err
}

// ListenTLS 启动带 TLS 的 Modbus TCP 监听，地址格式为 "address:port"。
func (s *Server) ListenTLS(addressPort string, config *tls.Config) (err error) {
	listen, err := tls.Listen("tcp", addressPort, config)
	if err != nil {
		log.Printf("Failed to Listen on TLS: %v\n", err)
		return err
	}
	s.listeners = append(s.listeners, listen)
	go s.accept(listen)
	return err
}
