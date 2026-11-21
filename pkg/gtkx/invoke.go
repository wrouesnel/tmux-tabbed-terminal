package gtkx

/*
#cgo pkg-config: gtk+-3.0
#include <glib.h>
*/
import "C"

import (
	"sync"
	"unsafe"
)

// invoked is the queue of functions Invoke has been given, for the main loop to run.
// spare is the queue before last, kept for its space.
//
//nolint:gochecknoglobals // the main loop is one
var invoked struct {
	sync.Mutex
	funcs, spare []func()
	scheduled    bool
}

// Invoke runs fn on the GLib main loop, soon, as glib.IdleAdd does, and is likewise safe
// to call from any goroutine. Functions run in the order given.
//
// It's here to keep allocations down on long runs: gotk3's IdleAdd registers a closure,
// records the caller's stack and calls back through reflection, each time, which on
// every poll of every host adds up. Invoke queues fn, and wakes the main loop through a
// plain cgo callback only if it isn't already due to run the queue: it allocates
// nothing itself.
func Invoke(fn func()) {
	invoked.Lock()
	invoked.funcs = append(invoked.funcs, fn)
	schedule := !invoked.scheduled
	invoked.scheduled = true
	invoked.Unlock()
	if schedule {
		scheduleQueued()
	}
}

//export tttRunQueued
func tttRunQueued(unsafe.Pointer) C.gboolean {
	invoked.Lock()
	funcs := invoked.funcs
	invoked.funcs, invoked.spare = invoked.spare[:0], nil
	invoked.scheduled = false
	invoked.Unlock()
	for i, fn := range funcs {
		funcs[i] = nil
		fn()
	}
	invoked.Lock()
	if invoked.spare == nil {
		invoked.spare = funcs[:0]
	}
	invoked.Unlock()
	return C.FALSE
}
