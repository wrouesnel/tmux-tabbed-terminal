package termwidget

import (
	"bytes"
	"fmt"
	"testing"
	"unsafe"

	"github.com/gotk3/gotk3/cairo"
	"github.com/gotk3/gotk3/gtk"
)

// realized makes a terminal in an offscreen window, realized at a size, or skips without a
// display.
func realized(t *testing.T) *Terminal {
	t.Helper()
	if err := gtk.InitCheck(nil); err != nil {
		t.Skipf("no display: %v", err)
	}
	term, err := New()
	if err != nil {
		t.Fatal(err)
	}
	win, err := gtk.OffscreenWindowNew()
	if err != nil {
		t.Fatal(err)
	}
	win.Add(term)
	win.SetDefaultSize(480, 300)
	win.ShowAll()
	for i := 0; i < 50 && (gtk.EventsPending() || term.GetAllocatedHeight() <= 1); i++ {
		gtk.MainIterationDo(false)
	}
	if !term.back.ensure(term) {
		t.Skip("the widget has no window to make a backing like")
	}
	t.Cleanup(win.Destroy)
	return term
}

// pixels returns what a surface shows, as RGB, without the byte RGB24 leaves unused.
func pixels(src *cairo.Surface, w, h int) []byte {
	img := cairo.CreateImageSurface(cairo.FORMAT_RGB24, w, h)
	cr := cairo.Create(img)
	cr.SetSourceSurface(src, 0, 0)
	cr.Paint()
	img.Flush()
	raw := unsafe.Slice((*byte)(img.GetData()), w*h*4) //nolint:mnd // RGB24 is 4 bytes a pixel
	rgb := make([]byte, 0, w*h*3)                      //nolint:mnd // three colors
	for i := 0; i < len(raw); i += 4 {
		rgb = append(rgb, raw[i], raw[i+1], raw[i+2])
	}
	return rgb
}

// direct returns what drawing every row afresh shows, on a surface made as the backing
// is, so that text is rendered alike.
func direct(term *Terminal, w, h int) []byte {
	win, err := term.GetWindow()
	if err != nil {
		panic(err)
	}
	surface, err := win.CreateSimilarSurface(cairo.CONTENT_COLOR, w, h)
	if err != nil {
		panic(err)
	}
	cr := cairo.Create(surface)
	bg := term.theme.bg
	cr.SetSourceRGBA(bg.R, bg.G, bg.B, bg.A)
	cr.Paint()
	cols, rows := term.emu.Size()
	layout := term.layoutFor(cr)
	for y := range rows {
		term.drawRow(cr, layout, y, cols)
	}
	surface.Flush()
	return pixels(surface, w, h)
}

// TestBackingMatchesDirectDrawing feeds output that scrolls the screen and regions of it,
// up and down, with colors and edits between, and checks after each that the backing,
// moved and rendered in part, shows what drawing every row afresh does.
func TestBackingMatchesDirectDrawing(t *testing.T) {
	term := realized(t)
	b := &term.back
	w, h := b.width, b.height
	steps := []string{
		"",
		"one\r\ntwo\r\nthree\r\n",
		"\x1b[31mred\x1b[0m four\r\nfive\r\n\x1b[44msix on blue\x1b[0m\r\n",
	}
	for i := range 30 {
		steps = append(steps, fmt.Sprintf("line %d \x1b[1mbold\x1b[0m\r\n", i))
	}
	steps = append(steps,
		// A scroll region, as tmux keeps for its status line, scrolled up and down.
		"\x1b[2;8r\x1b[8;1H\nin the region\r\n\x1b[2;1H\x1bMreversed\x1b[r",
		// Edits beside a scroll, in the same chunk.
		"\x1b[1;1Htop edit\x1b[20;1H\r\nafter\r\n\x1b[3;5Hmiddle",
		// Two regions in one chunk: no shift, rendered by row.
		"\x1b[1;5r\x1b[5;1H\n\x1b[6;10r\x1b[10;1H\n\x1b[r",
		// The whole screen cleared.
		"\x1b[2J\x1b[Hcleared\r\n",
	)
	for i, step := range steps {
		term.Feed([]byte(step))
		b.render(term)
		if got, want := pixels(b.surface, w, h), direct(term, w, h); !bytes.Equal(got, want) {
			t.Fatalf("after step %d %q, the backing differs from drawing afresh", i, step)
		}
	}
}

// TestBackingScrollsWithoutRenderingEveryRow checks a line of output scrolling the screen
// renders a few rows, not all of them.
func TestBackingScrollsWithoutRenderingEveryRow(t *testing.T) {
	term := realized(t)
	b := &term.back
	for i := range 40 {
		term.Feed([]byte(fmt.Sprintf("line %d\r\n", i)))
	}
	b.render(term)
	term.Feed([]byte("one more\r\n"))
	marked := 0
	for _, d := range b.dirty {
		if d {
			marked++
		}
	}
	_, rows := term.emu.Size()
	if b.all || marked > 4 {
		t.Errorf("a scroll marked %d of %d rows (all: %v)", marked, rows, b.all)
	}
}
