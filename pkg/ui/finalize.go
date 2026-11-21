package ui

import (
	"sync"

	"github.com/gotk3/gotk3/glib"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/gtkx"
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
	// schedule has the main loop call drain soon.
	schedule func(drain func())

	mu sync.Mutex
	// pending is the finalizers queued, and spare the space of the batch run last,
	// kept so that queueing allocates nothing once warm.
	pending, spare []glib.Released
	scheduled      bool
	// drainFunc is drain as a func value, made once.
	drainFunc func()
}

// finalizerFunc is a finalizer as a glib.Released. A func is pointer-shaped, so making
// one an interface allocates nothing.
type finalizerFunc glib.Finalizer

func (f finalizerFunc) ReleaseFinalized() { f() }

// addFinalizer queues a finalizer, as gotk3's FinalizerStrategy.
func (q *finalizerQueue) addFinalizer(f glib.Finalizer) { q.add(finalizerFunc(f)) }

// add queues what a finalizer releases, as gotk3's ReleaseStrategy, scheduling a drain if
// none is pending.
func (q *finalizerQueue) add(f glib.Released) {
	q.mu.Lock()
	q.pending = append(q.pending, f)
	schedule := !q.scheduled
	q.scheduled = true
	q.mu.Unlock()
	if schedule {
		if q.drainFunc == nil {
			q.drainFunc = func() { q.drain() }
		}
		q.schedule(q.drainFunc)
	}
}

// drain runs up to a batch of the queued finalizers, scheduling itself again if more are
// left, which it reports.
func (q *finalizerQueue) drain() bool {
	q.mu.Lock()
	n := min(len(q.pending), finalizerBatch)
	batch := q.pending[:n:n]
	more := n < len(q.pending)
	if more {
		q.pending = q.pending[n:]
	} else {
		// The queue starts again in the spare space, and this batch's becomes the spare
		// once it's run.
		q.pending, q.spare = q.spare[:0], nil
		q.scheduled = false
	}
	q.mu.Unlock()
	for i, f := range batch {
		batch[i] = nil
		f.ReleaseFinalized()
	}
	q.mu.Lock()
	if !more && q.spare == nil {
		q.spare = batch[:0:cap(batch)]
	}
	q.mu.Unlock()
	if more {
		q.schedule(q.drainFunc)
	}
	return more
}

// installFinalizerQueue has gotk3 release GObjects on the main loop, in batches.
func installFinalizerQueue() {
	// gtkx.Invoke, unlike glib.IdleAdd, allocates nothing to schedule a drain.
	q := &finalizerQueue{schedule: gtkx.Invoke}
	glib.FinalizerStrategy = q.addFinalizer
	// Objects and values come as themselves, so no function value is made for each.
	glib.ReleaseStrategy = q.add
}
