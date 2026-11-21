package gtkx

/*
#include <stdint.h>
#include <glib.h>
*/
import "C"

import (
	"runtime/cgo"
	"time"
)

// Timer is a function the main loop calls at an interval, from TimeoutAdd.
type Timer uint

// TimeoutAdd calls fn on the GLib main loop every interval until it returns false or the
// timer is removed, as glib.TimeoutAdd does. It must be called on the main loop.
//
// It's here to keep allocations down on long runs: gotk3's TimeoutAdd calls back through
// reflection, which allocates on every tick. Adding a timer allocates once; a tick
// allocates nothing.
func TimeoutAdd(interval time.Duration, fn func() bool) Timer {
	h := cgo.NewHandle(fn)
	return Timer(addTimeout(uint(interval.Milliseconds()), uintptr(h))) //nolint:gosec // intervals are small
}

// Remove stops the timer. It must be called on the main loop, before the timer has
// stopped itself.
func (t Timer) Remove() {
	removeSource(uint(t))
}

//export tttTimeoutTick
func tttTimeoutTick(handle C.uintptr_t) C.gboolean {
	fn, _ := cgo.Handle(handle).Value().(func() bool)
	if fn != nil && fn() {
		return C.TRUE
	}
	return C.FALSE
}

//export tttTimeoutDone
func tttTimeoutDone(handle C.uintptr_t) {
	cgo.Handle(handle).Delete()
}
