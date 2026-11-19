package ui

import (
	"testing"

	"github.com/gotk3/gotk3/glib"
)

// TestFinalizerQueue schedules one drain for many finalizers, runs them in batches, and
// schedules again once drained.
func TestFinalizerQueue(t *testing.T) {
	var drains []func() bool
	q := &finalizerQueue{schedule: func(d func() bool) { drains = append(drains, d) }}
	ran := 0
	const n = finalizerBatch*2 + 10
	for range n {
		q.add(glib.Finalizer(func() { ran++ }))
	}
	if len(drains) != 1 {
		t.Fatalf("%d drains scheduled for %d finalizers, want 1", len(drains), n)
	}
	rounds := 1
	for drains[0]() {
		rounds++
	}
	if ran != n || rounds != 3 {
		t.Errorf("ran %d in %d rounds, want %d in 3", ran, rounds, n)
	}
	q.add(glib.Finalizer(func() { ran++ }))
	if len(drains) != 2 {
		t.Errorf("no drain scheduled after the queue emptied")
	}
}
