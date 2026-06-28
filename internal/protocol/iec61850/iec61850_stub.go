//go:build !iec61850

package iec61850sim

import (
	"fmt"

	"virtual_bess/internal/simulator"
)

func StartServer(cfg simulator.IEC61850Config, sim *simulator.Simulator) (IEC61850Service, error) {
	_ = sim
	if !cfg.Enabled {
		return noopIEC61850Service{}, nil
	}
	return nil, fmt.Errorf("iec61850 support requires building with -tags iec61850")
}
