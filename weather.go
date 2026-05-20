package main

import "math/rand"

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

func weatherAmplitude(s weatherState) float64 {
	switch s {
	case weatherSunny:
		return 0.03
	case weatherPartlyCloudy:
		return 0.15
	case weatherCloudy:
		return 0.08
	default:
		return 0.05
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

func (b *BESS) initWeather() {
	b.weatherState = weatherState(rand.Intn(4))
	b.weatherRemain = weatherStateDuration(b.weatherState)
	b.weatherCoeff = weatherBaseCoeff(b.weatherState)
}

// weatherSmoothingAlpha is the EMA factor applied each tick.
// With 1-second ticks this yields ~30s state-transition rise time and damps
// the per-tick random sample so cloud-shadow flutter stays in a realistic
// 10–30 second timescale rather than jumping every second.
const weatherSmoothingAlpha = 0.1

func (b *BESS) updateWeather(dtSeconds float64) {
	b.weatherRemain -= dtSeconds
	if b.weatherRemain <= 0 {
		b.weatherState = nextWeatherState(b.weatherState)
		b.weatherRemain = weatherStateDuration(b.weatherState)
	}

	base := weatherBaseCoeff(b.weatherState)
	amp := weatherAmplitude(b.weatherState)
	target := base + (rand.Float64()*2-1)*amp

	b.weatherCoeff += (target - b.weatherCoeff) * weatherSmoothingAlpha
	if b.weatherCoeff > 1 {
		b.weatherCoeff = 1
	} else if b.weatherCoeff < 0 {
		b.weatherCoeff = 0
	}
}
