package emulator

import (
	"fmt"
)

// Key is a key that doesn't type a character.
type Key int

// Keys with their own escape sequences.
const (
	KeyNone Key = iota
	KeyUp
	KeyDown
	KeyRight
	KeyLeft
	KeyHome
	KeyEnd
	KeyInsert
	KeyDelete
	KeyPageUp
	KeyPageDown
	KeyBackspace
	KeyTab
	KeyEnter
	KeyEscape
	KeyF1
	KeyF2
	KeyF3
	KeyF4
	KeyF5
	KeyF6
	KeyF7
	KeyF8
	KeyF9
	KeyF10
	KeyF11
	KeyF12
)

// Mod is a set of modifier keys.
type Mod int

// Modifiers.
const (
	ModShift Mod = 1 << iota
	ModAlt
	ModCtrl
)

// xtermMod is the modifier parameter of xterm's sequences: 1 plus the modifier bits.
func xtermMod(mods Mod) int { return 1 + int(mods) }

// EncodeKey returns what a key sends, with modifiers, as xterm does.
//
//nolint:cyclop // a switch over the keys
func (e *Emulator) EncodeKey(k Key, mods Mod) []byte {
	cursorKey := func(final byte) []byte {
		switch {
		case mods != 0:
			return fmt.Appendf(nil, "\x1b[1;%d%c", xtermMod(mods), final)
		case e.modes.AppCursor:
			return []byte{0x1b, 'O', final}
		}
		return []byte{0x1b, '[', final}
	}
	tilde := func(n int) []byte {
		if mods != 0 {
			return fmt.Appendf(nil, "\x1b[%d;%d~", n, xtermMod(mods))
		}
		return fmt.Appendf(nil, "\x1b[%d~", n)
	}
	ss3 := func(final byte) []byte {
		if mods != 0 {
			return fmt.Appendf(nil, "\x1b[1;%d%c", xtermMod(mods), final)
		}
		return []byte{0x1b, 'O', final}
	}
	alt := func(b []byte) []byte {
		if mods&ModAlt != 0 {
			return append([]byte{0x1b}, b...)
		}
		return b
	}
	switch k {
	case KeyUp:
		return cursorKey('A')
	case KeyDown:
		return cursorKey('B')
	case KeyRight:
		return cursorKey('C')
	case KeyLeft:
		return cursorKey('D')
	case KeyHome:
		return cursorKey('H')
	case KeyEnd:
		return cursorKey('F')
	case KeyInsert:
		return tilde(2) //nolint:mnd // xterm's code
	case KeyDelete:
		return tilde(3) //nolint:mnd // xterm's code
	case KeyPageUp:
		return tilde(5) //nolint:mnd // xterm's code
	case KeyPageDown:
		return tilde(6) //nolint:mnd // xterm's code
	case KeyBackspace:
		if mods&ModCtrl != 0 {
			return alt([]byte{0x08})
		}
		return alt([]byte{0x7f})
	case KeyTab:
		if mods&ModShift != 0 {
			return []byte("\x1b[Z")
		}
		return alt([]byte{'\t'})
	case KeyEnter:
		return alt([]byte{'\r'})
	case KeyEscape:
		return alt([]byte{0x1b})
	case KeyF1, KeyF2, KeyF3, KeyF4:
		return ss3(byte('P' + k - KeyF1))
	case KeyF5, KeyF6, KeyF7, KeyF8, KeyF9, KeyF10, KeyF11, KeyF12:
		codes := [...]int{15, 17, 18, 19, 20, 21, 23, 24}
		return tilde(codes[k-KeyF5])
	}
	return nil
}

// EncodeRune returns what typing a character sends: Ctrl makes control characters of
// letters and some punctuation, and Alt prefixes ESC.
func EncodeRune(r rune, mods Mod) []byte {
	var b []byte
	if mods&ModCtrl != 0 {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			b = []byte{byte(r) & 0x1f}
		case r == ' ' || r == '@' || r == '2':
			b = []byte{0}
		case r >= '[' && r <= '_':
			b = []byte{byte(r) - 0x40}
		case r >= '3' && r <= '7':
			b = []byte{byte(r) - '3' + 0x1b}
		case r == '/':
			b = []byte{0x1f}
		case r == '8' || r == '?':
			b = []byte{0x7f}
		}
	}
	if b == nil {
		b = []byte(string(r))
	}
	if mods&ModAlt != 0 {
		b = append([]byte{0x1b}, b...)
	}
	return b
}

// MouseAction is what happened to the mouse.
type MouseAction int

// Mouse actions.
const (
	MousePress MouseAction = iota
	MouseRelease
	MouseMotion
)

// Mouse buttons, as the program sees them. Wheel up and down are buttons 4 and 5.
const (
	ButtonLeft      = 0
	ButtonMiddle    = 1
	ButtonRight     = 2
	ButtonNone      = 3
	ButtonWheelUp   = 64
	ButtonWheelDown = 65
)

// EncodeMouse returns what a mouse event at cell (x, y) sends, or nil when the program
// didn't ask for it. button is one of the Button constants: ButtonNone for motion
// without a button down.
func (e *Emulator) EncodeMouse(action MouseAction, button int, x, y int, mods Mod) []byte {
	m := e.modes.Mouse
	switch {
	case m == MouseNone:
		return nil
	case m == MouseX10 && action != MousePress:
		return nil
	case action == MouseMotion && m < MouseButton:
		return nil
	case action == MouseMotion && m == MouseButton && button == ButtonNone:
		return nil
	}
	code := button
	if m != MouseX10 {
		if mods&ModShift != 0 {
			code |= 4
		}
		if mods&ModAlt != 0 {
			code |= 8
		}
		if mods&ModCtrl != 0 {
			code |= 16
		}
	}
	if action == MouseMotion {
		code |= 32
	}
	if e.modes.MouseSGR {
		final := 'M'
		if action == MouseRelease {
			final = 'm'
		}
		return fmt.Appendf(nil, "\x1b[<%d;%d;%d%c", code, x+1, y+1, final)
	}
	if action == MouseRelease {
		code = code&^3 | ButtonNone
	}
	// The legacy encoding can't send positions beyond column or row 223.
	const maxPos = 255 - 32 - 1
	return []byte{0x1b, '[', 'M', byte(code + 32), byte(min(x, maxPos) + 33), byte(min(y, maxPos) + 33)} //nolint:gosec // bounded
}

// EncodeFocus returns what gaining or losing focus sends, or nil when the program didn't
// ask for it.
func (e *Emulator) EncodeFocus(focused bool) []byte {
	if !e.modes.FocusEvents {
		return nil
	}
	if focused {
		return []byte("\x1b[I")
	}
	return []byte("\x1b[O")
}

// EncodePaste returns what pasting text sends: the text bracketed when the program asked
// for bracketed paste, with newlines as carriage returns, as a typed Enter.
func (e *Emulator) EncodePaste(text string) []byte {
	b := make([]byte, 0, len(text)+12) //nolint:mnd // the brackets
	if e.modes.BracketedPaste {
		b = append(b, "\x1b[200~"...)
	}
	for i := 0; i < len(text); i++ {
		switch c := text[i]; c {
		case '\r':
			if i+1 < len(text) && text[i+1] == '\n' {
				i++
			}
			b = append(b, '\r')
		case '\n':
			b = append(b, '\r')
		case 0x1b:
			// An ESC could end the bracketing early.
			if !e.modes.BracketedPaste {
				b = append(b, c)
			}
		default:
			b = append(b, c)
		}
	}
	if e.modes.BracketedPaste {
		b = append(b, "\x1b[201~"...)
	}
	return b
}
