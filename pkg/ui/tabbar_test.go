package ui

import (
	"testing"
	"time"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

func TestWindowKey(t *testing.T) {
	// A session and a window with the same number on one host have different keys.
	if windowKey(LocalHost, "@3") == sessionKey(LocalHost, "$3") {
		t.Fatal("window and session keys collide")
	}
	if windowKey(LocalHost, "@3") == windowKey("admin@db", "@3") {
		t.Fatal("keys of the same window ID on different hosts are equal")
	}
}

func TestWindowTooltip(t *testing.T) {
	now := time.Unix(1000, 0)
	win := &tmux.Window{
		Index:    2,
		Name:     "build",
		Title:    "make -j8",
		Command:  "make",
		Path:     "/src",
		Activity: now.Add(-5 * time.Second),
	}
	want := "2: build\nmake -j8\nmake in /src\nLast output 5s ago"
	if got := windowTooltip(win, now); got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	bare := &tmux.Window{Index: 0, Name: "bash"}
	if got := windowTooltip(bare, now); got != "0: bash" {
		t.Errorf("got %q, want %q", got, "0: bash")
	}
}
