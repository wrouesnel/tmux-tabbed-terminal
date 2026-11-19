package ui

import (
	"sync"

	"github.com/gotk3/gotk3/glib"
)

// finalizerBatch is how many GObject releases run in one go on the main loop, so a
// large backlog doesn't hold up drawing and input.
const finalizerBatch = 4096

// finalizerQueue runs gotk3's finalizers, which release GObject references, on the main
// loop in batches. gotk3 calls its finalizers from the Go runtime's finalizer goroutine,
// where unreffing a widget isn't safe. Adding one idle source per finalizer, though,
// wakes the main loop once per object, which at tens of thousands of objects a second
// is a loop of its own (issue #15). The queue keeps at most one idle source pending.
type finalizerQueue struct {
	// schedule adds the drain to the main loop, which calls it until it returns false.
	schedule func(drain func() bool)

	mu        sync.Mutex
	pending   []glib.Finalizer
	scheduled bool
}

// add queues a finalizer, scheduling a drain if none is pending.
func (q *finalizerQueue) add(f glib.Finalizer) {
	q.mu.Lock()
	q.pending = append(q.pending, f)
	schedule := !q.scheduled
	q.scheduled = true
	q.mu.Unlock()
	if schedule {
		q.schedule(q.drain)
	}
}

// drain runs up to a batch of the queued finalizers, and reports whether more are left.
func (q *finalizerQueue) drain() bool {
	q.mu.Lock()
	n := min(len(q.pending), finalizerBatch)
	batch := q.pending[:n:n]
	q.pending = q.pending[n:]
	more := len(q.pending) > 0
	if !more {
		q.pending, q.scheduled = nil, false
	}
	q.mu.Unlock()
	for _, f := range batch {
		f()
	}
	return more
}

// installFinalizerQueue has gotk3 release GObjects on the main loop, in batches.
func installFinalizerQueue() {
	q := &finalizerQueue{schedule: func(drain func() bool) { glib.IdleAdd(drain) }}
	glib.FinalizerStrategy = q.add
}
