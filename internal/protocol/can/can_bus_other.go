//go:build !linux

package can

import "fmt"

// 非 Linux 平台没有 socketcan：dialCAN 直接返回不支持错误，
// 保证 CAN 未启用时可在 macOS 等平台正常编译/运行。
func init() {
	dialCAN = func(iface string) (canBus, error) {
		return nil, fmt.Errorf("CAN socket support requires Linux (socketcan), iface=%q", iface)
	}
}
