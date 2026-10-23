// Package vte is the terminal widget panes show tmux clients in, as a gotk3 widget.
//
// On Linux and other Unix systems it binds the parts of the VTE terminal widget
// (vte-2.91, GTK3) this program uses. Child processes are started with os/exec on a pty
// created by VTE, rather than through vte_terminal_spawn_async, so that Go owns the
// process and knows its tty.
//
// VTE doesn't support Windows, where the widget is a termwidget.Terminal instead. Build
// with the nativeterm tag to use that on Linux too.
package vte

import (
	"strings"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/termwidget/emulator"
)

// Color is an RGBA color with components from 0 to 1.
type Color struct {
	R, G, B, A float64
}

// Match is a link under the pointer: an OSC 8 hyperlink's URI, or the text of an
// AddMatch regex's match and that regex's tag.
type Match struct {
	Text      string
	Tag       int
	Hyperlink bool
}

// CellsText returns the text in a run of cells of a screen cols wide, counted in reading
// order from start to end inclusive, given the screen's text: its lines, one per line
// of output, which wrap onto as many rows as their width needs. A wide character that
// doesn't fit at the end of a row starts the next, as in the terminal.
func CellsText(screen string, cols, start, end int) string {
	if cols <= 0 || start > end {
		return ""
	}
	var out strings.Builder
	pos := 0
	for _, line := range strings.Split(screen, "\n") {
		if pos > end {
			break
		}
		lineStart := pos
		for _, r := range line {
			w := emulator.RuneWidth(r)
			if w == 2 && pos%cols == cols-1 { //nolint:mnd // a wide character
				pos++
			}
			switch {
			case w == 0:
				// A combining mark, on the character before it.
				if pos > start && pos-1 <= end {
					out.WriteRune(r)
				}
			case pos >= start && pos <= end:
				out.WriteRune(r)
			}
			pos += w
		}
		// Blank cells to the end of the line's last row, where the text was trimmed.
		rows := max(1, (pos-lineStart+cols-1)/cols)
		next := lineStart + rows*cols
		for ; pos < next; pos++ {
			if pos >= start && pos <= end {
				out.WriteByte(' ')
			}
		}
	}
	return strings.TrimRight(out.String(), " ")
}
