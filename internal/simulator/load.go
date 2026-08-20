package simulator

import (
	"math"
	"math/rand"
	"time"
)

// Load 是单台商业/工业负载的内部模拟（不暴露到 modbus）。
type Load struct {
	name          string
	ratedPowerKW  float64
	actualPowerKW float64
}

func NewLoad(name string, ratedPowerKW float64) *Load {
	return &Load{name: name, ratedPowerKW: ratedPowerKW}
}

func (l *Load) Name() string { return l.name }

func (l *Load) ActualPowerKW() float64 { return l.actualPowerKW }

// Update 按站点本地时钟推进负荷曲线。
//
// 输入 now 可以是任意时区的时刻，内部统一换算到站点时区（siteLocation），
// 保证仿真主机时区与站点不一致时负荷高峰仍落在站点的白天。
func (l *Load) Update(now time.Time) {
	local := now.In(siteLocation)
	hour := float64(local.Hour()) +
		float64(local.Minute())/60.0 +
		float64(local.Second())/3600.0

	base := loadBaseRatio(hour)
	noise := 1.0 + (rand.Float64()*0.10 - 0.05)
	l.actualPowerKW = l.ratedPowerKW * base * noise
}

// loadBaseRatio returns a normalized [0,1] load ratio for commercial/industrial load:
//   - Night baseline (0–6h, 23–24h): ~22%
//   - Morning ramp (6–8h): rises to ~85%
//   - Morning peak (8–11.5h): ~88–93%
//   - Lunch dip (11.5–13.5h, center 12.5h): ~52%
//   - Afternoon peak (13.5–18h): ~85–92%
//   - Evening peak (18–21h): ~90–95%
//   - Ramp down (21–23h): returns to baseline
func loadBaseRatio(hour float64) float64 {
	type kp struct{ h, v float64 }
	kps := []kp{
		{0.0, 0.22},
		{6.0, 0.22},
		{8.0, 0.88},
		{11.5, 0.90},
		{12.5, 0.52},
		{13.5, 0.88},
		{18.0, 0.92},
		{21.0, 0.92},
		{23.0, 0.22},
		{24.0, 0.22},
	}

	for i := 1; i < len(kps); i++ {
		if hour <= kps[i].h {
			t := (hour - kps[i-1].h) / (kps[i].h - kps[i-1].h)
			return kps[i-1].v + (kps[i].v-kps[i-1].v)*smoothstep(t)
		}
	}
	return 0.22
}

func smoothstep(t float64) float64 {
	t = math.Max(0, math.Min(1, t))
	return t * t * (3 - 2*t)
}
