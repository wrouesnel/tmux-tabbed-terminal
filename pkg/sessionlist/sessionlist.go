// Package sessionlist decides the order, grouping and filtering of the session list.
//
// Sessions are grouped by the application running in their current window, so that, for
// example, every session running claude sits together. A session's group follows its
// command only once the command has been running for a while, so a short command such as
// ls doesn't move the row back and forth.
package sessionlist

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

// ShellGroup is the group of sessions whose current window is at a shell prompt.
const ShellGroup = "shell"

// shells are commands grouped together as ShellGroup.
//
//nolint:gochecknoglobals
var shells = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true, "ksh": true,
	"mksh": true, "tcsh": true, "csh": true, "nu": true, "elvish": true, "xonsh": true,
}

// Application returns the group name for a command: its base name, with login shells'
// leading "-" removed and every shell named ShellGroup.
func Application(command string) string {
	cmd := filepath.Base(strings.TrimPrefix(strings.TrimSpace(command), "-"))
	if cmd == "" || cmd == "." || cmd == "/" {
		return ShellGroup
	}
	if shells[cmd] {
		return ShellGroup
	}
	return cmd
}

// sessionCommand returns the command of the session's current window.
func sessionCommand(s *tmux.Session) string {
	if w := s.ActiveWindow(); w != nil {
		return w.Command
	}
	return ""
}

type groupState struct {
	group        string
	pending      string
	pendingSince time.Time
}

// Grouper assigns sessions to groups and remembers them between snapshots. It isn't safe
// for concurrent use.
type Grouper struct {
	// Hold is how long a new application must run before its session changes group.
	Hold   time.Duration
	states map[string]*groupState
}

// NewGrouper returns a grouper with the given hold time.
func NewGrouper(hold time.Duration) *Grouper {
	return &Grouper{Hold: hold, states: map[string]*groupState{}}
}

// Group returns a session's group at now. A session seen for the first time goes straight
// into the group of its application.
func (g *Grouper) Group(s *tmux.Session, now time.Time) string {
	app := Application(sessionCommand(s))
	st, ok := g.states[s.ID]
	if !ok {
		g.states[s.ID] = &groupState{group: app}
		return app
	}
	switch {
	case app == st.group:
		st.pending = ""
	case app != st.pending:
		st.pending, st.pendingSince = app, now
	case now.Sub(st.pendingSince) >= g.Hold:
		st.group, st.pending = app, ""
	}
	return st.group
}

// Retain forgets every session not in ids.
func (g *Grouper) Retain(ids map[string]bool) {
	for id := range g.states {
		if !ids[id] {
			delete(g.states, id)
		}
	}
}

// Entry is one session in display order.
type Entry struct {
	ID string
	// Group is the session's group, or "" when the list isn't grouped.
	Group string
}

// Order returns the sessions in display order. Ungrouped, that's tmux's order. Grouped,
// groups are sorted by name with the shell group last, and sessions keep tmux's order
// within their group.
func Order(sessions []tmux.Session, groups map[string]string, grouped bool) []Entry {
	entries := make([]Entry, 0, len(sessions))
	for _, s := range sessions {
		e := Entry{ID: s.ID}
		if grouped {
			e.Group = groups[s.ID]
		}
		entries = append(entries, e)
	}
	if grouped {
		sort.SliceStable(entries, func(i, j int) bool {
			return groupLess(entries[i].Group, entries[j].Group)
		})
	}
	return entries
}

func groupLess(a, b string) bool {
	if (a == ShellGroup) != (b == ShellGroup) {
		return b == ShellGroup
	}
	return strings.ToLower(a) < strings.ToLower(b)
}

// Matches reports whether a session matches a search. Every word of the query must
// appear, ignoring case, in the session's name, its group, or the name or command of one
// of its windows. An empty query matches everything.
func Matches(s *tmux.Session, group string, query string) bool {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return true
	}
	haystack := []string{strings.ToLower(s.Name), strings.ToLower(group)}
	for _, w := range s.Windows {
		haystack = append(haystack, strings.ToLower(w.Name), strings.ToLower(w.Command))
	}
	for _, word := range words {
		found := false
		for _, h := range haystack {
			if strings.Contains(h, word) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
