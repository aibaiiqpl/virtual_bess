//go:build !iec61850

package simulator

import "fmt"

func StartIEC61850Server(cfg IEC61850Config, sim *Simulator) (IEC61850Service, error) {
	_ = sim
	if !cfg.Enabled {
		return noopIEC61850Service{}, nil
	}
	return nil, fmt.Errorf("iec61850 support requires building with -tags iec61850")
}
