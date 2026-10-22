// Package termwidget is a terminal widget for GTK3 that works on Windows, where VTE
// doesn't. It draws an emulator.Emulator in a DrawingArea with Cairo and Pango, and runs
// its program on a pty.Pty: ConPTY on Windows, and a Unix pty on Linux, where it can be
// tried out and tested.
//
// The widget is in this repository for now, but it's meant to stand alone: it doesn't
// import the rest of the application. See README.md for what it does and doesn't do.
//
// Like all GTK widgets, a Terminal may only be used from the GTK main thread.
package termwidget

import (
	"math"
	"os/exec"
	"sync"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/termwidget/emulator"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/termwidget/pty"
)

// Default size, in cells, until the widget is allocated a size.
const (
	defaultCols = 80
	defaultRows = 24
)

// Terminal is a terminal widget.
type Terminal struct {
	*gtk.DrawingArea

	emu   *emulator.Emulator
	pty   pty.Pty
	out   *writer
	fonts fonts
	theme theme

	// mu guards what the reading goroutine hands the main thread.
	mu        sync.Mutex
	pending   []byte
	scheduled bool
	// generation counts started programs, so that output a replaced one wrote late is
	// dropped.
	generation int

	onContents []func()
	sel        selection
	matches    matcher

	mouseButton int     // the button reported down to the program, or ButtonNone
	mouseCell   [2]int  // the cell motion was last reported at
	scrollAccum float64 // smooth scrolling not yet turned into wheel steps
	audibleBell bool
	// sizeRequested is set while SetSize's size request stands.
	sizeRequested bool
}

// New creates a terminal widget with nothing running in it.
func New() (*Terminal, error) {
	da, err := gtk.DrawingAreaNew()
	if err != nil {
		return nil, err
	}
	t := &Terminal{
		DrawingArea: da,
		emu:         emulator.New(defaultCols, defaultRows),
		theme:       defaultTheme(),
		mouseButton: emulator.ButtonNone,
		audibleBell: true,
	}
	t.emu.Respond = t.write
	t.emu.OnBell = t.bell
	t.applyThemeToEmulator()
	t.SetCanFocus(true)
	t.AddEvents(int(gdk.KEY_PRESS_MASK | gdk.BUTTON_PRESS_MASK | gdk.BUTTON_RELEASE_MASK |
		gdk.POINTER_MOTION_MASK | gdk.SCROLL_MASK | gdk.SMOOTH_SCROLL_MASK | gdk.FOCUS_CHANGE_MASK |
		gdk.LEAVE_NOTIFY_MASK))
	t.SetFont("Monospace 11")

	t.Connect("draw", t.draw)
	t.Connect("size-allocate", func() {
		t.fitToAllocation()
		if t.sizeRequested {
			t.sizeRequested = false
			t.setMinimumSize()
		}
	})
	// Input is handled after the application's own handlers, as VTE's class handlers are,
	// so that they can take events first.
	t.ConnectAfter("key-press-event", t.keyPress)
	t.ConnectAfter("button-press-event", t.buttonPress)
	t.ConnectAfter("button-release-event", t.buttonRelease)
	t.ConnectAfter("motion-notify-event", t.motion)
	t.ConnectAfter("scroll-event", t.scroll)
	t.ConnectAfter("focus-in-event", func() bool { t.focusChanged(true); return false })
	t.ConnectAfter("focus-out-event", func() bool { t.focusChanged(false); return false })
	return t, nil
}

// Emulator returns the terminal's emulator, for reading the screen.
func (t *Terminal) Emulator() *emulator.Emulator { return t.emu }

// Start runs cmd on a new terminal, closing any it was running on, which hangs that
// program up. It returns the path of the terminal device, or "" where there is none. The
// caller waits for cmd.
func (t *Terminal) Start(cmd *exec.Cmd) (string, error) {
	t.closePty()
	cols, rows := t.emu.Size()
	p, err := pty.Start(cmd, cols, rows)
	if err != nil {
		return "", err
	}
	t.mu.Lock()
	t.generation++
	generation := t.generation
	t.pending = nil
	t.mu.Unlock()
	t.pty, t.out = p, newWriter(p)
	go t.read(p, generation)
	return p.Name(), nil
}

// Close hangs up the running program, if any.
func (t *Terminal) Close() { t.closePty() }

func (t *Terminal) closePty() {
	if t.pty == nil {
		return
	}
	t.out.close()
	_ = t.pty.Close()
	t.pty, t.out = nil, nil
}

// read hands what the program writes to the main thread.
func (t *Terminal) read(p pty.Pty, generation int) {
	buf := make([]byte, 64*1024) //nolint:mnd // a read's worth
	for {
		n, err := p.Read(buf)
		if n > 0 {
			t.mu.Lock()
			if generation != t.generation {
				t.mu.Unlock()
				return
			}
			t.pending = append(t.pending, buf[:n]...)
			schedule := !t.scheduled
			t.scheduled = true
			t.mu.Unlock()
			if schedule {
				glib.IdleAdd(t.flush)
			}
		}
		if err != nil {
			return
		}
	}
}

// flush feeds the emulator what the program wrote since the last flush.
func (t *Terminal) flush() {
	t.mu.Lock()
	data := t.pending
	t.pending, t.scheduled = nil, false
	t.mu.Unlock()
	t.Feed(data)
}

// Feed shows data as if the terminal's program had written it.
func (t *Terminal) Feed(data []byte) {
	if len(data) == 0 {
		return
	}
	_, _ = t.emu.Write(data)
	t.QueueDraw()
	for _, fn := range t.onContents {
		fn()
	}
}

// write sends input to the program.
func (t *Terminal) write(b []byte) {
	if t.out != nil && len(b) > 0 {
		t.out.write(b)
	}
}

// OnContentsChanged calls fn whenever the visible contents change.
func (t *Terminal) OnContentsChanged(fn func()) {
	t.onContents = append(t.onContents, fn)
}

// SetSize sets the terminal's size in cells, and asks for room for it, as VTE does. The
// widget's allocation sets the size again when it changes.
func (t *Terminal) SetSize(cols, rows int) {
	t.resize(cols, rows)
	// GTK can't be told a natural size without subclassing, so this asks for a minimum
	// size until the next allocation, and then lets the widget shrink again.
	t.sizeRequested = true
	t.SetSizeRequest(int(math.Ceil(float64(cols)*t.fonts.cellW)), int(math.Ceil(float64(rows)*t.fonts.cellH)))
}

// setMinimumSize lets the widget shrink to two cells.
func (t *Terminal) setMinimumSize() {
	t.SetSizeRequest(int(math.Ceil(t.fonts.cellW*2)), int(t.fonts.cellH)) //nolint:mnd // two cells
}

// Size returns the terminal's size in cells.
func (t *Terminal) Size() (int, int) { return t.emu.Size() }

func (t *Terminal) resize(cols, rows int) {
	if c, r := t.emu.Size(); c == cols && r == rows {
		return
	}
	t.emu.Resize(cols, rows)
	t.sel.clear()
	if t.pty != nil {
		_ = t.pty.Resize(cols, rows)
	}
	t.QueueDraw()
}

// fitToAllocation sizes the terminal to as many cells as fit in the widget.
func (t *Terminal) fitToAllocation() {
	w, h := float64(t.GetAllocatedWidth()), float64(t.GetAllocatedHeight())
	if w <= 0 || h <= 0 {
		return
	}
	cols := max(int(math.Floor(w/t.fonts.cellW)), 1)
	rows := max(int(math.Floor(h/t.fonts.cellH)), 1)
	t.resize(cols, rows)
}

// CellAt returns the column and row of the screen at a point in the widget.
func (t *Terminal) CellAt(x, y float64) (int, int) {
	return int(math.Floor(x / t.fonts.cellW)), int(math.Floor(y / t.fonts.cellH))
}

// clampedCellAt is CellAt kept on the screen.
func (t *Terminal) clampedCellAt(x, y float64) (int, int) {
	col, row := t.CellAt(x, y)
	cols, rows := t.emu.Size()
	return min(max(col, 0), cols-1), min(max(row, 0), rows-1)
}

// SetAudibleBell sets whether the bell makes a sound.
func (t *Terminal) SetAudibleBell(b bool) { t.audibleBell = b }

func (t *Terminal) bell() {
	if !t.audibleBell {
		return
	}
	if d, err := gdk.DisplayGetDefault(); err == nil {
		d.Beep()
	}
}

func (t *Terminal) focusChanged(focused bool) {
	t.write(t.emu.EncodeFocus(focused))
	t.QueueDraw()
}

// writer writes input to the program from its own goroutine, in order, so that a program
// that's slow to read doesn't block the main thread.
type writer struct {
	mu     sync.Mutex
	cond   *sync.Cond
	queue  [][]byte
	closed bool
}

func newWriter(p pty.Pty) *writer {
	w := &writer{}
	w.cond = sync.NewCond(&w.mu)
	go func() {
		for {
			w.mu.Lock()
			for len(w.queue) == 0 && !w.closed {
				w.cond.Wait()
			}
			if w.closed {
				w.mu.Unlock()
				return
			}
			b := w.queue[0]
			w.queue = w.queue[1:]
			w.mu.Unlock()
			if _, err := p.Write(b); err != nil {
				return
			}
		}
	}()
	return w
}

func (w *writer) write(b []byte) {
	w.mu.Lock()
	w.queue = append(w.queue, append([]byte(nil), b...))
	w.mu.Unlock()
	w.cond.Signal()
}

func (w *writer) close() {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	w.cond.Signal()
}
