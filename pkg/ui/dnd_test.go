package ui

import "testing"

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
