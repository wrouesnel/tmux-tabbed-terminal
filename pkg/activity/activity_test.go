package activity_test

import (
	"testing"
	"time"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/activity"
)

func TestTracker(t *testing.T) {
	start := time.Unix(1000, 0)
	tr := activity.NewTracker(2 * time.Second)

	// The first observation is a baseline only.
	tr.Observe("$1", start, false, start)
	if s := tr.State("$1", start); s.Active || s.Unseen {
		t.Fatalf("baseline: got %+v, want idle", s)
	}

	// Unchanged activity stays idle.
	now := start.Add(time.Second)
	tr.Observe("$1", start, false, now)
	if s := tr.State("$1", now); s.Active {
		t.Fatalf("unchanged: got %+v, want idle", s)
	}

	// New output while hidden is active and unseen.
	now = start.Add(2 * time.Second)
	tr.Observe("$1", start.Add(2*time.Second), false, now)
	if s := tr.State("$1", now); !s.Active || !s.Unseen {
		t.Fatalf("output while hidden: got %+v, want active and unseen", s)
	}

	// Activity times out, but unseen stays until it's shown.
	later := now.Add(3 * time.Second)
	if s := tr.State("$1", later); s.Active || !s.Unseen {
		t.Fatalf("after timeout: got %+v, want idle and unseen", s)
	}
	tr.Observe("$1", start.Add(2*time.Second), true, later)
	if s := tr.State("$1", later); s.Unseen {
		t.Fatalf("after shown: got %+v, want seen", s)
	}

	// Output while visible is active but never unseen.
	now = later.Add(time.Second)
	tr.Observe("$1", now, true, now)
	if s := tr.State("$1", now); !s.Active || s.Unseen {
		t.Fatalf("output while visible: got %+v, want active and seen", s)
	}
}

func TestTrackerMarkSeenAndRetain(t *testing.T) {
	start := time.Unix(1000, 0)
	tr := activity.NewTracker(time.Second)
	tr.Observe("$1", start, false, start)
	tr.Observe("$2", start, false, start)
	tr.Observe("$1", start.Add(time.Second), false, start.Add(time.Second))

	tr.MarkSeen("$1")
	if s := tr.State("$1", start.Add(time.Second)); s.Unseen {
		t.Fatalf("MarkSeen: got %+v", s)
	}

	tr.Retain(map[string]bool{"$2": true})
	// A forgotten session starts again from a baseline.
	tr.Observe("$1", start.Add(5*time.Second), false, start.Add(5*time.Second))
	if s := tr.State("$1", start.Add(5*time.Second)); s.Active {
		t.Fatalf("after Retain: got %+v, want baseline", s)
	}
}
