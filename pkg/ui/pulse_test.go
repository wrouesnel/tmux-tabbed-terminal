package ui

import (
	"math"
	"testing"

	"github.com/gotk3/gotk3/gtk"
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

// TestPulseOnlyWhileActive checks the pulse runs only while a dot is shown and a window
// is active, and that dots stand still, at full strength, while none is.
func TestPulseOnlyWhileActive(t *testing.T) {
	if err := initTestGTK(); err != nil {
		t.Skipf("no display: %v", err)
	}
	dot, err := gtk.LabelNew("●")
	if err != nil {
		t.Fatal(err)
	}
	p := &pulser{shown: map[*gtk.Widget]struct{}{&dot.Widget: {}}}
	defer p.stop()
	p.update()
	if p.timer != 0 {
		t.Error("pulsing with no window active")
	}
	p.setActive(true)
	if p.timer == 0 {
		t.Error("not pulsing with a window active")
	}
	dot.SetOpacity(0.5)
	p.setActive(false)
	if p.timer != 0 {
		t.Error("still pulsing once no window is active")
	}
	if o := dot.GetOpacity(); o != 1 {
		t.Errorf("a still dot's opacity is %v, want 1", o)
	}
}
