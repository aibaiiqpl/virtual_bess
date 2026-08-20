//go:build !iec61850

package iec61850sim

import (
	"fmt"

	"virtual_bess/internal/simulator"
)

// Supported 表示当前二进制是否编译进了 IEC61850 支持（details 子命令据此说明部署变体）。
const Supported = false

func StartServer(cfg simulator.IEC61850Config, sim *simulator.Simulator) (IEC61850Service, error) {
	_ = sim
	if !cfg.Enabled {
		return noopIEC61850Service{}, nil
	}
	return nil, fmt.Errorf("iec61850 support requires building with -tags iec61850")
}
