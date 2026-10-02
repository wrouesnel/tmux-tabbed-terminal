// Package vte binds the parts of the VTE terminal widget (vte-2.91, GTK3) this program
// uses, as a gotk3 widget.
//
// Child processes are started with os/exec on a pty created by VTE, rather than through
// vte_terminal_spawn_async, so that Go owns the process and knows its tty.
package vte

/*
#cgo pkg-config: vte-2.91 gtk+-3.0
#include <stdlib.h>
#include <vte/vte.h>

static VteTerminal *to_terminal(void *p) { return VTE_TERMINAL(p); }
*/
import "C"

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

// Color is an RGBA color with components from 0 to 1.
type Color struct {
	R, G, B, A float64
}

func (c Color) native() C.GdkRGBA {
	return C.GdkRGBA{red: C.gdouble(c.R), green: C.gdouble(c.G), blue: C.gdouble(c.B), alpha: C.gdouble(c.A)}
}

// Terminal is a VteTerminal widget.
type Terminal struct {
	*gtk.Widget
}

// New creates a terminal widget.
func New() *Terminal {
	obj := glib.Take(unsafe.Pointer(C.vte_terminal_new()))
	return &Terminal{Widget: &gtk.Widget{InitiallyUnowned: glib.InitiallyUnowned{Object: obj}}}
}

func (t *Terminal) native() *C.VteTerminal {
	return C.to_terminal(unsafe.Pointer(t.GObject))
}

func gbool(b bool) C.gboolean {
	if b {
		return C.TRUE
	}
	return C.FALSE
}

// takeError converts a GError to a Go error and frees it.
func takeError(gerr *C.GError) error {
	if gerr == nil {
		return nil
	}
	defer C.g_error_free(gerr)
	return errors.New(C.GoString(gerr.message))
}

// Start runs cmd on a new pty attached to the terminal, as the session leader with the
// pty as its controlling terminal. It returns the path of the pty's tty. The caller
// waits for the process.
func (t *Terminal) Start(cmd *exec.Cmd) (string, error) {
	var gerr *C.GError
	pty := C.vte_pty_new_sync(C.VTE_PTY_DEFAULT, nil, &gerr)
	if pty == nil {
		return "", errors.Wrap(takeError(gerr), "vte_pty_new_sync")
	}
	// The terminal keeps its own reference.
	defer C.g_object_unref(C.gpointer(pty))
	C.vte_terminal_set_pty(t.native(), pty)

	masterFd := int(C.vte_pty_get_fd(pty))
	// VTE before 0.54, as on RHEL 8, leaves the pty locked until its own child setup,
	// which this doesn't use: unlock it, as unlockpt does.
	if err := unix.IoctlSetPointerInt(masterFd, unix.TIOCSPTLCK, 0); err != nil {
		return "", errors.Wrap(err, "unlocking the pty")
	}
	ptyNum, err := unix.IoctlGetUint32(masterFd, unix.TIOCGPTN)
	if err != nil {
		return "", errors.Wrap(err, "TIOCGPTN")
	}
	ttyPath := fmt.Sprintf("/dev/pts/%d", ptyNum)

	slave, err := os.OpenFile(ttyPath, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return "", errors.Wrap(err, "opening pty slave")
	}
	defer slave.Close()

	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		return "", errors.Wrapf(err, "starting %s", cmd.Path)
	}
	return ttyPath, nil
}

// SetFont sets the font from a Pango font description such as "Monospace 11".
func (t *Terminal) SetFont(desc string) {
	cdesc := C.CString(desc)
	defer C.free(unsafe.Pointer(cdesc))
	fd := C.pango_font_description_from_string(cdesc)
	defer C.pango_font_description_free(fd)
	C.vte_terminal_set_font(t.native(), fd)
}

// SetColors sets the foreground, background and palette. The palette has 0, 8, 16, 232
// or 256 entries. An empty palette restores the default.
func (t *Terminal) SetColors(fg, bg Color, palette []Color) {
	cfg, cbg := fg.native(), bg.native()
	var cpal *C.GdkRGBA
	if len(palette) > 0 {
		arr := make([]C.GdkRGBA, len(palette))
		for i, c := range palette {
			arr[i] = c.native()
		}
		cpal = &arr[0]
	}
	C.vte_terminal_set_colors(t.native(), &cfg, &cbg, cpal, C.gsize(len(palette)))
}

// SetCursorColors sets the cursor color and the color of text under it. A nil color
// restores the default.
func (t *Terminal) SetCursorColors(cursor, cursorFg *Color) {
	if cursor == nil {
		C.vte_terminal_set_color_cursor(t.native(), nil)
	} else {
		c := cursor.native()
		C.vte_terminal_set_color_cursor(t.native(), &c)
	}
	if cursorFg == nil {
		C.vte_terminal_set_color_cursor_foreground(t.native(), nil)
	} else {
		c := cursorFg.native()
		C.vte_terminal_set_color_cursor_foreground(t.native(), &c)
	}
}

// SetBoldIsBright sets whether bold text uses the bright palette colors.
func (t *Terminal) SetBoldIsBright(b bool) {
	C.vte_terminal_set_bold_is_bright(t.native(), gbool(b))
}

// SetScrollbackLines sets the scrollback length.
func (t *Terminal) SetScrollbackLines(lines int) {
	C.vte_terminal_set_scrollback_lines(t.native(), C.glong(lines))
}

// SetAudibleBell sets whether the bell makes a sound.
func (t *Terminal) SetAudibleBell(b bool) {
	C.vte_terminal_set_audible_bell(t.native(), gbool(b))
}

// SetMouseAutohide sets whether the pointer hides while typing.
func (t *Terminal) SetMouseAutohide(b bool) {
	C.vte_terminal_set_mouse_autohide(t.native(), gbool(b))
}

// SetFontScale sets the font zoom factor.
func (t *Terminal) SetFontScale(scale float64) {
	C.vte_terminal_set_font_scale(t.native(), C.gdouble(scale))
}

// FontScale returns the font zoom factor.
func (t *Terminal) FontScale() float64 {
	return float64(C.vte_terminal_get_font_scale(t.native()))
}

// HasSelection reports whether any text is selected.
func (t *Terminal) HasSelection() bool {
	return C.vte_terminal_get_has_selection(t.native()) != 0
}

// CopyClipboard copies the selection to the clipboard as text.
func (t *Terminal) CopyClipboard() {
	C.vte_terminal_copy_clipboard_format(t.native(), C.VTE_FORMAT_TEXT)
}

// PasteClipboard pastes the clipboard into the terminal.
func (t *Terminal) PasteClipboard() {
	C.vte_terminal_paste_clipboard(t.native())
}

// Size returns the terminal size in cells.
func (t *Terminal) Size() (int, int) {
	return int(C.vte_terminal_get_column_count(t.native())), int(C.vte_terminal_get_row_count(t.native()))
}
