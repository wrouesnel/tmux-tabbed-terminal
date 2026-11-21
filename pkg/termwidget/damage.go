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
	// dirty marks the rows changed, kept between calls.
	dirty []bool
}

// queueChanges queues drawing the rows that changed since it was last called. When the
// screen scrolled, what the backing shows is moved first, with the rows' hashes, so that
// only the rows which then differ are rendered again.
//
//nolint:funlen,cyclop // one pass over the screen
func (t *Terminal) queueChanges() {
	cols, rows := t.emu.Size()
	d := &t.damage
	b := &t.back
	full := len(d.rows) != rows
	if full {
		d.rows = make([]uint64, rows)
		b.invalidate()
	}
	// The selection is drawn where it is on the screen, not with the text, so what's
	// drawn isn't moved while there is one.
	top, bottom, n, scrolled := t.emu.TakeScroll()
	scrolled = scrolled && !full && !t.sel.active() && bottom < rows &&
		b.scroll(t.fonts.cellH, top, bottom, n)
	if scrolled {
		// Hashes no row has, for the rows moved in.
		shiftRows(d.rows[top:bottom+1], n, 0)
	}
	// The rows changed are marked in space kept between calls: output is handed over many
	// times a second.
	if cap(d.dirty) < rows {
		d.dirty = make([]bool, rows)
	}
	dirty := d.dirty[:rows]
	clear(dirty)
	for y := range rows {
		h := hashLine(t.emu.Line(y))
		if h != d.rows[y] {
			d.rows[y], dirty[y] = h, true
			b.mark(y, rows)
		}
	}

	// The cursor is drawn over the backing: moving it only needs its rows drawn.
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

	if full || scrolled {
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

// redrawAll renders and draws the whole screen again, for a change to how every row is
// drawn, such as its colors, its font or the selection.
func (t *Terminal) redrawAll() {
	t.back.invalidate()
	t.QueueDraw()
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
