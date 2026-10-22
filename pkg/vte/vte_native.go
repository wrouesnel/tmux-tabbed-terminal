//go:build windows || nativeterm

package vte

import (
	"os/exec"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/gtk"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/termwidget"
)

// Terminal is a termwidget terminal, which stands in for VTE where it isn't available:
// on Windows, or on Linux when built with the nativeterm tag to try it out.
type Terminal struct {
	*gtk.Widget
	t *termwidget.Terminal
}

// New creates a terminal widget.
func New() *Terminal {
	t, err := termwidget.New()
	if err != nil {
		panic(err)
	}
	return &Terminal{Widget: &t.Widget, t: t}
}

// Start runs cmd on a new terminal attached to the widget, closing any it was running
// on. It returns the path of the terminal device, which is "" on Windows. The caller
// waits for the process.
func (t *Terminal) Start(cmd *exec.Cmd) (string, error) { return t.t.Start(cmd) }

// OnContentsChanged calls fn whenever the visible contents change.
func (t *Terminal) OnContentsChanged(fn func()) { t.t.OnContentsChanged(fn) }

// SetFont sets the font from a Pango font description such as "Monospace 11".
func (t *Terminal) SetFont(desc string) { t.t.SetFont(desc) }

// SetColors sets the foreground, background and palette. The palette has 0, 8, 16, 232
// or 256 entries. An empty palette restores the default.
func (t *Terminal) SetColors(fg, bg Color, palette []Color) {
	pal := make([]termwidget.Color, len(palette))
	for i, c := range palette {
		pal[i] = termwidget.Color(c)
	}
	t.t.SetColors(termwidget.Color(fg), termwidget.Color(bg), pal)
}

// SetCursorColors sets the cursor color and the color of text under it. A nil color
// restores the default.
func (t *Terminal) SetCursorColors(cursor, cursorFg *Color) {
	conv := func(c *Color) *termwidget.Color {
		if c == nil {
			return nil
		}
		tc := termwidget.Color(*c)
		return &tc
	}
	t.t.SetCursorColors(conv(cursor), conv(cursorFg))
}

// SixelSupported reports false: SIXEL images aren't supported.
func SixelSupported() bool { return false }

// SetEnableSixel does nothing.
func (t *Terminal) SetEnableSixel(bool) {}

// SetAllowHyperlink sets whether the terminal follows OSC 8 hyperlinks.
func (t *Terminal) SetAllowHyperlink(allow bool) { t.t.SetAllowHyperlink(allow) }

// AddMatch adds a regex whose matches MatchAt reports, returning its tag. The link
// patterns are valid in both PCRE2 and Go's syntax, which this uses.
func (t *Terminal) AddMatch(pattern string) (int, error) { return t.t.AddMatch(pattern) }

// MatchAt returns the link under a mouse event, preferring a hyperlink to a regex match.
func (t *Terminal) MatchAt(ev *gdk.Event) (Match, bool) {
	m, ok := t.t.MatchAt(ev)
	return Match(m), ok
}

// CellAt returns the column and row of the screen at a point in the widget.
func (t *Terminal) CellAt(x, y float64) (int, int) { return t.t.CellAt(x, y) }

// MatchAtCell returns the AddMatch regex match covering a cell of the screen.
func (t *Terminal) MatchAtCell(col, row int) (Match, bool) {
	m, ok := t.t.MatchAtCell(col, row)
	return Match(m), ok
}

// Feed shows data as if the terminal's program had written it.
func (t *Terminal) Feed(data []byte) { t.t.Feed(data) }

// SetSize sets the terminal's size in cells.
func (t *Terminal) SetSize(cols, rows int) { t.t.SetSize(cols, rows) }

// SetBoldIsBright sets whether bold text uses the bright palette colors.
func (t *Terminal) SetBoldIsBright(b bool) { t.t.SetBoldIsBright(b) }

// SetScrollbackLines does nothing: there's no scrollback. tmux keeps its own.
func (t *Terminal) SetScrollbackLines(int) {}

// SetAudibleBell sets whether the bell makes a sound.
func (t *Terminal) SetAudibleBell(b bool) { t.t.SetAudibleBell(b) }

// SetMouseAutohide does nothing.
func (t *Terminal) SetMouseAutohide(bool) {}

// SetFontScale sets the font zoom factor.
func (t *Terminal) SetFontScale(scale float64) { t.t.SetFontScale(scale) }

// FontScale returns the font zoom factor.
func (t *Terminal) FontScale() float64 { return t.t.FontScale() }

// HasSelection reports whether any text is selected.
func (t *Terminal) HasSelection() bool { return t.t.HasSelection() }

// CopyClipboard copies the selection to the clipboard as text.
func (t *Terminal) CopyClipboard() { t.t.CopyClipboard() }

// PasteClipboard pastes the clipboard into the terminal.
func (t *Terminal) PasteClipboard() { t.t.PasteClipboard() }

// Size returns the terminal size in cells.
func (t *Terminal) Size() (int, int) { return t.t.Size() }
