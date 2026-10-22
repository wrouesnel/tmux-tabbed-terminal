package termwidget

import (
	"math"

	"github.com/gotk3/gotk3/cairo"
	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/pango"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/termwidget/emulator"
)

// Color is an RGBA color with components from 0 to 1.
type Color struct {
	R, G, B, A float64
}

// theme is the terminal's colors.
type theme struct {
	fg, bg     Color
	palette    [256]Color
	cursor     *Color
	cursorFg   *Color
	boldBright bool
}

// xtermPalette returns xterm's 256 colors: 16 ANSI colors, a 6×6×6 cube and a ramp of
// greys.
func xtermPalette() [256]Color {
	var p [256]Color
	ansi := [16][3]uint8{
		{0, 0, 0}, {205, 0, 0}, {0, 205, 0}, {205, 205, 0}, {0, 0, 238}, {205, 0, 205}, {0, 205, 205}, {229, 229, 229},
		{127, 127, 127}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0}, {92, 92, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255},
	}
	for i, c := range ansi {
		p[i] = rgb8(c[0], c[1], c[2])
	}
	level := func(i int) uint8 {
		if i == 0 {
			return 0
		}
		return uint8(55 + i*40) //nolint:gosec,mnd // xterm's cube levels
	}
	for i := range 216 {
		p[16+i] = rgb8(level(i/36), level(i/6%6), level(i%6))
	}
	for i := range 24 {
		g := uint8(8 + i*10) //nolint:gosec,mnd // xterm's grey ramp
		p[232+i] = rgb8(g, g, g)
	}
	return p
}

func rgb8(r, g, b uint8) Color {
	return Color{float64(r) / 255, float64(g) / 255, float64(b) / 255, 1} //nolint:mnd // bytes to fractions
}

func defaultTheme() theme {
	return theme{fg: Color{0.9, 0.9, 0.9, 1}, bg: Color{0, 0, 0, 1}, palette: xtermPalette()}
}

// SetColors sets the foreground, background and palette. The palette has 0, 8, 16, 232
// or 256 entries, which replace the start of xterm's. An empty palette restores xterm's.
func (t *Terminal) SetColors(fg, bg Color, palette []Color) {
	t.theme.fg, t.theme.bg = fg, bg
	t.theme.palette = xtermPalette()
	copy(t.theme.palette[:], palette)
	t.applyThemeToEmulator()
	t.QueueDraw()
}

// SetCursorColors sets the cursor color and the color of text under it. A nil color
// restores the default, which is the reverse of the text.
func (t *Terminal) SetCursorColors(cursor, cursorFg *Color) {
	t.theme.cursor, t.theme.cursorFg = cursor, cursorFg
	t.QueueDraw()
}

// SetBoldIsBright sets whether bold text uses the bright palette colors.
func (t *Terminal) SetBoldIsBright(b bool) {
	t.theme.boldBright = b
	t.QueueDraw()
}

func (t *Terminal) applyThemeToEmulator() {
	to8 := func(c Color) [3]uint8 {
		//nolint:gosec,mnd // fractions to bytes
		return [3]uint8{uint8(c.R * 255), uint8(c.G * 255), uint8(c.B * 255)}
	}
	t.emu.DefaultFg, t.emu.DefaultBg = to8(t.theme.fg), to8(t.theme.bg)
}

// resolve returns a cell color.
func (t *Terminal) resolve(c emulator.Color, def Color, bold bool) Color {
	if i, ok := c.Index(); ok {
		if bold && t.theme.boldBright && i < 8 {
			i += 8
		}
		return t.theme.palette[i]
	}
	if r, g, b, ok := c.RGB(); ok {
		return rgb8(r, g, b)
	}
	return def
}

// cellColors returns the colors a cell's text and background are drawn in.
func (t *Terminal) cellColors(s emulator.Style, selected bool) (Color, Color) {
	fg := t.resolve(s.Fg, t.theme.fg, s.Flags&emulator.Bold != 0)
	bg := t.resolve(s.Bg, t.theme.bg, false)
	if s.Flags&emulator.Dim != 0 {
		fg = Color{(fg.R + bg.R) / 2, (fg.G + bg.G) / 2, (fg.B + bg.B) / 2, fg.A}
	}
	if (s.Flags&emulator.Reverse != 0) != selected {
		fg, bg = bg, fg
	}
	if s.Flags&emulator.Invisible != 0 {
		fg = bg
	}
	return fg, bg
}

// fonts are the font in its four styles, and the size of a cell.
type fonts struct {
	desc         string
	scale        float64
	regular      *pango.FontDescription
	bold, italic *pango.FontDescription
	boldItalic   *pango.FontDescription
	cellW, cellH float64
}

func (f *fonts) styled(flags emulator.Flags) *pango.FontDescription {
	switch {
	case flags&emulator.Bold != 0 && flags&emulator.Italic != 0:
		return f.boldItalic
	case flags&emulator.Bold != 0:
		return f.bold
	case flags&emulator.Italic != 0:
		return f.italic
	}
	return f.regular
}

// SetFont sets the font from a Pango font description such as "Monospace 11".
func (t *Terminal) SetFont(desc string) {
	t.fonts.desc = desc
	t.loadFonts()
}

// SetFontScale sets the font zoom factor.
func (t *Terminal) SetFontScale(scale float64) {
	t.fonts.scale = scale
	t.loadFonts()
}

// FontScale returns the font zoom factor.
func (t *Terminal) FontScale() float64 {
	if t.fonts.scale == 0 {
		return 1
	}
	return t.fonts.scale
}

// loadFonts makes the font's styles and measures a cell.
func (t *Terminal) loadFonts() {
	f := &t.fonts
	base := pango.FontDescriptionFromString(f.desc)
	// Cairo lays text out at 96 DPI; GTK at the screen's resolution.
	scale := t.FontScale()
	if screen, err := gdk.ScreenGetDefault(); err == nil {
		if res := screen.GetResolution(); res > 0 {
			scale *= res / 96 //nolint:mnd // Cairo's resolution
		}
	}
	base.SetSize(int(float64(base.GetSize()) * scale))
	f.regular = base
	f.bold = base.Copy()
	f.bold.SetWeight(pango.WEIGHT_BOLD)
	f.italic = base.Copy()
	f.italic.SetStyle(pango.STYLE_ITALIC)
	f.boldItalic = f.bold.Copy()
	f.boldItalic.SetStyle(pango.STYLE_ITALIC)

	surface := cairo.CreateImageSurface(cairo.FORMAT_ARGB32, 1, 1)
	layout := pango.CairoCreateLayout(cairo.Create(surface))
	layout.SetFontDescription(f.regular)
	const sample = "MMMMMMMMMM"
	layout.SetText(sample, -1)
	w, h := layout.GetSize()
	f.cellW = float64(w) / pango.PANGO_SCALE / float64(len(sample))
	f.cellH = math.Ceil(float64(h) / pango.PANGO_SCALE)
	f.cellW, f.cellH = max(f.cellW, 1), max(f.cellH, 1)

	if !t.sizeRequested {
		t.setMinimumSize()
	}
	t.fitToAllocation()
	t.QueueDraw()
}

// draw draws the screen.
//
//nolint:funlen,cyclop // one pass over the screen
func (t *Terminal) draw(_ interface{}, cr *cairo.Context) bool {
	f := &t.fonts
	cols, rows := t.emu.Size()
	setColor := func(c Color) { cr.SetSourceRGBA(c.R, c.G, c.B, c.A) }
	setColor(t.theme.bg)
	cr.Paint()

	layout := pango.CairoCreateLayout(cr)
	cx, cy := t.emu.Cursor()
	modes := t.emu.Modes()
	focused := t.HasFocus()

	for y := range rows {
		line := t.emu.Line(y)
		top := float64(y) * f.cellH
		// Backgrounds first, so that text overhanging its cell isn't painted over.
		for x := 0; x < cols; x++ {
			c := &line.Cells[x]
			if c.Width == 0 {
				continue
			}
			_, bg := t.cellColors(c.Style, t.sel.contains(x, y))
			if bg != t.theme.bg {
				setColor(bg)
				cr.Rectangle(math.Floor(float64(x)*f.cellW), top, math.Ceil(float64(c.Width)*f.cellW), f.cellH)
				cr.Fill()
			}
		}
		// Then text, in runs of ASCII cells of the same style, which a monospace font
		// lays out on the grid. Other characters are placed in their cells one by one.
		for x := 0; x < cols; {
			c := &line.Cells[x]
			if c.Width == 0 || (c.Rune == ' ' && c.Flags&(emulator.Underline|emulator.Strike) == 0) {
				x++
				continue
			}
			selected := t.sel.contains(x, y)
			fg, _ := t.cellColors(c.Style, selected)
			start := x
			text := c.Text()
			x += int(c.Width)
			if isPlain(c) {
				for x < cols {
					n := &line.Cells[x]
					if !isPlain(n) || n.Style != c.Style || t.sel.contains(x, y) != selected {
						break
					}
					text += string(n.Rune)
					x++
				}
			}
			left := float64(start) * f.cellW
			setColor(fg)
			layout.SetFontDescription(f.styled(c.Flags))
			layout.SetText(text, -1)
			cr.MoveTo(left, top)
			pango.CairoShowLayout(cr, layout)
			width := float64(x-start) * f.cellW
			if c.Flags&emulator.Underline != 0 || c.Link != 0 && c.Link == t.matches.hoverLink {
				cr.Rectangle(left, top+f.cellH-2, width, 1)
				cr.Fill()
			}
			if c.Flags&emulator.Strike != 0 {
				cr.Rectangle(left, top+math.Floor(f.cellH/2), width, 1) //nolint:mnd // the middle
				cr.Fill()
			}
		}
	}

	if modes.CursorVisible && cy < rows && cx < cols {
		t.drawCursor(cr, layout, cx, cy, focused, modes.CursorShape)
	}
	return false
}

// isPlain reports whether a cell is a single ASCII character, which can be drawn in a run.
func isPlain(c *emulator.Cell) bool {
	return c.Width == 1 && c.Rune < 0x80 && len(c.Combining) == 0
}

func (t *Terminal) drawCursor(cr *cairo.Context, layout *pango.Layout, cx, cy int, focused bool, shape emulator.CursorShape) {
	f := &t.fonts
	c := &t.emu.Line(cy).Cells[cx]
	if c.Width == 0 && cx > 0 {
		cx--
		c = &t.emu.Line(cy).Cells[cx]
	}
	fg, bg := t.cellColors(c.Style, t.sel.contains(cx, cy))
	cursorColor, textColor := fg, bg
	if t.theme.cursor != nil {
		cursorColor = *t.theme.cursor
	}
	if t.theme.cursorFg != nil {
		textColor = *t.theme.cursorFg
	}
	x, y := float64(cx)*f.cellW, float64(cy)*f.cellH
	w := float64(max(c.Width, 1)) * f.cellW
	cr.SetSourceRGBA(cursorColor.R, cursorColor.G, cursorColor.B, cursorColor.A)
	switch {
	case !focused:
		cr.SetLineWidth(1)
		cr.Rectangle(x+0.5, y+0.5, w-1, f.cellH-1)
		cr.Stroke()
	case shape == emulator.CursorBar:
		cr.Rectangle(x, y, 2, f.cellH) //nolint:mnd // a bar's width
		cr.Fill()
	case shape == emulator.CursorUnderline:
		cr.Rectangle(x, y+f.cellH-2, w, 2) //nolint:mnd // an underline's height
		cr.Fill()
	default:
		cr.Rectangle(x, y, w, f.cellH)
		cr.Fill()
		if text := c.Text(); text != "" && text != " " {
			cr.SetSourceRGBA(textColor.R, textColor.G, textColor.B, textColor.A)
			layout.SetFontDescription(f.styled(c.Flags))
			layout.SetText(text, -1)
			cr.MoveTo(x, y)
			pango.CairoShowLayout(cr, layout)
		}
	}
}
