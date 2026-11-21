package termwidget

import (
	"math"

	"github.com/gotk3/gotk3/cairo"
	"github.com/gotk3/gotk3/pango"
)

// backing is the screen as rendered, kept between draws, so that a draw paints it and
// renders only the rows that changed. When output scrolls, what was rendered is moved
// rather than rendered again: laying out every row with Pango as a program writes a
// line was most of the time the terminal took to draw.
//
// A row is rendered from its cells, and the damage tracker's hash of each row is of
// what the backing shows there: a scroll moves the hashes as it moves the pixels, so a
// row whose cells then differ is rendered again, whatever else happened. The cursor is
// drawn over the backing on each draw, as it isn't a row's content.
type backing struct {
	// surface is the backing, and cr draws on it. spare and spareCr are a second, for
	// moving what's drawn: the two swap.
	surface, spare *cairo.Surface
	cr, spareCr    *cairo.Context
	width, height  int
	scale          int
	// dirty marks the rows to render before the next paint; all marks every row, and the
	// space below the last.
	dirty []bool
	all   bool
}

// invalidate has the whole backing rendered again, as when the colors, font or selection
// change.
func (b *backing) invalidate() { b.all = true }

// mark has rows y-1 to y+1 rendered again: a row's text can overhang its neighbours.
func (b *backing) mark(y, rows int) {
	if len(b.dirty) != rows {
		b.dirty = make([]bool, rows)
		b.all = true
	}
	for r := max(y-1, 0); r <= min(y+1, rows-1); r++ {
		b.dirty[r] = true
	}
}

// ensure makes the backing the widget's size and scale, reporting whether there is one:
// there isn't until the widget is realized.
func (b *backing) ensure(t *Terminal) bool {
	w, h, scale := t.GetAllocatedWidth(), t.GetAllocatedHeight(), t.GetScaleFactor()
	if b.surface != nil && w == b.width && h == b.height && scale == b.scale {
		return true
	}
	if w <= 0 || h <= 0 {
		return false
	}
	win, err := t.GetWindow()
	if err != nil || win == nil {
		return false
	}
	surface, err := win.CreateSimilarSurface(cairo.CONTENT_COLOR, w, h)
	if err != nil {
		return false
	}
	spare, err := win.CreateSimilarSurface(cairo.CONTENT_COLOR, w, h)
	if err != nil {
		return false
	}
	b.surface, b.spare = surface, spare
	b.cr, b.spareCr = cairo.Create(surface), cairo.Create(spare)
	b.width, b.height, b.scale = w, h, scale
	b.all = true
	return true
}

// scroll moves what's rendered of rows top to bottom up n rows, or down if n is negative,
// with the rows' marks. It reports whether it did: it can't before there's a backing.
func (b *backing) scroll(cellH float64, top, bottom, n int) bool {
	if b.surface == nil || b.all || len(b.dirty) <= bottom || top < 0 || top > bottom {
		return false
	}
	// Everything is copied to the spare, then the region over it again, moved, and the
	// two swap: cairo can't copy a surface onto itself.
	cr := b.spareCr
	cr.SetSourceSurface(b.surface, 0, 0)
	cr.Paint()
	cr.Save()
	y0, y1 := math.Floor(float64(top)*cellH), math.Ceil(float64(bottom+1)*cellH)
	cr.Rectangle(0, y0, float64(b.width), y1-y0)
	cr.Clip()
	cr.SetSourceSurface(b.surface, 0, -float64(n)*cellH)
	cr.Paint()
	cr.Restore()
	b.surface, b.spare = b.spare, b.surface
	b.cr, b.spareCr = b.spareCr, b.cr
	shiftRows(b.dirty[top:bottom+1], n, true)
	return true
}

// shiftRows moves rows up n places, or down if n is negative, filling those moved in
// with fill.
func shiftRows[T any](rows []T, n int, fill T) {
	h := len(rows)
	switch {
	case n >= h || -n >= h:
		for i := range rows {
			rows[i] = fill
		}
	case n > 0:
		copy(rows, rows[n:])
		for i := h - n; i < h; i++ {
			rows[i] = fill
		}
	case n < 0:
		copy(rows[-n:], rows[:h+n])
		for i := range -n {
			rows[i] = fill
		}
	}
}

// render renders the rows marked, and everything if all is set.
func (b *backing) render(t *Terminal) {
	cols, rows := t.emu.Size()
	f := &t.fonts
	cr := b.cr
	if len(b.dirty) != rows {
		b.dirty = make([]bool, rows)
		b.all = true
	}
	layout := t.layoutFor(cr)
	bg := t.theme.bg
	if b.all {
		cr.SetSourceRGBA(bg.R, bg.G, bg.B, bg.A)
		cr.Paint()
		for y := range rows {
			t.drawRow(cr, layout, y, cols)
		}
		b.all = false
		clear(b.dirty)
		return
	}
	for y := range rows {
		if !b.dirty[y] {
			continue
		}
		b.dirty[y] = false
		top := float64(y) * f.cellH
		cr.Save()
		cr.Rectangle(0, top, float64(b.width), f.cellH)
		cr.Clip()
		cr.SetSourceRGBA(bg.R, bg.G, bg.B, bg.A)
		cr.Paint()
		// The rows each side too, for their text overhanging this one.
		for r := max(y-1, 0); r <= min(y+1, rows-1); r++ {
			t.drawRow(cr, layout, r, cols)
		}
		cr.Restore()
	}
}

// layoutFor returns the terminal's Pango layout, up to date for cr. One is kept: pango's
// CairoCreateLayout makes a new one each time, which gotk3 never frees.
func (t *Terminal) layoutFor(cr *cairo.Context) *pango.Layout {
	if t.layout == nil {
		t.layout = pango.CairoCreateLayout(cr)
	} else {
		pango.CairoUpdateLayout(cr, t.layout)
	}
	return t.layout
}
