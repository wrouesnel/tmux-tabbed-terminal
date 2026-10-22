package emulator

import (
	"fmt"
	"strings"
)

// performer handles the control functions the parser finds.
type performer struct {
	e *Emulator
}

func (p *performer) Print(r rune) { p.e.print(r) }

func (p *performer) Execute(b byte) {
	e := p.e
	switch b {
	case 0x07: // BEL
		if e.OnBell != nil {
			e.OnBell()
		}
	case 0x08: // BS
		if e.cur.x > 0 {
			e.cur.x--
		}
		e.cur.wrapNext = false
	case 0x09: // HT
		e.tab(1)
	case 0x0a, 0x0b, 0x0c: // LF, VT, FF
		e.index()
	case 0x0d: // CR
		e.cur.x = 0
		e.cur.wrapNext = false
	case 0x0e: // SO
		e.cur.gl = 1
	case 0x0f: // SI
		e.cur.gl = 0
	}
}

// tab moves the cursor to the nth next tab stop, or back to the -nth previous one.
func (e *Emulator) tab(n int) {
	for ; n > 0 && e.cur.x < e.cols-1; n-- {
		e.cur.x++
		for e.cur.x < e.cols-1 && !e.tabs[e.cur.x] {
			e.cur.x++
		}
	}
	for ; n < 0 && e.cur.x > 0; n++ {
		e.cur.x--
		for e.cur.x > 0 && !e.tabs[e.cur.x] {
			e.cur.x--
		}
	}
	e.cur.wrapNext = false
}

// DCS strings, such as SIXEL images and XTGETTCAP, aren't supported.
func (p *performer) Hook([][]uint16, []byte, bool, rune) {}
func (p *performer) Put(byte)                            {}
func (p *performer) Unhook()                             {}
func (p *performer) SosPmApcDispatch(byte, []byte, bool) {}

func (p *performer) EscDispatch(intermediates []byte, _ bool, b byte) {
	e := p.e
	if len(intermediates) == 1 {
		switch intermediates[0] {
		case '(', ')':
			e.cur.charsets[intermediates[0]-'('] = b == '0'
		case '#':
			if b == '8' { // DECALN
				for y := range e.rows {
					for x := range e.cols {
						e.buf().lines[y].Cells[x] = Cell{Rune: 'E', Width: 1}
					}
				}
			}
		}
		return
	}
	switch b {
	case '7':
		e.saveCursor()
	case '8':
		e.restoreCursor()
	case 'D':
		e.index()
	case 'E':
		e.cur.x = 0
		e.index()
	case 'H':
		e.tabs[e.cur.x] = true
	case 'M':
		e.reverseIndex()
	case 'c':
		e.reset()
	}
}

// param returns parameter i, or def when it's missing or 0.
func param(params [][]uint16, i int, def int) int {
	if i >= len(params) || len(params[i]) == 0 || params[i][0] == 0 {
		return def
	}
	return int(params[i][0])
}

//nolint:gocyclo,cyclop,funlen // a switch over the control sequences
func (p *performer) CsiDispatch(params [][]uint16, intermediates []byte, ignore bool, r rune) {
	e := p.e
	if ignore {
		return
	}
	private := byte(0)
	if len(intermediates) > 0 {
		private = intermediates[0]
	}
	n := param(params, 0, 1)
	switch private {
	case '?':
		switch r {
		case 'h', 'l':
			for _, ps := range params {
				if len(ps) > 0 {
					e.setPrivateMode(int(ps[0]), r == 'h')
				}
			}
		case 'p': // DECRQM, with intermediate $
			e.respond(fmt.Sprintf("\x1b[?%d;%d$y", param(params, 0, 0), e.privateModeState(param(params, 0, 0))))
		}
		return
	case '>':
		if r == 'c' { // secondary DA
			e.respond("\x1b[>1;10;0c")
		}
		return
	case ' ':
		if r == 'q' { // DECSCUSR
			shape := param(params, 0, 0)
			e.modes.CursorBlink = shape == 0 || shape%2 == 1
			switch shape {
			case 3, 4:
				e.modes.CursorShape = CursorUnderline
			case 5, 6:
				e.modes.CursorShape = CursorBar
			default:
				e.modes.CursorShape = CursorBlock
			}
		}
		return
	case '!':
		if r == 'p' { // DECSTR
			e.modes = Modes{AutoWrap: true, CursorVisible: true}
			e.cur.style = Style{}
			e.top, e.bottom = 0, e.rows-1
		}
		return
	case 0:
	default:
		return
	}

	switch r {
	case '@':
		e.insertChars(n)
	case 'A':
		e.moveTo(e.cur.x, max(e.cur.y-n, min(e.top, e.cur.y)))
	case 'B', 'e':
		e.moveTo(e.cur.x, min(e.cur.y+n, max(e.bottom, e.cur.y)))
	case 'C', 'a':
		e.moveTo(e.cur.x+n, e.cur.y)
	case 'D':
		e.moveTo(e.cur.x-n, e.cur.y)
	case 'E':
		e.moveTo(0, min(e.cur.y+n, max(e.bottom, e.cur.y)))
	case 'F':
		e.moveTo(0, max(e.cur.y-n, min(e.top, e.cur.y)))
	case 'G', '`':
		e.moveTo(n-1, e.cur.y)
	case 'H', 'f':
		row := param(params, 0, 1) - 1
		if e.modes.Origin {
			row += e.top
		}
		e.moveTo(param(params, 1, 1)-1, row)
	case 'I':
		e.tab(n)
	case 'Z':
		e.tab(-n)
	case 'J':
		e.eraseDisplay(param(params, 0, 0))
	case 'K':
		switch param(params, 0, 0) {
		case 0:
			e.eraseCells(e.cur.y, e.cur.x, e.cols-1)
		case 1:
			e.eraseCells(e.cur.y, 0, e.cur.x)
		case 2:
			e.eraseCells(e.cur.y, 0, e.cols-1)
		}
	case 'L':
		if e.cur.y >= e.top && e.cur.y <= e.bottom {
			e.scrollRegion(e.cur.y, e.bottom, -n)
			e.cur.x = 0
		}
	case 'M':
		if e.cur.y >= e.top && e.cur.y <= e.bottom {
			e.scrollRegion(e.cur.y, e.bottom, n)
			e.cur.x = 0
		}
	case 'P':
		e.deleteChars(n)
	case 'S':
		e.scrollUp(n)
	case 'T':
		e.scrollDown(n)
	case 'X':
		e.eraseCells(e.cur.y, e.cur.x, e.cur.x+n-1)
	case 'b': // REP
		if e.cur.x > 0 || e.cur.wrapNext {
			x := e.cur.x
			if !e.cur.wrapNext {
				x--
			}
			c := e.buf().lines[e.cur.y].Cells[x]
			for range n {
				e.print(c.Rune)
			}
		}
	case 'c':
		if param(params, 0, 0) == 0 { // primary DA: a VT220 with ANSI color
			e.respond("\x1b[?62;22c")
		}
	case 'd':
		row := n - 1
		if e.modes.Origin {
			row += e.top
		}
		e.moveTo(e.cur.x, row)
	case 'g':
		switch param(params, 0, 0) {
		case 0:
			e.tabs[e.cur.x] = false
		case 3:
			e.tabs = make([]bool, e.cols)
		}
	case 'h', 'l':
		if param(params, 0, 0) == 4 { //nolint:mnd // IRM
			e.modes.Insert = r == 'h'
		}
	case 'm':
		e.sgr(params)
	case 'n':
		switch param(params, 0, 0) {
		case 5:
			e.respond("\x1b[0n")
		case 6:
			row := e.cur.y
			if e.modes.Origin {
				row -= e.top
			}
			e.respond(fmt.Sprintf("\x1b[%d;%dR", row+1, e.cur.x+1))
		}
	case 'r':
		top, bottom := param(params, 0, 1)-1, param(params, 1, e.rows)-1
		if bottom > e.rows-1 {
			bottom = e.rows - 1
		}
		if top < bottom {
			e.top, e.bottom = top, bottom
			e.home()
		}
	case 's':
		e.saveCursor()
	case 'u':
		e.restoreCursor()
	case 't':
		if param(params, 0, 0) == 18 { //nolint:mnd // report the size in characters
			e.respond(fmt.Sprintf("\x1b[8;%d;%dt", e.rows, e.cols))
		}
	}
}

func (e *Emulator) eraseDisplay(mode int) {
	switch mode {
	case 0:
		e.eraseCells(e.cur.y, e.cur.x, e.cols-1)
		if e.cur.y+1 < e.rows {
			e.eraseLines(e.cur.y+1, e.rows-1)
		}
	case 1:
		if e.cur.y > 0 {
			e.eraseLines(0, e.cur.y-1)
		}
		e.eraseCells(e.cur.y, 0, e.cur.x)
	case 2, 3: //nolint:mnd // 3 clears scrollback too, which there isn't
		e.eraseLines(0, e.rows-1)
	}
}

//nolint:cyclop // a switch over the modes
func (e *Emulator) setPrivateMode(mode int, on bool) {
	m := &e.modes
	switch mode {
	case 1:
		m.AppCursor = on
	case 6:
		m.Origin = on
		e.home()
	case 7:
		m.AutoWrap = on
	case 9, 1000, 1002, 1003:
		mouse := map[int]MouseMode{9: MouseX10, 1000: MouseNormal, 1002: MouseButton, 1003: MouseAny}[mode]
		if on {
			m.Mouse = mouse
		} else if m.Mouse == mouse {
			m.Mouse = MouseNone
		}
	case 12:
		m.CursorBlink = on
	case 25:
		m.CursorVisible = on
	case 47, 1047:
		e.setAltScreen(on, false, mode == 1047 && on)
	case 1048:
		if on {
			e.saveCursor()
		} else {
			e.restoreCursor()
		}
	case 1049:
		e.setAltScreen(on, true, true)
	case 1004:
		m.FocusEvents = on
	case 1006:
		m.MouseSGR = on
	case 2004:
		m.BracketedPaste = on
	}
}

// privateModeState reports a mode for DECRQM: 1 set, 2 reset, 0 not recognized.
func (e *Emulator) privateModeState(mode int) int {
	m := e.modes
	state := map[int]bool{
		1: m.AppCursor, 6: m.Origin, 7: m.AutoWrap, 25: m.CursorVisible, 1049: e.altActive,
		1000: m.Mouse == MouseNormal, 1002: m.Mouse == MouseButton, 1003: m.Mouse == MouseAny,
		1004: m.FocusEvents, 1006: m.MouseSGR, 2004: m.BracketedPaste,
	}
	on, ok := state[mode]
	switch {
	case !ok:
		return 0
	case on:
		return 1
	}
	return 2 //nolint:mnd // reset
}

// sgr sets the text attributes.
//
//nolint:gocyclo,cyclop // a switch over the attributes
func (e *Emulator) sgr(params [][]uint16) {
	s := &e.cur.style
	if len(params) == 0 {
		params = [][]uint16{{0}}
	}
	for i := 0; i < len(params); i++ {
		ps := params[i]
		code := 0
		if len(ps) > 0 {
			code = int(ps[0])
		}
		switch {
		case code == 0:
			*s = Style{Link: s.Link}
		case code == 1:
			s.Flags |= Bold
		case code == 2:
			s.Flags |= Dim
		case code == 3:
			s.Flags |= Italic
		case code == 4:
			// 4:0 turns underline off; the other styles are all drawn as one.
			if len(ps) > 1 && ps[1] == 0 {
				s.Flags &^= Underline
			} else {
				s.Flags |= Underline
			}
		case code == 5 || code == 6:
			s.Flags |= Blink
		case code == 7:
			s.Flags |= Reverse
		case code == 8:
			s.Flags |= Invisible
		case code == 9:
			s.Flags |= Strike
		case code == 21:
			s.Flags |= Underline
		case code == 22:
			s.Flags &^= Bold | Dim
		case code == 23:
			s.Flags &^= Italic
		case code == 24:
			s.Flags &^= Underline
		case code == 25:
			s.Flags &^= Blink
		case code == 27:
			s.Flags &^= Reverse
		case code == 28:
			s.Flags &^= Invisible
		case code == 29:
			s.Flags &^= Strike
		case code >= 30 && code <= 37:
			s.Fg = IndexColor(uint8(code - 30)) //nolint:gosec // 0 to 7
		case code == 38, code == 48, code == 58:
			var c Color
			var ok bool
			c, ok, i = extendedColor(params, i)
			if ok {
				switch code {
				case 38:
					s.Fg = c
				case 48:
					s.Bg = c
				}
			}
		case code == 39:
			s.Fg = DefaultColor
		case code >= 40 && code <= 47:
			s.Bg = IndexColor(uint8(code - 40)) //nolint:gosec // 0 to 7
		case code == 49:
			s.Bg = DefaultColor
		case code >= 90 && code <= 97:
			s.Fg = IndexColor(uint8(code - 90 + 8)) //nolint:gosec // 8 to 15
		case code >= 100 && code <= 107:
			s.Bg = IndexColor(uint8(code - 100 + 8)) //nolint:gosec // 8 to 15
		}
	}
}

// extendedColor parses the color of SGR 38, 48 or 58 at params[i], in either the colon
// form (38:5:n, 38:2::r:g:b or 38:2:r:g:b) or the semicolon form (38;5;n, 38;2;r;g;b). It
// returns the index of the last parameter it used.
func extendedColor(params [][]uint16, i int) (Color, bool, int) {
	ps := params[i]
	var args []uint16
	next := i
	if len(ps) > 1 {
		args = ps[1:]
	} else {
		for j := i + 1; j < len(params) && len(args) < 5; j++ {
			if len(params[j]) > 0 {
				args = append(args, params[j][0])
			}
		}
	}
	if len(args) == 0 {
		return 0, false, i
	}
	switch args[0] {
	case 5:
		if len(args) < 2 { //nolint:mnd // 5;n
			return 0, false, i
		}
		if len(ps) == 1 {
			next = i + 2 //nolint:mnd // 5;n
		}
		return IndexColor(uint8(args[1])), true, next //nolint:gosec // a palette index
	case 2:
		rgb := args[1:]
		// The colon form may have a color space ID before the components.
		if len(ps) > 1 && len(rgb) == 4 {
			rgb = rgb[1:]
		}
		if len(rgb) < 3 { //nolint:mnd // r, g, b
			return 0, false, i
		}
		if len(ps) == 1 {
			next = i + 4 //nolint:mnd // 2;r;g;b
		}
		//nolint:gosec // components are bytes
		return RGBColor(uint8(rgb[0]), uint8(rgb[1]), uint8(rgb[2])), true, next
	}
	return 0, false, i
}

func (p *performer) OscDispatch(params [][]byte, _ bool) {
	e := p.e
	if len(params) == 0 {
		return
	}
	switch string(params[0]) {
	case "0", "2":
		if len(params) > 1 {
			e.title = string(joinOSC(params[1:]))
			if e.OnTitle != nil {
				e.OnTitle(e.title)
			}
		}
	case "8":
		// OSC 8 ; params ; URI. The URI may hold semicolons.
		uri := ""
		if len(params) > 2 { //nolint:mnd // 8, params, URI
			uri = string(joinOSC(params[2:]))
		}
		e.cur.style.Link = e.linkID(uri)
	case "10", "11":
		if len(params) > 1 && string(params[1]) == "?" {
			c := e.DefaultFg
			if string(params[0]) == "11" {
				c = e.DefaultBg
			}
			e.respond(fmt.Sprintf("\x1b]%s;rgb:%02x%02x/%02x%02x/%02x%02x\x1b\\",
				params[0], c[0], c[0], c[1], c[1], c[2], c[2]))
		}
	}
}

func joinOSC(parts [][]byte) []byte {
	strs := make([]string, len(parts))
	for i, p := range parts {
		strs[i] = string(p)
	}
	return []byte(strings.Join(strs, ";"))
}

// String renders the screen as text, one line per row with trailing blanks trimmed, for
// tests and debugging.
func (e *Emulator) String() string {
	var b strings.Builder
	for y := range e.rows {
		b.WriteString(strings.TrimRight(e.Text(0, y, e.cols-1, y), " "))
		b.WriteString("\n")
	}
	return b.String()
}
