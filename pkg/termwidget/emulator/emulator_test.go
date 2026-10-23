package emulator

import (
	"strings"
	"testing"
)

func screen(t *testing.T, cols, rows int, input string) *Emulator {
	t.Helper()
	e := New(cols, rows)
	_, _ = e.Write([]byte(input))
	return e
}

func expect(t *testing.T, e *Emulator, want ...string) {
	t.Helper()
	got := strings.Split(strings.TrimSuffix(e.String(), "\n"), "\n")
	for len(want) < len(got) {
		want = append(want, "")
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("screen:\n got %q\nwant %q", got, want)
	}
}

func TestPrintWrapAndScroll(t *testing.T) {
	e := screen(t, 5, 3, "abcdefgh\r\nij\r\nkl")
	expect(t, e, "fgh", "ij", "kl")
	e = screen(t, 5, 3, "abcdefg")
	if !e.Line(0).Wrapped || e.Line(1).Wrapped {
		t.Errorf("wrapped flags: %v %v", e.Line(0).Wrapped, e.Line(1).Wrapped)
	}
	if got := e.Text(0, 0, 4, 1); got != "abcdefg" {
		t.Errorf("text across a wrap: %q", got)
	}
}

func TestLastColumnDoesNotWrapUntilNextChar(t *testing.T) {
	e := screen(t, 3, 2, "abc\r\n")
	expect(t, e, "abc", "")
	if x, y := e.Cursor(); x != 0 || y != 1 {
		t.Errorf("cursor %d,%d", x, y)
	}
}

func TestCursorMovementAndErase(t *testing.T) {
	e := screen(t, 10, 3, "hello\x1b[1;3HX\x1b[2;1Hworld\x1b[1D\x1b[K")
	expect(t, e, "heXlo", "worl")
	e = screen(t, 10, 3, "aaaa\r\nbbbb\r\ncccc\x1b[2;2H\x1b[J")
	expect(t, e, "aaaa", "b")
	e = screen(t, 10, 3, "aaaa\r\nbbbb\r\ncccc\x1b[2;2H\x1b[1J")
	expect(t, e, "", "  bb", "cccc")
}

func TestScrollRegion(t *testing.T) {
	e := screen(t, 4, 4, "1\r\n2\r\n3\r\n4\x1b[2;3r\x1b[3;1H\n")
	expect(t, e, "1", "3", "", "4")
	e = screen(t, 4, 4, "1\r\n2\r\n3\r\n4\x1b[2;3r\x1b[2;1H\x1bM")
	expect(t, e, "1", "", "2", "4")
	e = screen(t, 4, 4, "1\r\n2\r\n3\r\n4\x1b[2;1H\x1b[L")
	expect(t, e, "1", "", "2", "3")
	e = screen(t, 4, 4, "1\r\n2\r\n3\r\n4\x1b[2;1H\x1b[2M")
	expect(t, e, "1", "4")
}

func TestInsertDeleteChars(t *testing.T) {
	e := screen(t, 6, 1, "abcdef\x1b[1;2H\x1b[2@")
	expect(t, e, "a  bcd")
	e = screen(t, 6, 1, "abcdef\x1b[1;2H\x1b[2P")
	expect(t, e, "adef")
	e = screen(t, 6, 1, "abcdef\x1b[1;2H\x1b[2X")
	expect(t, e, "a  def")
}

func TestWideCharacters(t *testing.T) {
	e := screen(t, 5, 2, "a漢b")
	if l := e.Line(0); l.Cells[1].Width != 2 || l.Cells[2].Width != 0 || l.Cells[3].Rune != 'b' {
		t.Errorf("cells: %+v", l.Cells)
	}
	expect(t, e, "a漢b")
	// A wide character that doesn't fit in the last column wraps whole.
	e = screen(t, 5, 2, "abcd漢")
	expect(t, e, "abcd", "漢")
	// Overwriting half of a wide character blanks the other half.
	e = screen(t, 5, 1, "漢\x1b[1;2Hx")
	expect(t, e, " x")
}

func TestCombiningMarks(t *testing.T) {
	e := screen(t, 5, 1, "éx")
	if c := e.Line(0).Cells[0]; c.Text() != "é" {
		t.Errorf("combined cell %q", c.Text())
	}
	if c := e.Line(0).Cells[1]; c.Rune != 'x' {
		t.Errorf("next cell %q", c.Rune)
	}
}

func TestSGR(t *testing.T) {
	e := screen(t, 10, 1, "\x1b[1;31;42ma\x1b[38;5;200;48;2;1;2;3mb\x1b[38:2::4:5:6mc\x1b[0md")
	cells := e.Line(0).Cells
	if cells[0].Flags != Bold || cells[0].Fg != IndexColor(1) || cells[0].Bg != IndexColor(2) {
		t.Errorf("a: %+v", cells[0].Style)
	}
	if cells[1].Fg != IndexColor(200) || cells[1].Bg != RGBColor(1, 2, 3) || cells[1].Flags != Bold {
		t.Errorf("b: %+v", cells[1].Style)
	}
	if cells[2].Fg != RGBColor(4, 5, 6) {
		t.Errorf("c: %+v", cells[2].Style)
	}
	if cells[3].Style != (Style{}) {
		t.Errorf("d: %+v", cells[3].Style)
	}
}

func TestEraseUsesBackground(t *testing.T) {
	e := screen(t, 4, 1, "\x1b[44m\x1b[K")
	if c := e.Line(0).Cells[3]; c.Bg != IndexColor(4) {
		t.Errorf("erased cell background %v", c.Bg)
	}
}

func TestAltScreen(t *testing.T) {
	e := screen(t, 6, 2, "main\x1b[?1049hALT")
	expect(t, e, "    AL", "T")
	if !e.AltScreen() {
		t.Error("not on the alternate screen")
	}
	_, _ = e.Write([]byte("\x1b[?1049l"))
	expect(t, e, "main")
	if x, y := e.Cursor(); x != 4 || y != 0 {
		t.Errorf("cursor not restored: %d,%d", x, y)
	}
}

func TestDECGraphics(t *testing.T) {
	e := screen(t, 5, 1, "\x1b(0lqk\x1b(Bq")
	expect(t, e, "┌─┐q")
}

func TestResponses(t *testing.T) {
	var got []string
	e := New(10, 5)
	e.Respond = func(b []byte) { got = append(got, string(b)) }
	_, _ = e.Write([]byte("\x1b[3;4H\x1b[6n\x1b[c\x1b[>c\x1b[5n\x1b]11;?\x07\x1b[?2004$p"))
	want := []string{"\x1b[3;4R", "\x1b[?62;22c", "\x1b[>1;10;0c", "\x1b[0n", "\x1b]11;rgb:0000/0000/0000\x1b\\", "\x1b[?2004;2$y"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("responses %q, want %q", got, want)
	}
}

func TestHyperlinks(t *testing.T) {
	e := screen(t, 20, 1, "a\x1b]8;;https://x.test/a;b\x1b\\link\x1b]8;;\x1b\\z")
	cells := e.Line(0).Cells
	if cells[0].Link != 0 || cells[5].Link != 0 {
		t.Errorf("unlinked cells have links")
	}
	if got := e.Link(cells[1].Link); got != "https://x.test/a;b" {
		t.Errorf("link %q", got)
	}
}

func TestLinkText(t *testing.T) {
	// A hyperlink wrapping onto the next row, and another to the same URI after it.
	e := screen(t, 8, 3, "see \x1b]8;;https://x.test/\x1b\\my link\x1b]8;;\x1b\\ or \x1b]8;;https://x.test/\x1b\\it\x1b]8;;\x1b\\")
	cases := []struct {
		col, row int
		want     string
		ok       bool
	}{
		{4, 0, "my link", true},
		{2, 1, "my link", true},
		{0, 0, "", false},
		{4, 1, "", false},
		{8, 0, "", false},
	}
	for _, c := range cases {
		if got, ok := e.LinkText(c.col, c.row); got != c.want || ok != c.ok {
			t.Errorf("at %d,%d: %q %v, want %q %v", c.col, c.row, got, ok, c.want, c.ok)
		}
	}
	if got, _ := e.LinkText(7, 1); got != "it" {
		t.Errorf("second link: %q", got)
	}
}

func TestTitle(t *testing.T) {
	e := screen(t, 5, 1, "\x1b]2;hello; there\x07")
	if e.Title() != "hello; there" {
		t.Errorf("title %q", e.Title())
	}
}

func TestResize(t *testing.T) {
	e := screen(t, 5, 3, "a\r\nb\r\nc")
	e.Resize(3, 2)
	expect(t, e, "b", "c")
	if x, y := e.Cursor(); x != 1 || y != 1 {
		t.Errorf("cursor %d,%d", x, y)
	}
	e.Resize(6, 4)
	expect(t, e, "b", "c", "", "")
	_, _ = e.Write([]byte("\x1b[4;6Hz"))
	expect(t, e, "b", "c", "", "     z")
}

func TestTabs(t *testing.T) {
	e := screen(t, 20, 1, "a\tb\tc")
	expect(t, e, "a       b       c")
}

func TestEncodeKey(t *testing.T) {
	e := New(10, 1)
	cases := []struct {
		k    Key
		mods Mod
		want string
	}{
		{KeyUp, 0, "\x1b[A"},
		{KeyUp, ModCtrl, "\x1b[1;5A"},
		{KeyDelete, 0, "\x1b[3~"},
		{KeyPageUp, ModShift, "\x1b[5;2~"},
		{KeyF1, 0, "\x1bOP"},
		{KeyF5, 0, "\x1b[15~"},
		{KeyF12, ModAlt, "\x1b[24;3~"},
		{KeyTab, ModShift, "\x1b[Z"},
		{KeyBackspace, 0, "\x7f"},
		{KeyEnter, ModAlt, "\x1b\r"},
	}
	for _, c := range cases {
		if got := string(e.EncodeKey(c.k, c.mods)); got != c.want {
			t.Errorf("key %v %v: %q, want %q", c.k, c.mods, got, c.want)
		}
	}
	_, _ = e.Write([]byte("\x1b[?1h"))
	if got := string(e.EncodeKey(KeyLeft, 0)); got != "\x1bOD" {
		t.Errorf("app cursor left: %q", got)
	}
	runes := []struct {
		r    rune
		mods Mod
		want string
	}{
		{'a', 0, "a"}, {'c', ModCtrl, "\x03"}, {'C', ModCtrl, "\x03"}, {' ', ModCtrl, "\x00"},
		{'[', ModCtrl, "\x1b"}, {'x', ModAlt, "\x1bx"}, {'é', 0, "é"},
	}
	for _, c := range runes {
		if got := string(EncodeRune(c.r, c.mods)); got != c.want {
			t.Errorf("rune %q %v: %q, want %q", c.r, c.mods, got, c.want)
		}
	}
}

func TestEncodeMouse(t *testing.T) {
	e := New(300, 10)
	if e.EncodeMouse(MousePress, ButtonLeft, 0, 0, 0) != nil {
		t.Error("mouse reported without tracking")
	}
	_, _ = e.Write([]byte("\x1b[?1000h"))
	if got := string(e.EncodeMouse(MousePress, ButtonLeft, 1, 2, 0)); got != "\x1b[M \"#" {
		t.Errorf("X10 press %q", got)
	}
	if got := string(e.EncodeMouse(MouseRelease, ButtonLeft, 1, 2, 0)); got != "\x1b[M#\"#" {
		t.Errorf("X10 release %q", got)
	}
	if e.EncodeMouse(MouseMotion, ButtonLeft, 1, 2, 0) != nil {
		t.Error("motion reported in normal mode")
	}
	_, _ = e.Write([]byte("\x1b[?1002h\x1b[?1006h"))
	if got := string(e.EncodeMouse(MouseMotion, ButtonLeft, 250, 2, ModCtrl)); got != "\x1b[<48;251;3M" {
		t.Errorf("SGR drag %q", got)
	}
	if got := string(e.EncodeMouse(MouseRelease, ButtonRight, 0, 0, 0)); got != "\x1b[<2;1;1m" {
		t.Errorf("SGR release %q", got)
	}
}

func TestEncodePaste(t *testing.T) {
	e := New(10, 1)
	if got := string(e.EncodePaste("a\nb\r\nc")); got != "a\rb\rc" {
		t.Errorf("paste %q", got)
	}
	_, _ = e.Write([]byte("\x1b[?2004h"))
	if got := string(e.EncodePaste("x\x1by")); got != "\x1b[200~xy\x1b[201~" {
		t.Errorf("bracketed paste %q", got)
	}
}
