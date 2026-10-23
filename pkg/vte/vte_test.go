package vte_test

import (
	"testing"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/vte"
)

// TestSixelSupported checks the feature check runs against the installed VTE, whichever
// way it was built. It needs no display.
func TestSixelSupported(t *testing.T) {
	t.Logf("VTE built with SIXEL support: %v", vte.SixelSupported())
}

func TestCellsText(t *testing.T) {
	// A screen 10 wide: a line that wraps onto a second row, a blank line, a line with a
	// wide character that doesn't fit at the end of its row, and a combining mark.
	screen := "see my link here\n\nabcdefghi漢x\né ok"
	cases := []struct {
		name       string
		start, end int
		want       string
	}{
		{"one row", 4, 10, "my link"},
		{"across the wrap", 7, 15, "link here"},
		{"blank line", 20, 29, ""},
		{"wide character on the next row", 38, 42, "i漢x"},
		{"combining mark", 50, 53, "é ok"},
		{"past the end", 60, 70, ""},
	}
	for _, c := range cases {
		if got := vte.CellsText(screen, 10, c.start, c.end); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
