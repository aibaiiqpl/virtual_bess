package simulator

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"virtual_bess/internal/zaplog"
)

// PersistedState 是写到磁盘的快照，进程重启后从这里恢复电表/电池/PV 的累积量。
type PersistedState struct {
	Meters    []PersistedMeter   `json:"meters"`
	Batteries []PersistedBattery `json:"batteries"`
	PVs       []PersistedPV      `json:"pvs"`
}

type PersistedMeter struct {
	SlaveID    uint8   `json:"slave_id"`
	ForwardKWh float64 `json:"forward_kwh"`
	ReverseKWh float64 `json:"reverse_kwh"`
}

type PersistedBattery struct {
	PCSSlaveID          uint8   `json:"pcs_slave_id"`
	CurrentEnergyKWh    float64 `json:"current_energy_kwh"`
	TotalChargeKWh      float64 `json:"total_charge_kwh"`
	TotalDischargeKWh   float64 `json:"total_discharge_kwh"`
	SessionChargeKWh    float64 `json:"session_charge_kwh"`
	SessionDischargeKWh float64 `json:"session_discharge_kwh"`
}

type PersistedPV struct {
	SlaveID          uint8   `json:"slave_id"`
	TotalEnergyKWh   float64 `json:"total_energy_kwh"`
	DailyEnergyKWh   float64 `json:"daily_energy_kwh"`
	MonthlyEnergyKWh float64 `json:"monthly_energy_kwh"`
	YearlyEnergyKWh  float64 `json:"yearly_energy_kwh"`
	DailyPeakPowerKW float64 `json:"daily_peak_power_kw"`
	DayKey           int     `json:"day_key"`
	MonthKey         int     `json:"month_key"`
	YearKey          int     `json:"year_key"`
}

// LoadState 从 path 读取上一次持久化的状态。文件不存在返回 nil, nil。
func LoadState(path string) (*PersistedState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var st PersistedState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("decode state: %w", err)
	}
	return &st, nil
}

// SaveState 原子写：先写临时文件再 rename。
func SaveState(path string, st *PersistedState) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// SnapshotLocked 加锁后调用 Snapshot；可在任意 goroutine 中安全调用。
func (sim *Simulator) SnapshotLocked() *PersistedState {
	sim.mu.Lock()
	defer sim.mu.Unlock()
	return sim.Snapshot()
}

// RestoreLocked 加锁后调用 Restore。
func (sim *Simulator) RestoreLocked(st *PersistedState) {
	sim.mu.Lock()
	defer sim.mu.Unlock()
	sim.Restore(st)
}

// Snapshot 收集当前 simulator 状态写成 PersistedState。
// 需要在持有 sim.mu 时调用。
func (sim *Simulator) Snapshot() *PersistedState {
	st := &PersistedState{}
	for _, agg := range sim.meters {
		st.Meters = append(st.Meters, PersistedMeter{
			SlaveID:    agg.meter.bank.SlaveID,
			ForwardKWh: agg.meter.forwardKWh,
			ReverseKWh: agg.meter.reverseKWh,
		})
	}
	for _, bu := range sim.batteries {
		st.Batteries = append(st.Batteries, PersistedBattery{
			PCSSlaveID:          bu.pcs.SlaveID,
			CurrentEnergyKWh:    bu.currentEnergyKWh,
			TotalChargeKWh:      bu.totalChargeKWh,
			TotalDischargeKWh:   bu.totalDischargeKWh,
			SessionChargeKWh:    bu.sessionChargeKWh,
			SessionDischargeKWh: bu.sessionDischargeKWh,
		})
	}
	for _, pv := range sim.pvs {
		st.PVs = append(st.PVs, PersistedPV{
			SlaveID:          pv.bank.SlaveID,
			TotalEnergyKWh:   pv.totalEnergyKWh,
			DailyEnergyKWh:   pv.dailyEnergyKWh,
			MonthlyEnergyKWh: pv.monthlyEnergyKWh,
			YearlyEnergyKWh:  pv.yearlyEnergyKWh,
			DailyPeakPowerKW: pv.dailyPeakPowerKW,
			DayKey:           pv.dayKey,
			MonthKey:         pv.monthKey,
			YearKey:          pv.yearKey,
		})
	}
	return st
}

// Restore 把 PersistedState 写回 simulator 各个 unit。
// 找不到对应 slave 的条目会跳过并打 warning。需要在持有 sim.mu 时调用。
func (sim *Simulator) Restore(st *PersistedState) {
	if st == nil {
		return
	}
	meterBySlave := map[uint8]*Meter{}
	for _, agg := range sim.meters {
		meterBySlave[agg.meter.bank.SlaveID] = agg.meter
	}
	for _, pm := range st.Meters {
		m, ok := meterBySlave[pm.SlaveID]
		if !ok {
			zaplog.Warnf("state: meter slave %d not in current config, skip", pm.SlaveID)
			continue
		}
		m.forwardKWh = pm.ForwardKWh
		m.reverseKWh = pm.ReverseKWh
	}

	batBySlave := map[uint8]*BatteryUnit{}
	for _, bu := range sim.batteries {
		batBySlave[bu.pcs.SlaveID] = bu
	}
	for _, pb := range st.Batteries {
		bu, ok := batBySlave[pb.PCSSlaveID]
		if !ok {
			zaplog.Warnf("state: battery pcs %d not in current config, skip", pb.PCSSlaveID)
			continue
		}
		bu.currentEnergyKWh = pb.CurrentEnergyKWh
		bu.totalChargeKWh = pb.TotalChargeKWh
		bu.totalDischargeKWh = pb.TotalDischargeKWh
		bu.sessionChargeKWh = pb.SessionChargeKWh
		bu.sessionDischargeKWh = pb.SessionDischargeKWh
	}

	pvBySlave := map[uint8]*PVUnit{}
	for _, pv := range sim.pvs {
		pvBySlave[pv.bank.SlaveID] = pv
	}
	for _, pp := range st.PVs {
		pv, ok := pvBySlave[pp.SlaveID]
		if !ok {
			zaplog.Warnf("state: pv slave %d not in current config, skip", pp.SlaveID)
			continue
		}
		pv.totalEnergyKWh = pp.TotalEnergyKWh
		pv.dailyEnergyKWh = pp.DailyEnergyKWh
		pv.monthlyEnergyKWh = pp.MonthlyEnergyKWh
		pv.yearlyEnergyKWh = pp.YearlyEnergyKWh
		pv.dailyPeakPowerKW = pp.DailyPeakPowerKW
		pv.dayKey = pp.DayKey
		pv.monthKey = pp.MonthKey
		pv.yearKey = pp.YearKey
	}
}
