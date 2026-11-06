package ui

import (
	"math"
	"testing"
)

func TestPulseOpacity(t *testing.T) {
	if got := pulseOpacity(0); got != 1 {
		t.Errorf("pulseOpacity(0) = %v, want 1", got)
	}
	least := 1.0
	for step := range pulseSteps {
		o := pulseOpacity(step)
		if o < pulseMin || o > 1 {
			t.Errorf("pulseOpacity(%d) = %v, out of range", step, o)
		}
		least = min(least, o)
	}
	// The dimmest step is within one step of pulseMin.
	if least > pulseMin+(1-pulseMin)*(1-math.Cos(math.Pi/pulseSteps))/2+1e-9 {
		t.Errorf("dimmest is %v, want near %v", least, pulseMin)
	}
	if pulseOpacity(pulseSteps) != pulseOpacity(0) {
		t.Error("the pulse doesn't repeat")
	}
}
