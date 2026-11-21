package ui

import (
	"math"
	"time"

	"github.com/gotk3/gotk3/gtk"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/gtkx"
)

const (
	// pulsePeriod is how long a busy dot takes to fade out and back.
	pulsePeriod = 2200 * time.Millisecond
	// pulseSteps is how many times a period a busy dot's opacity changes. A CSS
	// animation would redraw at the frame rate, which keeps a whole core busy over
	// Remote Desktop; this redraws only the dots, a few times a second.
	pulseSteps = 11
	// pulseMin is the opacity a busy dot fades to.
	pulseMin = 0.2
)

// pulser fades every busy dot shown in and out together, with one timer that runs only
// while one is shown.
type pulser struct {
	shown map[*gtk.Widget]struct{}
	timer gtkx.Timer
	step  int
}

var busyPulse = pulser{shown: map[*gtk.Widget]struct{}{}}

// pulse makes w a busy dot, which fades in and out while it's shown.
func pulse(w *gtk.Widget) {
	addClass(w, "ttt-busy-dot")
	w.Connect("map", func() {
		busyPulse.shown[w] = struct{}{}
		w.SetOpacity(busyPulse.opacity())
		busyPulse.start()
	})
	w.Connect("unmap", func() {
		delete(busyPulse.shown, w)
		w.SetOpacity(1)
		if len(busyPulse.shown) == 0 {
			busyPulse.stop()
		}
	})
}

// pulseOpacity is a busy dot's opacity at a step of the period, easing from 1 down to
// pulseMin and back.
func pulseOpacity(step int) float64 {
	phase := 2 * math.Pi * float64(step%pulseSteps) / pulseSteps
	return pulseMin + (1-pulseMin)*(1+math.Cos(phase))/2
}

func (p *pulser) opacity() float64 {
	if !animationsEnabled() {
		return 1
	}
	return pulseOpacity(p.step)
}

func (p *pulser) start() {
	if p.timer != 0 {
		return
	}
	// gtkx's timer, unlike glib's, allocates nothing as it ticks, several times a
	// second for as long as a session is busy.
	p.timer = gtkx.TimeoutAdd(pulsePeriod/pulseSteps, func() bool {
		p.step = (p.step + 1) % pulseSteps
		opacity := p.opacity()
		for w := range p.shown {
			w.SetOpacity(opacity)
		}
		return true
	})
}

func (p *pulser) stop() {
	if p.timer != 0 {
		p.timer.Remove()
		p.timer = 0
	}
}

// animations is whether the desktop wants animations, once known, kept up to date as
// the setting changes. The pulse timer asks several times a second, and reading the
// setting each time made garbage that added up on long runs. It's only used on the main
// thread.
var animations struct {
	known, enabled bool
}

// animationsEnabled is whether the desktop wants animations, as GTK's own do.
func animationsEnabled() bool {
	if animations.known {
		return animations.enabled
	}
	settings, err := gtk.SettingsGetDefault()
	if err != nil {
		return true
	}
	read := func() {
		animations.enabled = true
		if v, err := settings.GetProperty("gtk-enable-animations"); err == nil {
			if enabled, ok := v.(bool); ok {
				animations.enabled = enabled
			}
		}
	}
	read()
	settings.Connect("notify::gtk-enable-animations", read)
	animations.known = true
	return animations.enabled
}
