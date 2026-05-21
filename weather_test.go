package main

import (
	"math"
	"testing"
)

func TestWeatherTransitionsSumToOne(t *testing.T) {
	for state, transitions := range weatherTransitions {
		if len(transitions) == 0 {
			t.Errorf("state %d has no transitions defined", state)
			continue
		}
		last := transitions[len(transitions)-1]
		if math.Abs(last.cum-1.0) > 0.001 {
			t.Errorf("state %d cumulative probability = %v, want 1.0", state, last.cum)
		}
	}
}

func TestWeatherBaseCoeffOrdering(t *testing.T) {
	if !(weatherBaseCoeff(weatherSunny) > weatherBaseCoeff(weatherPartlyCloudy) &&
		weatherBaseCoeff(weatherPartlyCloudy) > weatherBaseCoeff(weatherCloudy) &&
		weatherBaseCoeff(weatherCloudy) > weatherBaseCoeff(weatherRainy)) {
		t.Errorf("base coefficients not monotonic: sunny=%v partly=%v cloudy=%v rainy=%v",
			weatherBaseCoeff(weatherSunny),
			weatherBaseCoeff(weatherPartlyCloudy),
			weatherBaseCoeff(weatherCloudy),
			weatherBaseCoeff(weatherRainy))
	}
}

func TestWeatherCoeffStaysBounded(t *testing.T) {
	w := NewWeather()
	for i := 0; i < 5000; i++ {
		w.Update(1.0)
		if w.coeff < 0 || w.coeff > 1 {
			t.Fatalf("weatherCoeff out of [0,1] at tick %d: %v", i, w.coeff)
		}
	}
}

func TestWeatherInitialStateNotAlwaysFixed(t *testing.T) {
	seen := map[weatherState]bool{}
	for i := 0; i < 200 && len(seen) < 2; i++ {
		w := NewWeather()
		seen[w.state] = true
	}
	if len(seen) < 2 {
		t.Errorf("NewWeather appears deterministic, only saw state: %v", seen)
	}
}

func TestWeatherSmoothingDampensSwings(t *testing.T) {
	// Force a large state change and verify the coefficient doesn't jump in one tick.
	w := &Weather{
		state:  weatherSunny,
		remain: 9999,
		coeff:  weatherBaseCoeff(weatherSunny), // ~0.97
	}
	w.state = weatherRainy // simulate transition; target is ~0.08
	w.Update(1.0)

	// One tick should move ~10% toward target (alpha=0.1), not all the way.
	if w.coeff < 0.5 {
		t.Errorf("weatherCoeff jumped too fast: %v (expected gradual EMA transition)", w.coeff)
	}
}
