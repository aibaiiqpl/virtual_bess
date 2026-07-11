//go:build linux

package can

import (
	"encoding/binary"
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// socketCAN frame 线格式（对齐 linux/can.h struct can_frame，共 16 字节）：
//
//	[0:4]  can_id（主机字节序，含 EFF/RTR/ERR 标志）
//	[4]    can_dlc（数据长度 0~8）
//	[5:8]  __pad / __res0 / len8_dlc
//	[8:16] data[8]
const canFrameSize = 16

// nativeEndian 是 socketcan can_id 使用的主机字节序。
// 部署目标 amd64/arm64 均为小端；如需移植到大端架构在此调整。
var nativeEndian binary.ByteOrder = binary.LittleEndian

func init() {
	dialCAN = dialSocketCAN
}

type socketCANBus struct {
	fd    int
	iface string
}

// dialSocketCAN 打开一个绑定到指定接口的 CAN raw socket。
func dialSocketCAN(iface string) (canBus, error) {
	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		return nil, fmt.Errorf("can interface %q not found: %w", iface, err)
	}
	fd, err := unix.Socket(unix.AF_CAN, unix.SOCK_RAW, unix.CAN_RAW)
	if err != nil {
		return nil, fmt.Errorf("open CAN raw socket for %q: %w", iface, err)
	}
	addr := &unix.SockaddrCAN{Ifindex: ifi.Index}
	if err := unix.Bind(fd, addr); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("bind CAN socket to %q: %w", iface, err)
	}
	return &socketCANBus{fd: fd, iface: iface}, nil
}

func (b *socketCANBus) Send(f canFrame) error {
	var buf [canFrameSize]byte
	nativeEndian.PutUint32(buf[0:4], f.id|canEFFFlag)
	buf[4] = 8 // DLC 固定 8
	copy(buf[8:16], f.data[:])
	_, err := unix.Write(b.fd, buf[:])
	return err
}

func (b *socketCANBus) Recv() (uint32, []byte, error) {
	var buf [canFrameSize]byte
	n, err := unix.Read(b.fd, buf[:])
	if err != nil {
		return 0, nil, err
	}
	if n < canFrameSize {
		return 0, nil, fmt.Errorf("short CAN frame: %d bytes", n)
	}
	rawID := nativeEndian.Uint32(buf[0:4])
	id := rawID & unix.CAN_EFF_MASK // 去掉 EFF/RTR/ERR 标志位，保留 29 位 ID
	dlc := int(buf[4])
	if dlc > 8 {
		dlc = 8
	}
	data := make([]byte, dlc)
	copy(data, buf[8:8+dlc])
	return id, data, nil
}

func (b *socketCANBus) Close() error {
	return unix.Close(b.fd)
}
