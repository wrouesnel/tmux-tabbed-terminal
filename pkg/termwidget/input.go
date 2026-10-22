package termwidget

import (
	"math"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/gtk"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/termwidget/emulator"
)

// keys are the keys that send escape sequences rather than characters.
//
//nolint:gochecknoglobals
var keys = map[uint]emulator.Key{
	gdk.KEY_Up:           emulator.KeyUp,
	gdk.KEY_KP_Up:        emulator.KeyUp,
	gdk.KEY_Down:         emulator.KeyDown,
	gdk.KEY_KP_Down:      emulator.KeyDown,
	gdk.KEY_Right:        emulator.KeyRight,
	gdk.KEY_KP_Right:     emulator.KeyRight,
	gdk.KEY_Left:         emulator.KeyLeft,
	gdk.KEY_KP_Left:      emulator.KeyLeft,
	gdk.KEY_Home:         emulator.KeyHome,
	gdk.KEY_KP_Home:      emulator.KeyHome,
	gdk.KEY_End:          emulator.KeyEnd,
	gdk.KEY_KP_End:       emulator.KeyEnd,
	gdk.KEY_Insert:       emulator.KeyInsert,
	gdk.KEY_KP_Insert:    emulator.KeyInsert,
	gdk.KEY_Delete:       emulator.KeyDelete,
	gdk.KEY_KP_Delete:    emulator.KeyDelete,
	gdk.KEY_Page_Up:      emulator.KeyPageUp,
	gdk.KEY_KP_Page_Up:   emulator.KeyPageUp,
	gdk.KEY_Page_Down:    emulator.KeyPageDown,
	gdk.KEY_KP_Page_Down: emulator.KeyPageDown,
	gdk.KEY_BackSpace:    emulator.KeyBackspace,
	gdk.KEY_Tab:          emulator.KeyTab,
	gdk.KEY_ISO_Left_Tab: emulator.KeyTab,
	gdk.KEY_Return:       emulator.KeyEnter,
	gdk.KEY_KP_Enter:     emulator.KeyEnter,
	gdk.KEY_Escape:       emulator.KeyEscape,
	gdk.KEY_F1:           emulator.KeyF1,
	gdk.KEY_F2:           emulator.KeyF2,
	gdk.KEY_F3:           emulator.KeyF3,
	gdk.KEY_F4:           emulator.KeyF4,
	gdk.KEY_F5:           emulator.KeyF5,
	gdk.KEY_F6:           emulator.KeyF6,
	gdk.KEY_F7:           emulator.KeyF7,
	gdk.KEY_F8:           emulator.KeyF8,
	gdk.KEY_F9:           emulator.KeyF9,
	gdk.KEY_F10:          emulator.KeyF10,
	gdk.KEY_F11:          emulator.KeyF11,
	gdk.KEY_F12:          emulator.KeyF12,
}

func modsOf(state gdk.ModifierType) emulator.Mod {
	var m emulator.Mod
	if state&gdk.SHIFT_MASK != 0 {
		m |= emulator.ModShift
	}
	if state&gdk.MOD1_MASK != 0 {
		m |= emulator.ModAlt
	}
	if state&gdk.CONTROL_MASK != 0 {
		m |= emulator.ModCtrl
	}
	return m
}

func (t *Terminal) keyPress(_ interface{}, ev *gdk.Event) bool {
	key := gdk.EventKeyNewFromEvent(ev)
	keyval := key.KeyVal()
	mods := modsOf(gdk.ModifierType(key.State()))
	if keyval == gdk.KEY_ISO_Left_Tab {
		mods |= emulator.ModShift
	}
	if k, ok := keys[keyval]; ok {
		t.typed(t.emu.EncodeKey(k, mods))
		return true
	}
	// The keyval is already shifted, so Shift isn't passed on with characters.
	if r := gdk.KeyvalToUnicode(keyval); r != 0 {
		t.typed(emulator.EncodeRune(r, mods&^emulator.ModShift))
		return true
	}
	return false
}

// typed sends typed input to the program, which ends any selection, as typing does.
func (t *Terminal) typed(b []byte) {
	if t.sel.active() {
		t.sel.clear()
		t.QueueDraw()
	}
	t.write(b)
}

// mouseButtons maps GDK's buttons to the program's.
//
//nolint:gochecknoglobals
var mouseButtons = map[uint]int{1: emulator.ButtonLeft, 2: emulator.ButtonMiddle, 3: emulator.ButtonRight}

// reporting reports whether mouse events go to the program: it asked for them, and Shift,
// which selects text instead, isn't held.
func (t *Terminal) reporting(state gdk.ModifierType) bool {
	return t.emu.Modes().Mouse != emulator.MouseNone && state&gdk.SHIFT_MASK == 0
}

func (t *Terminal) buttonPress(_ interface{}, ev *gdk.Event) bool {
	b := gdk.EventButtonNewFromEvent(ev)
	t.GrabFocus()
	if b.Type() != gdk.EVENT_BUTTON_PRESS {
		return true
	}
	state := gdk.ModifierType(b.State())
	col, row := t.clampedCellAt(b.X(), b.Y())
	button, known := mouseButtons[uint(b.Button())]
	if t.reporting(state) {
		if known {
			t.mouseButton, t.mouseCell = button, [2]int{col, row}
			t.write(t.emu.EncodeMouse(emulator.MousePress, button, col, row, modsOf(state)))
		}
		return true
	}
	switch b.Button() {
	case gdk.BUTTON_PRIMARY:
		t.sel.start(col, row)
		t.QueueDraw()
	case gdk.BUTTON_MIDDLE:
		t.pasteFrom(gdk.SELECTION_PRIMARY)
	}
	return true
}

func (t *Terminal) buttonRelease(_ interface{}, ev *gdk.Event) bool {
	b := gdk.EventButtonNewFromEvent(ev)
	state := gdk.ModifierType(b.State())
	col, row := t.clampedCellAt(b.X(), b.Y())
	if t.mouseButton != emulator.ButtonNone {
		t.write(t.emu.EncodeMouse(emulator.MouseRelease, t.mouseButton, col, row, modsOf(state)))
		t.mouseButton = emulator.ButtonNone
		return true
	}
	if b.Button() == gdk.BUTTON_PRIMARY && t.sel.dragging {
		t.sel.dragging = false
		if t.sel.empty() {
			t.sel.clear()
		} else if cb, err := gtk.ClipboardGet(gdk.SELECTION_PRIMARY); err == nil {
			cb.SetText(t.selectedText())
		}
		t.QueueDraw()
	}
	return true
}

func (t *Terminal) motion(_ interface{}, ev *gdk.Event) bool {
	m := gdk.EventMotionNewFromEvent(ev)
	x, y := m.MotionVal()
	col, row := t.clampedCellAt(x, y)
	t.matches.hover(t, x, y)
	if t.reporting(m.State()) {
		if [2]int{col, row} != t.mouseCell {
			t.mouseCell = [2]int{col, row}
			t.write(t.emu.EncodeMouse(emulator.MouseMotion, t.mouseButton, col, row, modsOf(m.State())))
		}
		return false
	}
	if t.sel.dragging {
		t.sel.extend(col, row)
		t.QueueDraw()
	}
	return false
}

func (t *Terminal) scroll(_ interface{}, ev *gdk.Event) bool {
	s := gdk.EventScrollNewFromEvent(ev)
	steps := 0
	switch s.Direction() {
	case gdk.SCROLL_UP:
		steps = -1
	case gdk.SCROLL_DOWN:
		steps = 1
	case gdk.SCROLL_SMOOTH:
		t.scrollAccum += s.DeltaY()
		steps = int(t.scrollAccum)
		t.scrollAccum -= float64(steps)
	default:
		return false
	}
	if steps == 0 {
		return true
	}
	state := gdk.ModifierType(s.State())
	col, row := t.clampedCellAt(s.X(), s.Y())
	button, key := emulator.ButtonWheelDown, emulator.KeyDown
	if steps < 0 {
		button, key = emulator.ButtonWheelUp, emulator.KeyUp
	}
	n := int(math.Abs(float64(steps)))
	switch {
	case t.reporting(state):
		for range n {
			t.write(t.emu.EncodeMouse(emulator.MousePress, button, col, row, modsOf(state)))
		}
	case t.emu.AltScreen():
		// Full-screen programs without the mouse scroll by cursor keys, as in VTE.
		const linesPerStep = 3
		for range n * linesPerStep {
			t.write(t.emu.EncodeKey(key, 0))
		}
	}
	return true
}

// selection is a range of selected cells, from an anchor to the cell dragged to.
type selection struct {
	on                       bool
	dragging                 bool
	anchorX, anchorY, hx, hy int
}

func (s *selection) start(x, y int) {
	*s = selection{on: true, dragging: true, anchorX: x, anchorY: y, hx: x, hy: y}
}

func (s *selection) extend(x, y int) { s.hx, s.hy = x, y }
func (s *selection) clear()          { *s = selection{} }
func (s *selection) active() bool    { return s.on }
func (s *selection) empty() bool     { return s.anchorX == s.hx && s.anchorY == s.hy }

// bounds returns the selection's first and last cells, in reading order.
func (s *selection) bounds() (int, int, int, int) {
	if s.hy < s.anchorY || (s.hy == s.anchorY && s.hx < s.anchorX) {
		return s.hx, s.hy, s.anchorX, s.anchorY
	}
	return s.anchorX, s.anchorY, s.hx, s.hy
}

func (s *selection) contains(x, y int) bool {
	if !s.on || s.empty() {
		return false
	}
	x0, y0, x1, y1 := s.bounds()
	return (y > y0 || (y == y0 && x >= x0)) && (y < y1 || (y == y1 && x <= x1))
}

func (t *Terminal) selectedText() string {
	if !t.sel.on || t.sel.empty() {
		return ""
	}
	return t.emu.Text(t.sel.bounds())
}

// HasSelection reports whether any text is selected.
func (t *Terminal) HasSelection() bool { return t.sel.on && !t.sel.empty() }

// CopyClipboard copies the selection to the clipboard as text.
func (t *Terminal) CopyClipboard() {
	if !t.HasSelection() {
		return
	}
	if cb, err := gtk.ClipboardGet(gdk.SELECTION_CLIPBOARD); err == nil {
		cb.SetText(t.selectedText())
	}
}

// PasteClipboard pastes the clipboard into the terminal.
func (t *Terminal) PasteClipboard() { t.pasteFrom(gdk.SELECTION_CLIPBOARD) }

func (t *Terminal) pasteFrom(atom gdk.Atom) {
	cb, err := gtk.ClipboardGet(atom)
	if err != nil {
		return
	}
	text, err := cb.WaitForText()
	if err != nil || text == "" {
		return
	}
	t.typed(t.emu.EncodePaste(text))
}
