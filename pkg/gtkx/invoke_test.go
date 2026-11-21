package gtkx_test

import (
	"testing"

	"github.com/gotk3/gotk3/glib"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/gtkx"
)

func TestInvoke(t *testing.T) {
	loop := glib.MainLoopNew(nil, false)
	var got []int
	const n = 100
	go func() {
		for i := range n {
			gtkx.Invoke(func() {
				got = append(got, i)
				if i == n-1 {
					loop.Quit()
				}
			})
		}
	}()
	timeout := glib.TimeoutAdd(5000, func() bool { //nolint:mnd // ms
		t.Error("the queued functions didn't all run")
		loop.Quit()
		return false
	})
	loop.Run()
	glib.SourceRemove(timeout)
	for i, v := range got {
		if v != i {
			t.Fatalf("ran out of order: %v", got)
		}
	}
	if len(got) != n {
		t.Errorf("ran %d of %d", len(got), n)
	}

	// Queued from the main loop itself, it runs after, not during, the caller.
	ran := false
	gtkx.Invoke(func() {
		gtkx.Invoke(func() { ran = true; loop.Quit() })
		if ran {
			t.Error("ran while the function queueing it ran")
		}
	})
	loop.Run()
	if !ran {
		t.Error("function queued from the main loop didn't run")
	}
}
