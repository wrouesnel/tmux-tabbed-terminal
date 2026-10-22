// Package emulator is a terminal emulator without a display: it parses what a program
// writes to a terminal, keeps the screen it would show, and encodes keys and mouse events
// for the program. It covers what xterm-256color promises, which is what tmux and
// full-screen programs use. It has no scrollback.
//
// An Emulator isn't safe for concurrent use: feed it and read it from one goroutine,
// such as a GUI's main thread.
package emulator

import (
	vtparser "github.com/danielgatis/go-vte"
)

// MouseMode is which mouse events the program asked for.
type MouseMode int

// Mouse tracking modes, set with DECSET.
const (
	MouseNone   MouseMode = iota
	MouseX10              // 9: presses only
	MouseNormal           // 1000: presses and releases
	MouseButton           // 1002: and motion while a button is down
	MouseAny              // 1003: and all motion
)

// CursorShape is the cursor's shape, as DECSCUSR sets it.
type CursorShape int

// Cursor shapes.
const (
	CursorBlock CursorShape = iota
	CursorUnderline
	CursorBar
)

// Modes are the terminal modes a display and input encoder need.
type Modes struct {
	AppCursor      bool // DECCKM: cursor keys send SS3 sequences
	AutoWrap       bool // DECAWM
	CursorVisible  bool // DECTCEM
	Insert         bool // IRM
	Origin         bool // DECOM: cursor addressing is relative to the scroll region
	BracketedPaste bool
	FocusEvents    bool
	Mouse          MouseMode
	MouseSGR       bool // 1006: SGR encoding of mouse events
	CursorShape    CursorShape
	CursorBlink    bool
}

type cursor struct {
	x, y  int
	style Style
	// wrapNext is set when a character was written in the last column: the next one
	// wraps first.
	wrapNext bool
	// charsets are G0 and G1, true for DEC special graphics; gl is the one in use.
	charsets [2]bool
	gl       int
}

type buffer struct {
	lines []Line
	saved cursor
}

// Emulator is a terminal's screen and state.
type Emulator struct {
	cols, rows int
	primary    buffer
	alt        buffer
	altActive  bool
	cur        cursor
	// top and bottom are the scroll region's first and last rows.
	top, bottom int
	tabs        []bool
	modes       Modes
	title       string
	links       []string
	linkIDs     map[string]uint32

	parser *vtparser.Parser

	// Respond receives replies to the program's queries, to be written to it.
	Respond func([]byte)
	// OnBell is called for BEL.
	OnBell func()
	// OnTitle is called when the program sets the window title.
	OnTitle func(string)
	// DefaultFg and DefaultBg are reported to programs that ask for the colors.
	DefaultFg, DefaultBg [3]uint8
}

// New returns an emulator with a blank screen of cols by rows cells.
func New(cols, rows int) *Emulator {
	cols, rows = max(cols, 1), max(rows, 1)
	e := &Emulator{
		cols: cols, rows: rows,
		linkIDs:   map[string]uint32{},
		DefaultFg: [3]uint8{0xff, 0xff, 0xff},
	}
	e.parser = vtparser.NewParser(&performer{e})
	e.reset()
	return e
}

// reset restores the state a terminal starts in, as RIS does.
func (e *Emulator) reset() {
	e.primary = buffer{lines: e.blankLines(e.rows)}
	e.alt = buffer{lines: e.blankLines(e.rows)}
	e.altActive = false
	e.cur = cursor{}
	e.top, e.bottom = 0, e.rows-1
	e.modes = Modes{AutoWrap: true, CursorVisible: true}
	e.resetTabs()
	e.primary.saved, e.alt.saved = e.cur, e.cur
}

func (e *Emulator) blankLines(n int) []Line {
	lines := make([]Line, n)
	for i := range lines {
		lines[i] = newLine(e.cols, Style{})
	}
	return lines
}

func (e *Emulator) resetTabs() {
	e.tabs = make([]bool, e.cols)
	for i := 8; i < e.cols; i += 8 {
		e.tabs[i] = true
	}
}

// Write feeds the emulator output of the program.
func (e *Emulator) Write(data []byte) (int, error) {
	for _, b := range data {
		e.parser.Advance(b)
	}
	return len(data), nil
}

// Size returns the screen's size in cells.
func (e *Emulator) Size() (int, int) { return e.cols, e.rows }

// Line returns row y of the screen. The caller mustn't modify it.
func (e *Emulator) Line(y int) *Line { return &e.buf().lines[y] }

// Cursor returns the cursor's column and row.
func (e *Emulator) Cursor() (int, int) { return e.cur.x, e.cur.y }

// Modes returns the terminal's modes.
func (e *Emulator) Modes() Modes { return e.modes }

// AltScreen reports whether the alternate screen is showing.
func (e *Emulator) AltScreen() bool { return e.altActive }

// Title returns the window title the program set.
func (e *Emulator) Title() string { return e.title }

// Link returns the URI of a hyperlink ID from a cell's style.
func (e *Emulator) Link(id uint32) string {
	if id == 0 || int(id) > len(e.links) {
		return ""
	}
	return e.links[id-1]
}

func (e *Emulator) buf() *buffer {
	if e.altActive {
		return &e.alt
	}
	return &e.primary
}

func (e *Emulator) respond(s string) {
	if e.Respond != nil {
		e.Respond([]byte(s))
	}
}

// Resize changes the screen's size. Lines aren't rewrapped: they're cut or padded on the
// right, and rows leave or join at the bottom, or at the top when the cursor would
// otherwise fall off the screen.
func (e *Emulator) Resize(cols, rows int) {
	cols, rows = max(cols, 1), max(rows, 1)
	if cols == e.cols && rows == e.rows {
		return
	}
	for _, b := range []*buffer{&e.primary, &e.alt} {
		curY := &b.saved.y
		if b == e.buf() {
			curY = &e.cur.y
		}
		if drop := *curY - rows + 1; drop > 0 {
			b.lines = b.lines[drop:]
			*curY -= drop
		}
		if len(b.lines) > rows {
			b.lines = b.lines[:rows]
		}
		for len(b.lines) < rows {
			b.lines = append(b.lines, newLine(cols, Style{}))
		}
		for i := range b.lines {
			l := &b.lines[i]
			if len(l.Cells) > cols {
				l.Cells = l.Cells[:cols]
				// A wide character cut in half leaves a blank.
				if last := &l.Cells[cols-1]; last.Width == 2 { //nolint:mnd // wide
					*last = blank(Style{})
				}
			}
			for len(l.Cells) < cols {
				l.Cells = append(l.Cells, blank(Style{}))
			}
		}
		b.saved.x = min(b.saved.x, cols-1)
		b.saved.y = min(b.saved.y, rows-1)
	}
	e.cols, e.rows = cols, rows
	e.cur.x, e.cur.y = min(e.cur.x, cols-1), min(e.cur.y, rows-1)
	e.cur.wrapNext = false
	e.top, e.bottom = 0, rows-1
	e.resetTabs()
}

// Text returns the screen's text from cell (x0, y0) to cell (x1, y1) inclusive, with
// trailing blanks trimmed from each line and lines joined by newlines, except where the
// text wrapped.
func (e *Emulator) Text(x0, y0, x1, y1 int) string {
	var out []rune
	for y := y0; y <= y1 && y < e.rows; y++ {
		l := e.Line(y)
		from, to := 0, e.cols-1
		if y == y0 {
			from = x0
		}
		if y == y1 {
			to = min(x1, e.cols-1)
		}
		var line []rune
		for x := from; x <= to; x++ {
			c := &l.Cells[x]
			if c.Width == 0 {
				continue
			}
			line = append(line, c.Rune)
			line = append(line, c.Combining...)
		}
		if !l.Wrapped || y == y1 {
			for len(line) > 0 && line[len(line)-1] == ' ' {
				line = line[:len(line)-1]
			}
		}
		out = append(out, line...)
		if y != y1 && !l.Wrapped {
			out = append(out, '\n')
		}
	}
	return string(out)
}

// decGraphics maps the DEC special graphics characters to Unicode line drawing.
//
//nolint:gochecknoglobals
var decGraphics = map[rune]rune{
	'`': '◆', 'a': '▒', 'b': '␉', 'c': '␌', 'd': '␍', 'e': '␊', 'f': '°', 'g': '±',
	'h': '␤', 'i': '␋', 'j': '┘', 'k': '┐', 'l': '┌', 'm': '└', 'n': '┼', 'o': '⎺',
	'p': '⎻', 'q': '─', 'r': '⎼', 's': '⎽', 't': '├', 'u': '┤', 'v': '┴', 'w': '┬',
	'x': '│', 'y': '≤', 'z': '≥', '{': 'π', '|': '≠', '}': '£', '~': '·',
}

// print writes a character at the cursor.
func (e *Emulator) print(r rune) {
	if e.cur.charsets[e.cur.gl] {
		if g, ok := decGraphics[r]; ok {
			r = g
		}
	}
	w := RuneWidth(r)
	lines := e.buf().lines
	if w == 0 {
		// A combining mark joins the character before the cursor.
		x, y := e.cur.x-1, e.cur.y
		if e.cur.wrapNext {
			x = e.cur.x
		}
		if x >= 0 {
			c := &lines[y].Cells[x]
			if c.Width == 0 && x > 0 {
				c = &lines[y].Cells[x-1]
			}
			c.Combining = append(c.Combining, r)
		}
		return
	}
	if e.cur.wrapNext || (w == 2 && e.cur.x == e.cols-1 && e.modes.AutoWrap) {
		if e.cur.x == e.cols-1 && !e.cur.wrapNext {
			// A wide character doesn't fit in the last column: leave it blank.
			e.setCell(e.cur.x, e.cur.y, blank(e.cur.style))
		}
		lines[e.cur.y].Wrapped = true
		e.cur.x = 0
		e.index()
	}
	if e.modes.Insert {
		e.insertChars(w)
	}
	cell := Cell{Rune: r, Width: uint8(w), Style: e.cur.style} //nolint:gosec // w is 1 or 2
	e.setCell(e.cur.x, e.cur.y, cell)
	if w == 2 && e.cur.x+1 < e.cols {
		e.setCell(e.cur.x+1, e.cur.y, Cell{Rune: ' ', Width: 0, Style: e.cur.style})
	}
	if e.cur.x+w >= e.cols {
		e.cur.x = e.cols - 1
		e.cur.wrapNext = e.modes.AutoWrap
	} else {
		e.cur.x += w
	}
}

// setCell overwrites a cell, blanking what's left of a wide character it splits.
func (e *Emulator) setCell(x, y int, c Cell) {
	cells := e.buf().lines[y].Cells
	old := &cells[x]
	if old.Width == 0 && x > 0 && cells[x-1].Width == 2 { //nolint:mnd // wide
		cells[x-1] = blank(cells[x-1].Style)
	}
	if old.Width == 2 && x+1 < len(cells) && c.Width != 2 { //nolint:mnd // wide
		cells[x+1] = blank(cells[x+1].Style)
	}
	cells[x] = c
}

// moveTo moves the cursor, keeping it on the screen, or in the scroll region in origin
// mode.
func (e *Emulator) moveTo(x, y int) {
	minY, maxY := 0, e.rows-1
	if e.modes.Origin {
		minY, maxY = e.top, e.bottom
	}
	e.cur.x = min(max(x, 0), e.cols-1)
	e.cur.y = min(max(y, minY), maxY)
	e.cur.wrapNext = false
}

// home moves the cursor to the top left: of the scroll region in origin mode.
func (e *Emulator) home() {
	if e.modes.Origin {
		e.moveTo(0, e.top)
	} else {
		e.moveTo(0, 0)
	}
}

// index moves the cursor down, scrolling the region up at its bottom.
func (e *Emulator) index() {
	switch {
	case e.cur.y == e.bottom:
		e.scrollUp(1)
	case e.cur.y < e.rows-1:
		e.cur.y++
	}
	e.cur.wrapNext = false
}

// reverseIndex moves the cursor up, scrolling the region down at its top.
func (e *Emulator) reverseIndex() {
	switch {
	case e.cur.y == e.top:
		e.scrollDown(1)
	case e.cur.y > 0:
		e.cur.y--
	}
	e.cur.wrapNext = false
}

// scrollUp scrolls the scroll region up n lines.
func (e *Emulator) scrollUp(n int) { e.scrollRegion(e.top, e.bottom, n) }

// scrollDown scrolls the scroll region down n lines.
func (e *Emulator) scrollDown(n int) { e.scrollRegion(e.top, e.bottom, -n) }

// scrollRegion scrolls rows top to bottom up n lines, or down -n lines, filling with
// blank lines.
func (e *Emulator) scrollRegion(top, bottom, n int) {
	lines := e.buf().lines
	height := bottom - top + 1
	if n > height {
		n = height
	}
	if n < -height {
		n = -height
	}
	switch {
	case n > 0:
		copy(lines[top:], lines[top+n:bottom+1])
		for i := bottom - n + 1; i <= bottom; i++ {
			lines[i] = newLine(e.cols, e.cur.style)
		}
	case n < 0:
		n = -n
		copy(lines[top+n:bottom+1], lines[top:bottom+1-n])
		for i := top; i < top+n; i++ {
			lines[i] = newLine(e.cols, e.cur.style)
		}
	}
}

// eraseCells blanks cells x0 to x1 of row y.
func (e *Emulator) eraseCells(y, x0, x1 int) {
	x0, x1 = max(x0, 0), min(x1, e.cols-1)
	for x := x0; x <= x1; x++ {
		e.setCell(x, y, blank(e.cur.style))
	}
}

func (e *Emulator) eraseLines(y0, y1 int) {
	for y := y0; y <= y1; y++ {
		e.buf().lines[y] = newLine(e.cols, e.cur.style)
	}
}

// insertChars shifts the cells from the cursor right n places.
func (e *Emulator) insertChars(n int) {
	cells := e.buf().lines[e.cur.y].Cells
	n = min(n, e.cols-e.cur.x)
	copy(cells[e.cur.x+n:], cells[e.cur.x:e.cols-n])
	for x := e.cur.x; x < e.cur.x+n; x++ {
		cells[x] = blank(e.cur.style)
	}
}

// deleteChars shifts the cells right of the cursor left n places.
func (e *Emulator) deleteChars(n int) {
	cells := e.buf().lines[e.cur.y].Cells
	n = min(n, e.cols-e.cur.x)
	copy(cells[e.cur.x:], cells[e.cur.x+n:])
	for x := e.cols - n; x < e.cols; x++ {
		cells[x] = blank(e.cur.style)
	}
}

func (e *Emulator) saveCursor() { e.buf().saved = e.cur }

func (e *Emulator) restoreCursor() {
	e.cur = e.buf().saved
	e.cur.x, e.cur.y = min(e.cur.x, e.cols-1), min(e.cur.y, e.rows-1)
}

// setAltScreen switches to or from the alternate screen.
func (e *Emulator) setAltScreen(on, saveCursor, clear bool) {
	if on == e.altActive {
		return
	}
	if on {
		if saveCursor {
			e.saveCursor()
		}
		e.altActive = true
		if clear {
			e.alt.lines = e.blankLines(e.rows)
		}
		return
	}
	e.altActive = false
	if saveCursor {
		e.restoreCursor()
	}
}

// linkID returns the ID of a hyperlink URI, adding it if it's new.
func (e *Emulator) linkID(uri string) uint32 {
	if uri == "" {
		return 0
	}
	if id, ok := e.linkIDs[uri]; ok {
		return id
	}
	e.links = append(e.links, uri)
	id := uint32(len(e.links)) //nolint:gosec // the links don't number 4 billion
	e.linkIDs[uri] = id
	return id
}
