//go:build !windows && !nativeterm

package vte

/*
#cgo pkg-config: vte-2.91 gtk+-3.0
#include <stdlib.h>
#include <vte/vte.h>

static VteTerminal *to_terminal(void *p) { return VTE_TERMINAL(p); }

// SIXEL support is a build option of VTE, and its API appeared in VTE 0.62. Older VTE, as
// on RHEL 8, has neither, so these report it as missing there.
static gboolean ttt_sixel_supported(void) {
#if VTE_CHECK_VERSION(0, 62, 0)
	return (vte_get_feature_flags() & VTE_FEATURE_FLAG_SIXEL) != 0;
#else
	return FALSE;
#endif
}

static void ttt_set_enable_sixel(VteTerminal *terminal, gboolean enable) {
#if VTE_CHECK_VERSION(0, 62, 0)
	vte_terminal_set_enable_sixel(terminal, enable);
#endif
}

// PCRE2 compile options, from pcre2.h, which VTE's headers don't include.
#define TTT_PCRE2_CASELESS  0x00000008u
#define TTT_PCRE2_MULTILINE 0x00000400u
#define TTT_PCRE2_UCP       0x00020000u

// ttt_match_add adds a regex VTE highlights under the pointer, returning its tag, or -1
// with error set if it doesn't compile.
static int ttt_match_add(VteTerminal *terminal, const char *pattern, GError **error) {
	VteRegex *regex = vte_regex_new_for_match(pattern, -1,
		TTT_PCRE2_MULTILINE | TTT_PCRE2_UCP | TTT_PCRE2_CASELESS, error);
	if (regex == NULL) {
		return -1;
	}
	int tag = vte_terminal_match_add_regex(terminal, regex, 0);
	vte_regex_unref(regex);
	vte_terminal_match_set_cursor_name(terminal, tag, "pointer");
	return tag;
}

// ttt_cell_at returns the cell at a point of the terminal widget, which pads its text by
// its CSS padding.
static void ttt_cell_at(VteTerminal *terminal, double x, double y, glong *col, glong *row) {
	GtkStyleContext *style = gtk_widget_get_style_context(GTK_WIDGET(terminal));
	GtkBorder padding;
	gtk_style_context_get_padding(style, gtk_style_context_get_state(style), &padding);
	*col = (glong)((x - padding.left) / vte_terminal_get_char_width(terminal));
	*row = (glong)((y - padding.top) / vte_terminal_get_char_height(terminal));
}

// ttt_match_at_cell returns the regex match at a cell, which the caller frees, or NULL.
// vte_terminal_check_match_at replaces it, but only from VTE 0.70.
static char *ttt_match_at_cell(VteTerminal *terminal, glong col, glong row, int *tag) {
	G_GNUC_BEGIN_IGNORE_DEPRECATIONS
	return vte_terminal_match_check(terminal, col, row, tag);
	G_GNUC_END_IGNORE_DEPRECATIONS
}
*/
import "C"

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

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

// OnContentsChanged calls fn whenever the visible contents change.
func (t *Terminal) OnContentsChanged(fn func()) {
	t.Connect("contents-changed", fn)
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

// SixelSupported reports whether this VTE was built with SIXEL image support. It's a
// build option most distributions leave off, so terminals can only show SIXEL images where
// it's on.
func SixelSupported() bool {
	return C.ttt_sixel_supported() != 0
}

// SetEnableSixel turns SIXEL image support on or off. It does nothing where VTE wasn't
// built with it: check SixelSupported.
func (t *Terminal) SetEnableSixel(enable bool) {
	C.ttt_set_enable_sixel(t.native(), gbool(enable))
}

// SetAllowHyperlink sets whether the terminal follows OSC 8 hyperlinks, which programs
// use to show a label for a URL. Hovering one underlines it.
func (t *Terminal) SetAllowHyperlink(allow bool) {
	C.vte_terminal_set_allow_hyperlink(t.native(), gbool(allow))
}

// AddMatch adds a PCRE2 regex, matched case-insensitively within one line, whose matches
// are underlined, with a hand pointer, under the mouse. It returns the tag MatchAt reports
// for it.
func (t *Terminal) AddMatch(pattern string) (int, error) {
	cpattern := C.CString(pattern)
	defer C.free(unsafe.Pointer(cpattern))
	var gerr *C.GError
	tag := C.ttt_match_add(t.native(), cpattern, &gerr)
	if tag < 0 {
		return 0, errors.Wrapf(takeError(gerr), "regex %q", pattern)
	}
	return int(tag), nil
}

// MatchAt returns the link under a mouse event, preferring a hyperlink to a regex match.
// ok is false when there's neither.
func (t *Terminal) MatchAt(ev *gdk.Event) (Match, bool) {
	cev := (*C.GdkEvent)(unsafe.Pointer(ev.GdkEvent))
	if uri := C.vte_terminal_hyperlink_check_event(t.native(), cev); uri != nil {
		defer C.g_free(C.gpointer(uri))
		return Match{Text: C.GoString(uri), Hyperlink: true}, true
	}
	var tag C.int
	if text := C.vte_terminal_match_check_event(t.native(), cev, &tag); text != nil {
		defer C.g_free(C.gpointer(text))
		return Match{Text: C.GoString(text), Tag: int(tag)}, true
	}
	return Match{}, false
}

// CellAt returns the column and row of the screen at a point in the widget, such as a
// mouse event's.
func (t *Terminal) CellAt(x, y float64) (int, int) {
	var col, row C.glong
	C.ttt_cell_at(t.native(), C.double(x), C.double(y), &col, &row)
	return int(col), int(row)
}

// MatchAtCell returns the AddMatch regex match covering a cell of the screen, as
// MatchAt does for the cell under the pointer.
func (t *Terminal) MatchAtCell(col, row int) (Match, bool) {
	var tag C.int
	text := C.ttt_match_at_cell(t.native(), C.glong(col), C.glong(row), &tag)
	if text == nil {
		return Match{}, false
	}
	defer C.g_free(C.gpointer(text))
	return Match{Text: C.GoString(text), Tag: int(tag)}, true
}

// Feed shows data as if the terminal's program had written it.
func (t *Terminal) Feed(data []byte) {
	if len(data) == 0 {
		return
	}
	C.vte_terminal_feed(t.native(), (*C.char)(unsafe.Pointer(&data[0])), C.gssize(len(data)))
}

// SetSize sets the terminal's size in cells.
func (t *Terminal) SetSize(cols, rows int) {
	C.vte_terminal_set_size(t.native(), C.glong(cols), C.glong(rows))
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
