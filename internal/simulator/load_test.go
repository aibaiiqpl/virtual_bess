package simulator

import (
	"math"
	"testing"
)

func TestLoadBaseRatioKeyHours(t *testing.T) {
	tests := []struct {
		hour float64
		want float64
	}{
		{0.0, 0.22},  // midnight baseline
		{6.0, 0.22},  // pre-dawn
		{8.0, 0.88},  // morning peak begins
		{12.5, 0.52}, // lunch dip center
		{18.0, 0.92}, // evening peak begins
		{21.0, 0.92}, // evening peak end
		{23.0, 0.22}, // late night
		{24.0, 0.22}, // end of day
	}
	for _, tt := range tests {
		got := loadBaseRatio(tt.hour)
		if math.Abs(got-tt.want) > 0.001 {
			t.Errorf("loadBaseRatio(%v) = %v, want %v", tt.hour, got, tt.want)
		}
	}
}

func TestLoadCurveOrdering(t *testing.T) {
	// 午高峰 > 午休 > 夜间基础
	morningPeak := loadBaseRatio(10.0)
	lunchDip := loadBaseRatio(12.5)
	nightBase := loadBaseRatio(3.0)
	eveningPeak := loadBaseRatio(19.5)

	if morningPeak <= lunchDip {
		t.Errorf("morning peak %v should be > lunch dip %v", morningPeak, lunchDip)
	}
	if lunchDip <= nightBase {
		t.Errorf("lunch dip %v should be > night base %v", lunchDip, nightBase)
	}
	if eveningPeak <= lunchDip {
		t.Errorf("evening peak %v should be > lunch dip %v", eveningPeak, lunchDip)
	}
}

func TestLoadCurveContinuous(t *testing.T) {
	// 检查曲线没有突变：相邻 0.1h 采样点之间的变化 < 0.1
	prev := loadBaseRatio(0.0)
	for h := 0.1; h <= 24.0; h += 0.1 {
		cur := loadBaseRatio(h)
		if math.Abs(cur-prev) > 0.1 {
			t.Errorf("discontinuity at hour %.1f: %v → %v", h, prev, cur)
		}
		prev = cur
	}
}

func TestLoadCurveInBounds(t *testing.T) {
	for h := 0.0; h <= 24.0; h += 0.25 {
		v := loadBaseRatio(h)
		if v < 0 || v > 1 {
			t.Errorf("loadBaseRatio(%v) = %v out of [0,1]", h, v)
		}
	}
}

func TestSmoothstepBoundary(t *testing.T) {
	if smoothstep(-1) != 0 {
		t.Errorf("smoothstep(-1) = %v, want 0", smoothstep(-1))
	}
	if smoothstep(2) != 1 {
		t.Errorf("smoothstep(2) = %v, want 1", smoothstep(2))
	}
	if math.Abs(smoothstep(0.5)-0.5) > 0.001 {
		t.Errorf("smoothstep(0.5) = %v, want 0.5", smoothstep(0.5))
	}
}
