package simulator

import (
	"math"
	"math/rand"
)

type weatherState int

const (
	weatherSunny        weatherState = iota // coeff ~0.94–1.00
	weatherPartlyCloudy                     // coeff ~0.53–0.83, high flutter
	weatherCloudy                           // coeff ~0.27–0.43
	weatherRainy                            // coeff ~0.03–0.13
)

func weatherBaseCoeff(s weatherState) float64 {
	switch s {
	case weatherSunny:
		return 0.97
	case weatherPartlyCloudy:
		return 0.68
	case weatherCloudy:
		return 0.35
	default:
		return 0.08
	}
}

func weatherTargetSpread(s weatherState) float64 {
	switch s {
	case weatherSunny:
		return 0.02
	case weatherPartlyCloudy:
		return 0.10
	case weatherCloudy:
		return 0.06
	default:
		return 0.03
	}
}

// weatherStateDuration returns a random stay duration in seconds.
func weatherStateDuration(s weatherState) float64 {
	switch s {
	case weatherSunny:
		return 1200 + rand.Float64()*2400 // 20–60 min
	case weatherPartlyCloudy:
		return 600 + rand.Float64()*1200 // 10–30 min
	case weatherCloudy:
		return 900 + rand.Float64()*1500 // 15–40 min
	default: // rainy
		return 1800 + rand.Float64()*3600 // 30–90 min
	}
}

// weatherTransitions[state] holds cumulative probability transitions to the next state.
var weatherTransitions = [4][]struct {
	next weatherState
	cum  float64
}{
	weatherSunny:        {{weatherSunny, 0.50}, {weatherPartlyCloudy, 0.85}, {weatherCloudy, 1.00}},
	weatherPartlyCloudy: {{weatherSunny, 0.30}, {weatherPartlyCloudy, 0.60}, {weatherCloudy, 0.90}, {weatherRainy, 1.00}},
	weatherCloudy:       {{weatherSunny, 0.10}, {weatherPartlyCloudy, 0.35}, {weatherCloudy, 0.75}, {weatherRainy, 1.00}},
	weatherRainy:        {{weatherPartlyCloudy, 0.15}, {weatherCloudy, 0.50}, {weatherRainy, 1.00}},
}

func nextWeatherState(cur weatherState) weatherState {
	r := rand.Float64()
	for _, t := range weatherTransitions[cur] {
		if r < t.cum {
			return t.next
		}
	}
	return cur
}

// Weather 是所有 PV 共享的天气模型。
type Weather struct {
	state      weatherState
	remain     float64
	coeff      float64
	slowCoeff  float64
	slowTarget float64

	shadeDepth  float64
	shadeTarget float64
	shadeRemain float64
}

func NewWeather() *Weather {
	w := &Weather{}
	w.state = weatherState(rand.Intn(4))
	w.remain = weatherStateDuration(w.state)
	w.slowCoeff = weatherStateTarget(w.state)
	w.slowTarget = w.slowCoeff
	w.updateCoeff()
	return w
}

func (w *Weather) Coeff() float64 { return w.coeff }

func (w *Weather) Update(dtSeconds float64) {
	if dtSeconds <= 0 {
		w.updateCoeff()
		return
	}

	w.remain -= dtSeconds
	if w.remain <= 0 {
		w.state = nextWeatherState(w.state)
		w.remain = weatherStateDuration(w.state)
		w.slowTarget = weatherStateTarget(w.state)
	}

	w.updateSlowCoeff(dtSeconds)
	w.updateCloudShadow(dtSeconds)
	w.updateCoeff()
}

func weatherStateTarget(s weatherState) float64 {
	base := weatherBaseCoeff(s)
	spread := weatherTargetSpread(s)
	return clampFloat64(base+(rand.Float64()*2-1)*spread, 0, 1)
}

func (w *Weather) updateSlowCoeff(dtSeconds float64) {
	const slowTimeConstantSeconds = 240.0
	alpha := 1 - math.Exp(-dtSeconds/slowTimeConstantSeconds)
	w.slowCoeff += (w.slowTarget - w.slowCoeff) * alpha
}

func (w *Weather) updateCloudShadow(dtSeconds float64) {
	if w.shadeRemain > 0 {
		w.shadeRemain -= dtSeconds
		if w.shadeRemain <= 0 {
			w.shadeTarget = 0
		} else if rand.Float64() < shadowRetargetProbability(dtSeconds) {
			w.shadeTarget = randomShadowDepth(w.state)
		}
	} else if rand.Float64() < shadowStartProbability(w.state, dtSeconds) {
		w.shadeRemain = randomShadowDuration(w.state)
		w.shadeTarget = randomShadowDepth(w.state)
	}

	w.updateShadeDepth(dtSeconds)
}

func (w *Weather) updateShadeDepth(dtSeconds float64) {
	timeConstant := 25.0
	if w.shadeTarget > w.shadeDepth {
		timeConstant = 4.0
	}
	alpha := 1 - math.Exp(-dtSeconds/timeConstant)
	w.shadeDepth += (w.shadeTarget - w.shadeDepth) * alpha
	w.shadeDepth = clampFloat64(w.shadeDepth, 0, 0.85)
}

func shadowStartProbability(s weatherState, dtSeconds float64) float64 {
	interval := shadowMeanIntervalSeconds(s)
	return 1 - math.Exp(-dtSeconds/interval)
}

func shadowRetargetProbability(dtSeconds float64) float64 {
	return 1 - math.Exp(-dtSeconds/8.0)
}

func shadowMeanIntervalSeconds(s weatherState) float64 {
	switch s {
	case weatherSunny:
		return 1800
	case weatherPartlyCloudy:
		return 180
	case weatherCloudy:
		return 300
	default:
		return 900
	}
}

func randomShadowDuration(s weatherState) float64 {
	switch s {
	case weatherSunny:
		return 20 + rand.Float64()*70
	case weatherPartlyCloudy:
		return 10 + rand.Float64()*80
	case weatherCloudy:
		return 20 + rand.Float64()*100
	default:
		return 30 + rand.Float64()*120
	}
}

func randomShadowDepth(s weatherState) float64 {
	switch s {
	case weatherSunny:
		return 0.03 + rand.Float64()*0.09
	case weatherPartlyCloudy:
		return 0.15 + rand.Float64()*0.45
	case weatherCloudy:
		return 0.10 + rand.Float64()*0.30
	default:
		return 0.05 + rand.Float64()*0.15
	}
}

func (w *Weather) updateCoeff() {
	w.coeff = clampFloat64(w.slowCoeff*(1-w.shadeDepth), 0, 1)
}

func clampFloat64(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
