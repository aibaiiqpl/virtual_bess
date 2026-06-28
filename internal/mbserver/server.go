// Package mbserver 实现 Modbus 从站服务端。
package mbserver

import (
	"io"
	"net"
	"sync"
	"time"

	"virtual_bess/internal/zaplog"

	"github.com/goburrow/serial"
)

// Server 是带完整 Modbus 内存区的从站服务端。
type Server struct {
	// Debug 控制是否输出更多调试信息。
	Debug            bool
	listeners        []net.Listener
	ports            []serial.Port
	portsWG          sync.WaitGroup
	portsCloseChan   chan struct{}
	requestChan      chan *Request
	function         [256](func(*Server, Framer) ([]byte, *Exception))
	DiscreteInputs   []byte
	Coils            []byte
	HoldingRegisters []uint16
	InputRegisters   []uint16
}

// Request 保存连接与 Modbus 帧。
type Request struct {
	conn  io.ReadWriteCloser
	frame Framer
}

// NewServer 创建新的 Modbus 从站服务端。
func NewServer() *Server {
	s := &Server{}

	// 分配 Modbus 内存区。
	s.DiscreteInputs = make([]byte, 65536)
	s.Coils = make([]byte, 65536)
	s.HoldingRegisters = make([]uint16, 65536)
	s.InputRegisters = make([]uint16, 65536)

	// 注册默认功能码处理器。
	s.function[1] = ReadCoils
	s.function[2] = ReadDiscreteInputs
	s.function[3] = ReadHoldingRegisters
	s.function[4] = ReadInputRegisters
	s.function[5] = WriteSingleCoil
	s.function[6] = WriteHoldingRegister
	s.function[15] = WriteMultipleCoils
	s.function[16] = WriteHoldingRegisters

	s.requestChan = make(chan *Request)
	s.portsCloseChan = make(chan struct{})

	go s.handler()

	return s
}

// RegisterFunctionHandler 覆盖指定 Modbus 功能码的默认处理逻辑。
func (s *Server) RegisterFunctionHandler(funcCode uint8, function func(*Server, Framer) ([]byte, *Exception)) {
	s.function[funcCode] = function
}

func (s *Server) handle(request *Request) Framer {
	var exception *Exception
	var data []byte

	response := request.frame.Copy()

	function := request.frame.GetFunction()
	if s.function[function] != nil {
		data, exception = s.function[function](s, request.frame)
		response.SetData(data)
	} else {
		exception = &IllegalFunction
	}

	if exception != &Success {
		response.SetException(exception)
	}

	return response
}

func (s *Server) handleErrorRequest(request *Request, exception *Exception) {

	response := request.frame.Copy()

	response.SetException(exception)

	request.conn.Write(response.Bytes())
}

// 所有请求串行处理，避免 Modbus 内存区并发写入导致数据竞争。
func (s *Server) handler() {
	for {
		request := <-s.requestChan
		response := s.handle(request)
		startTime := time.Now()
		request.conn.Write(response.Bytes())
		duration := time.Since(startTime)
		if duration > 100*time.Millisecond {
			zaplog.Warnf("[MB-SVR] slow request %v, frame: %+v\n", duration, request.frame)
		}
	}
}

// Close 停止监听 TCP 端口并关闭串口。
func (s *Server) Close() {
	for _, listen := range s.listeners {
		listen.Close()
	}

	close(s.portsCloseChan)
	s.portsWG.Wait()

	for _, port := range s.ports {
		port.Close()
	}
}
