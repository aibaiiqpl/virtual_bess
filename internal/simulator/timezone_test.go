package simulator

import (
	"math"
	"testing"
	"time"
)

func TestSetSiteTimezoneRejectsInvalidZone(t *testing.T) {
	old := siteLocation
	t.Cleanup(func() { siteLocation = old })

	if err := SetSiteTimezone("Not/AZone"); err == nil {
		t.Fatal("SetSiteTimezone with invalid zone = nil, want error")
	}
	if siteLocation != old {
		t.Fatalf("siteLocation changed to %v on invalid zone, want unchanged", siteLocation)
	}
}

func TestSetSiteTimezoneEmptyFallsBackToHostLocal(t *testing.T) {
	withSiteTimezone(t, "Asia/Tokyo")
	withSiteTimezone(t, "")

	if SiteLocation() != time.Local {
		t.Fatalf("SiteLocation() = %v, want host local", SiteLocation())
	}
}

// 负荷曲线必须按站点时区取小时数：同一 UTC 时刻在东京已是午夜低谷，
// 在 UTC 站点仍是下午高峰，两者的负荷率不能相同。
func TestLoadUpdateFollowsSiteTimezone(t *testing.T) {
	at := time.Date(2026, 5, 18, 15, 0, 0, 0, time.UTC) // 东京 2026-05-19 00:00。

	withSiteTimezone(t, "Asia/Tokyo")
	tokyo := NewLoad("load", 100)
	tokyo.Update(at)

	withSiteTimezone(t, "UTC")
	utc := NewLoad("load", 100)
	utc.Update(at)

	// Update 带 ±5% 随机噪声，用宽松区间断言曲线段而不是精确值。
	assertRatioNear(t, tokyo.ActualPowerKW()/100, loadBaseRatio(0.0))
	assertRatioNear(t, utc.ActualPowerKW()/100, loadBaseRatio(15.0))
}

// PV 日电量必须在站点当地零点翻转，而不是主机时区零点。
func TestPVDailyEnergyResetsAtSiteMidnight(t *testing.T) {
	withSiteTimezone(t, "Asia/Tokyo")
	pv := newTestPV(t)

	beforeMidnight := time.Date(2026, 5, 18, 14, 0, 0, 0, time.UTC) // 东京 23:00。
	pv.resetPeriods(beforeMidnight)
	pv.dailyEnergyKWh = 12.5
	pv.dailyPeakPowerKW = 80

	stillSameDay := time.Date(2026, 5, 18, 14, 59, 0, 0, time.UTC) // 东京 23:59。
	pv.resetPeriods(stillSameDay)
	if pv.dailyEnergyKWh != 12.5 {
		t.Fatalf("dailyEnergyKWh before site midnight = %v, want 12.5", pv.dailyEnergyKWh)
	}

	afterMidnight := time.Date(2026, 5, 18, 15, 1, 0, 0, time.UTC) // 东京次日 00:01。
	pv.resetPeriods(afterMidnight)
	if pv.dailyEnergyKWh != 0 || pv.dailyPeakPowerKW != 0 {
		t.Fatalf("daily counters after site midnight = (%v, %v), want (0, 0)",
			pv.dailyEnergyKWh, pv.dailyPeakPowerKW)
	}
}

func assertRatioNear(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > want*0.06 {
		t.Fatalf("load ratio = %v, want near %v", got, want)
	}
}
