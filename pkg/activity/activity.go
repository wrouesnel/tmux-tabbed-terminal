// Package activity decides which tmux sessions are busy, from the last-output timestamps
// tmux reports for them.
//
// tmux keeps activity times to the second, so a session counts as active for a while
// after its timestamp last moved forward rather than only at the instant it changed.
package activity

import "time"

// State is the activity of one session.
type State struct {
	// Active is true while the session is producing output.
	Active bool
	// Unseen is true if the session produced output while it wasn't on screen.
	Unseen bool
}

type entry struct {
	activity   time.Time
	lastChange time.Time
	unseen     bool
}

// Tracker remembers session activity between polls. It isn't safe for concurrent use.
type Tracker struct {
	// Timeout is how long a session stays active after its activity time last changed.
	Timeout time.Duration
	entries map[string]*entry
}

// NewTracker returns a tracker with the given timeout.
func NewTracker(timeout time.Duration) *Tracker {
	return &Tracker{Timeout: timeout, entries: map[string]*entry{}}
}

// Observe records a session's activity time at now. The first observation of a session
// only sets its baseline, so existing sessions aren't all marked busy at startup. If
// the activity moved forward and the session isn't visible, it becomes unseen.
func (t *Tracker) Observe(id string, activity time.Time, visible bool, now time.Time) {
	e, ok := t.entries[id]
	if !ok {
		t.entries[id] = &entry{activity: activity}
		return
	}
	if activity.After(e.activity) {
		e.activity = activity
		e.lastChange = now
		if !visible {
			e.unseen = true
		}
	}
	if visible {
		e.unseen = false
	}
}

// MarkSeen clears the unseen flag of a session, as when it's shown.
func (t *Tracker) MarkSeen(id string) {
	if e, ok := t.entries[id]; ok {
		e.unseen = false
	}
}

// State returns the activity state of a session at now.
func (t *Tracker) State(id string, now time.Time) State {
	e, ok := t.entries[id]
	if !ok {
		return State{}
	}
	return State{
		Active: !e.lastChange.IsZero() && now.Sub(e.lastChange) < t.Timeout,
		Unseen: e.unseen,
	}
}

// Retain forgets every session not in ids.
func (t *Tracker) Retain(ids map[string]bool) {
	for id := range t.entries {
		if !ids[id] {
			delete(t.entries, id)
		}
	}
}
