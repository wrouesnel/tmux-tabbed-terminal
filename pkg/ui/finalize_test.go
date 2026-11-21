package ui

import (
	"testing"

	"github.com/gotk3/gotk3/glib"
)

// TestFinalizerQueue schedules one drain for many finalizers, runs them in batches,
// scheduling itself again while more are left, and schedules again once drained.
func TestFinalizerQueue(t *testing.T) {
	var scheduled []func()
	q := &finalizerQueue{schedule: func(d func()) { scheduled = append(scheduled, d) }}
	ran := 0
	const n = finalizerBatch*2 + 10
	for range n {
		q.addFinalizer(glib.Finalizer(func() { ran++ }))
	}
	if len(scheduled) != 1 {
		t.Fatalf("%d drains scheduled for %d finalizers, want 1", len(scheduled), n)
	}
	for i := 0; i < len(scheduled); i++ {
		scheduled[i]()
	}
	if ran != n || len(scheduled) != 3 {
		t.Errorf("ran %d in %d rounds, want %d in 3", ran, len(scheduled), n)
	}
	q.addFinalizer(glib.Finalizer(func() { ran++ }))
	if len(scheduled) != 4 {
		t.Errorf("no drain scheduled after the queue emptied")
	}
}

// TestFinalizerQueueDoesntAllocate checks a warm queue reuses its space.
func TestFinalizerQueueDoesntAllocate(t *testing.T) {
	var next func()
	q := &finalizerQueue{schedule: func(d func()) { next = d }}
	f := glib.Finalizer(func() {})
	o := &glib.Object{}
	round := func() {
		for range 50 {
			q.addFinalizer(f)
			q.add(released{o})
		}
		next()
	}
	round()
	round()
	if allocs := testing.AllocsPerRun(10, round); allocs != 0 {
		t.Errorf("a round allocated %v objects", allocs)
	}
}

// released stands for an object a finalizer releases, without unreffing anything.
type released struct{ *glib.Object }

func (released) ReleaseFinalized() {}
