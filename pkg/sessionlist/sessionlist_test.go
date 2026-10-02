package sessionlist_test

import (
	"testing"
	"time"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/sessionlist"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

func session(id, name, command string) tmux.Session {
	return tmux.Session{ID: id, Name: name, Windows: []tmux.Window{{Active: true, Name: command, Command: command}}}
}

func TestApplication(t *testing.T) {
	cases := map[string]string{
		"claude":         "claude",
		"bash":           sessionlist.ShellGroup,
		"-zsh":           sessionlist.ShellGroup,
		"/usr/bin/vim":   "vim",
		"":               sessionlist.ShellGroup,
		"  python3.12  ": "python3.12",
	}
	for in, want := range cases {
		if got := sessionlist.Application(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestGrouperHoldsGroupThroughShortCommands(t *testing.T) {
	g := sessionlist.NewGrouper(3 * time.Second)
	start := time.Unix(1000, 0)
	s := session("$1", "work", "bash")

	if got := g.Group(&s, start); got != sessionlist.ShellGroup {
		t.Fatalf("first: got %q", got)
	}
	// A short command doesn't move the session.
	s.Windows[0].Command = "ls"
	if got := g.Group(&s, start.Add(time.Second)); got != sessionlist.ShellGroup {
		t.Fatalf("short command: got %q", got)
	}
	s.Windows[0].Command = "bash"
	g.Group(&s, start.Add(2*time.Second))

	// A command running past the hold time does.
	s.Windows[0].Command = "claude"
	if got := g.Group(&s, start.Add(3*time.Second)); got != sessionlist.ShellGroup {
		t.Fatalf("new command: got %q, want it held", got)
	}
	if got := g.Group(&s, start.Add(6*time.Second)); got != "claude" {
		t.Fatalf("after hold: got %q, want claude", got)
	}

	g.Retain(map[string]bool{})
	s.Windows[0].Command = "vim"
	if got := g.Group(&s, start.Add(7*time.Second)); got != "vim" {
		t.Fatalf("after Retain: got %q, want vim straight away", got)
	}
}

func TestOrder(t *testing.T) {
	sessions := []tmux.Session{
		session("$1", "a", "bash"),
		session("$2", "b", "claude"),
		session("$3", "c", "vim"),
		session("$4", "d", "claude"),
	}
	groups := map[string]string{"$1": sessionlist.ShellGroup, "$2": "claude", "$3": "vim", "$4": "claude"}

	ids := func(entries []sessionlist.Entry) string {
		s := ""
		for _, e := range entries {
			s += e.ID
		}
		return s
	}
	if got := ids(sessionlist.Order(sessions, groups, false)); got != "$1$2$3$4" {
		t.Errorf("ungrouped: got %s", got)
	}
	grouped := sessionlist.Order(sessions, groups, true)
	if got := ids(grouped); got != "$2$4$3$1" {
		t.Errorf("grouped: got %s, want claude, vim, then shells", got)
	}
	if grouped[0].Group != "claude" || grouped[3].Group != sessionlist.ShellGroup {
		t.Errorf("groups: got %+v", grouped)
	}
}

func TestMatches(t *testing.T) {
	s := session("$1", "zfs-bug-hunt", "claude")
	s.Windows = append(s.Windows, tmux.Window{Name: "logs", Command: "journalctl"})
	for query, want := range map[string]bool{
		"":             true,
		"zfs":          true,
		"ZFS hunt":     true,
		"claude":       true,
		"journal":      true,
		"zfs nothing":  false,
		"kubernetes":   false,
		"   bug   zfs": true,
		"buildbox":     true,
	} {
		if got := sessionlist.Matches(&s, query, "claude", "buildbox"); got != want {
			t.Errorf("%q: got %v, want %v", query, got, want)
		}
	}
}
