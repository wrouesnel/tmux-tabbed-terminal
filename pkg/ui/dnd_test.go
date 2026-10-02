package ui

import (
	"testing"
	"time"
)

func TestZoneAt(t *testing.T) {
	cases := []struct {
		x, y int
		want dropZone
	}{
		{50, 50, zoneCenter},
		{5, 50, zoneLeft},
		{95, 50, zoneRight},
		{50, 3, zoneTop},
		{50, 97, zoneBottom},
		{2, 10, zoneLeft}, // nearer the left than the top
		{10, 2, zoneTop},  // nearer the top than the left
		{30, 30, zoneCenter},
	}
	for _, c := range cases {
		if got := zoneAt(c.x, c.y, 100, 100); got != c.want {
			t.Errorf("zoneAt(%d, %d): got %d, want %d", c.x, c.y, got, c.want)
		}
	}
	if got := zoneAt(1, 1, 0, 0); got != zoneCenter {
		t.Errorf("unallocated pane: got %d", got)
	}
}

func TestScrollbackPath(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/data")
	at := time.Date(2026, 10, 3, 9, 5, 7, 0, time.UTC)
	got := scrollbackPath("db via bastion", "my/session", at)
	want := "/data/tmux-tabbed-terminal/scrollback/db_via_bastion/my_session/2026-10-03_09-05-07.txt"
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	if fileSafe("..") != "_" || fileSafe("") != "_" {
		t.Fatal("dot names aren't made safe")
	}
}
