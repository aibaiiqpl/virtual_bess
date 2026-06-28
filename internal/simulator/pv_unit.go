package simulator

import (
	"math"
	"time"
)

const pvInverterEfficiency = 0.98

// pvLocation 持有站点时区，用于把时间转换为本地太阳时。
var pvLocation *time.Location = time.Local

// SetPVTimezone 设置 PV 发电计算使用的时区。
func SetPVTimezone(tz string) {
	if tz == "" {
		pvLocation = time.Local
		return
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		pvLocation = time.Local
		return
	}
	pvLocation = loc
}

type pvLimitMode int

const (
	pvLimitPercent pvLimitMode = iota
	pvLimitFixed
)

// PVUnit 表示一台 PV 逆变器。多个 PVUnit 共享同一份 Weather。
type PVUnit struct {
	bank *SlaveBank

	ratedPowerKW      float64
	pcsACVoltage      float64
	batteryVoltageNom float64

	running          bool
	actualPowerKW    float64
	totalEnergyKWh   float64
	dailyEnergyKWh   float64
	monthlyEnergyKWh float64
	yearlyEnergyKWh  float64
	dailyPeakPowerKW float64

	limitMode           pvLimitMode
	lastPercentLimitRaw uint16
	lastFixedLimitRaw   uint16

	dayKey, monthKey, yearKey int
}

func NewPVUnit(cfg PVUnitConfig, pcsACVoltage float64, bank *SlaveBank) *PVUnit {
	pv := &PVUnit{
		bank:              bank,
		ratedPowerKW:      cfg.RatedPowerKW,
		pcsACVoltage:      pcsACVoltage,
		batteryVoltageNom: 800, // DC bus 显示用固定值，不与 BMS 状态联动。
		running:           true,
		limitMode:         pvLimitPercent,
	}
	// 默认 100% 限值。
	bank.WriteU16(RegPVPercentLimit, 1000)
	pv.lastPercentLimitRaw = 1000
	return pv
}

func (pv *PVUnit) ActualPowerKW() float64 { return pv.actualPowerKW }

// OnPVWrite 处理 PV slave 的寄存器写。
// 调用方已持有 sim.mu，且 bank 已完成写入。
// 限值寄存器写入时切换模式，并对保存值做 clamp。
func (pv *PVUnit) OnPVWrite(addr, value uint16) {
	switch addr {
	case RegPVPercentLimit:
		value = clampU16(value, 0, 1000)
		pv.bank.WriteU16(RegPVPercentLimit, value)
		pv.lastPercentLimitRaw = value
		pv.limitMode = pvLimitPercent
	case RegPVFixedLimit:
		value = clampU16(value, 0, pv.maxFixedLimitRaw())
		pv.bank.WriteU16(RegPVFixedLimit, value)
		pv.lastFixedLimitRaw = value
		pv.limitMode = pvLimitFixed
	}
}

// ProcessControls 处理开停机脉冲，并兜底同步直接写寄存器造成的限值变化。
func (pv *PVUnit) ProcessControls() {
	if pv.bank.ReadU16(RegPVStartup) == 1 {
		pv.running = true
		pv.bank.WriteU16(RegPVStartup, 0)
	}
	if pv.bank.ReadU16(RegPVShutdown) == 1 {
		pv.running = false
		pv.actualPowerKW = 0
		pv.bank.WriteU16(RegPVShutdown, 0)
	}

	percentRaw := pv.bank.ReadU16(RegPVPercentLimit)
	percentChanged := percentRaw != pv.lastPercentLimitRaw
	percentRaw = clampU16(percentRaw, 0, 1000)
	pv.bank.WriteU16(RegPVPercentLimit, percentRaw)

	fixedRaw := pv.bank.ReadU16(RegPVFixedLimit)
	fixedChanged := fixedRaw != pv.lastFixedLimitRaw
	fixedRaw = clampU16(fixedRaw, 0, pv.maxFixedLimitRaw())
	pv.bank.WriteU16(RegPVFixedLimit, fixedRaw)

	if percentChanged {
		pv.lastPercentLimitRaw = percentRaw
		pv.limitMode = pvLimitPercent
	}
	if fixedChanged {
		pv.lastFixedLimitRaw = fixedRaw
		pv.limitMode = pvLimitFixed
	}
}

func (pv *PVUnit) UpdateSimulation(now time.Time, dtSeconds, weatherCoeff float64) {
	pv.resetPeriods(now)

	powerKW := 0.0
	if pv.running {
		powerKW = math.Min(pv.naturalPowerKW(now, weatherCoeff), pv.activeLimitKW())
	}
	if powerKW < 0 {
		powerKW = 0
	}
	pv.actualPowerKW = powerKW
	if powerKW > pv.dailyPeakPowerKW {
		pv.dailyPeakPowerKW = powerKW
	}

	if dtSeconds <= 0 || powerKW == 0 {
		return
	}
	deltaEnergy := powerKW * dtSeconds / 3600.0
	pv.totalEnergyKWh += deltaEnergy
	pv.dailyEnergyKWh += deltaEnergy
	pv.monthlyEnergyKWh += deltaEnergy
	pv.yearlyEnergyKWh += deltaEnergy
}

func (pv *PVUnit) naturalPowerKW(now time.Time, weatherCoeff float64) float64 {
	local := now.In(pvLocation)
	hour := float64(local.Hour()) +
		float64(local.Minute())/60.0 +
		float64(local.Second())/3600.0 +
		float64(local.Nanosecond())/float64(time.Hour)

	const sunrise, sunset = 6.0, 18.0
	if hour <= sunrise || hour >= sunset {
		return 0
	}
	angle := math.Pi * (hour - sunrise) / (sunset - sunrise)
	natural := pv.ratedPowerKW * 0.95 * math.Sin(angle)
	return natural * weatherCoeff
}

func (pv *PVUnit) activeLimitKW() float64 {
	switch pv.limitMode {
	case pvLimitFixed:
		return float64(pv.lastFixedLimitRaw) * 0.1
	default:
		return pv.ratedPowerKW * float64(pv.lastPercentLimitRaw) / 1000.0
	}
}

func (pv *PVUnit) resetPeriods(now time.Time) {
	dayKey := now.Year()*1000 + now.YearDay()
	monthKey := now.Year()*100 + int(now.Month())
	yearKey := now.Year()

	if pv.dayKey == 0 {
		pv.dayKey = dayKey
	}
	if pv.monthKey == 0 {
		pv.monthKey = monthKey
	}
	if pv.yearKey == 0 {
		pv.yearKey = yearKey
	}

	if dayKey != pv.dayKey {
		pv.dailyEnergyKWh = 0
		pv.dailyPeakPowerKW = 0
		pv.dayKey = dayKey
	}
	if monthKey != pv.monthKey {
		pv.monthlyEnergyKWh = 0
		pv.monthKey = monthKey
	}
	if yearKey != pv.yearKey {
		pv.yearlyEnergyKWh = 0
		pv.yearKey = yearKey
	}
}

func (pv *PVUnit) maxFixedLimitRaw() uint16 {
	maxRaw := pv.ratedPowerKW * 10
	maxUint16 := float64(^uint16(0))
	if maxRaw > maxUint16 {
		return ^uint16(0)
	}
	return uint16(maxRaw)
}

func clampU16(value, min, max uint16) uint16 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
