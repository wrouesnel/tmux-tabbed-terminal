package termwidget

import (
	"math"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/termwidget/emulator"
)

// damage is what the screen showed when its changes were last queued for drawing, so
// that output redraws only the rows it changed. Drawing every row with Pango for each
// chunk of output takes most of a core while a program writes quickly.
type damage struct {
	// rows are a hash of each row's cells.
	rows []uint64
	// cursor is the cursor's row, or -1 when it isn't shown, and cursorCol its column.
	cursor, cursorCol int
	cursorShape       emulator.CursorShape
}

// queueChanges queues drawing the rows that changed since it was last called.
func (t *Terminal) queueChanges() {
	cols, rows := t.emu.Size()
	d := &t.damage
	full := len(d.rows) != rows
	if full {
		d.rows = make([]uint64, rows)
	}
	dirty := make([]bool, rows)
	for y := range rows {
		h := hashLine(t.emu.Line(y))
		if h != d.rows[y] {
			d.rows[y], dirty[y] = h, true
		}
	}

	cx, cy := t.emu.Cursor()
	modes := t.emu.Modes()
	if !modes.CursorVisible || cy >= rows || cx >= cols {
		cy = -1
	}
	if cy != d.cursor || cx != d.cursorCol || modes.CursorShape != d.cursorShape {
		for _, y := range []int{d.cursor, cy} {
			if y >= 0 && y < rows {
				dirty[y] = true
			}
		}
		d.cursor, d.cursorCol, d.cursorShape = cy, cx, modes.CursorShape
	}

	if full {
		t.QueueDraw()
		return
	}
	// Runs of changed rows, the width of the widget.
	width := t.GetAllocatedWidth()
	for y := 0; y < rows; {
		if !dirty[y] {
			y++
			continue
		}
		start := y
		for y < rows && dirty[y] {
			y++
		}
		top := int(math.Floor(float64(start) * t.fonts.cellH))
		bottom := int(math.Ceil(float64(y) * t.fonts.cellH))
		t.QueueDrawArea(0, top, width, bottom-top)
	}
}

// hashLine returns an FNV-1a hash of what a line shows.
func hashLine(l *emulator.Line) uint64 {
	const prime = 1099511628211
	h := uint64(14695981039346656037) //nolint:mnd // FNV's offset basis
	mix := func(v uint64) { h = (h ^ v) * prime }
	for i := range l.Cells {
		c := &l.Cells[i]
		mix(uint64(c.Rune))
		mix(uint64(c.Width) | uint64(c.Flags)<<8 | uint64(c.Link)<<32)
		mix(uint64(c.Fg) | uint64(c.Bg)<<32)
		for _, r := range c.Combining {
			mix(uint64(r))
		}
	}
	return h
}
