//go:build windows

package vte

import (
	"os/exec"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/gtk"
	"github.com/pkg/errors"
)

// ErrUnsupported is returned when starting a process in a terminal on Windows, which VTE
// doesn't support.
var ErrUnsupported = errors.New("terminals are not supported on Windows yet")

// Terminal is a placeholder for the terminal widget: a label saying terminals aren't
// supported. Its settings do nothing.
type Terminal struct {
	*gtk.Widget
}

// New creates a placeholder terminal widget.
func New() *Terminal {
	label, err := gtk.LabelNew(ErrUnsupported.Error())
	if err != nil {
		panic(err)
	}
	return &Terminal{Widget: &label.Widget}
}

// Start fails with ErrUnsupported.
func (t *Terminal) Start(cmd *exec.Cmd) (string, error) {
	return "", errors.Wrapf(ErrUnsupported, "starting %s", cmd.Path)
}

// OnContentsChanged does nothing: the placeholder has no contents.
func (t *Terminal) OnContentsChanged(func()) {}

// SetFont does nothing.
func (t *Terminal) SetFont(string) {}

// SetColors does nothing.
func (t *Terminal) SetColors(Color, Color, []Color) {}

// SetCursorColors does nothing.
func (t *Terminal) SetCursorColors(*Color, *Color) {}

// SixelSupported reports false.
func SixelSupported() bool { return false }

// SetEnableSixel does nothing.
func (t *Terminal) SetEnableSixel(bool) {}

// SetAllowHyperlink does nothing.
func (t *Terminal) SetAllowHyperlink(bool) {}

// AddMatch does nothing: the placeholder has no text to match.
func (t *Terminal) AddMatch(string) (int, error) { return 0, nil }

// MatchAt reports no link.
func (t *Terminal) MatchAt(*gdk.Event) (Match, bool) { return Match{}, false }

// CellAt returns 0, 0.
func (t *Terminal) CellAt(float64, float64) (int, int) { return 0, 0 }

// MatchAtCell reports no match.
func (t *Terminal) MatchAtCell(int, int) (Match, bool) { return Match{}, false }

// Feed does nothing.
func (t *Terminal) Feed([]byte) {}

// SetSize does nothing.
func (t *Terminal) SetSize(int, int) {}

// SetBoldIsBright does nothing.
func (t *Terminal) SetBoldIsBright(bool) {}

// SetScrollbackLines does nothing.
func (t *Terminal) SetScrollbackLines(int) {}

// SetAudibleBell does nothing.
func (t *Terminal) SetAudibleBell(bool) {}

// SetMouseAutohide does nothing.
func (t *Terminal) SetMouseAutohide(bool) {}

// SetFontScale does nothing.
func (t *Terminal) SetFontScale(float64) {}

// FontScale returns 1.
func (t *Terminal) FontScale() float64 { return 1 }

// HasSelection reports false.
func (t *Terminal) HasSelection() bool { return false }

// CopyClipboard does nothing.
func (t *Terminal) CopyClipboard() {}

// PasteClipboard does nothing.
func (t *Terminal) PasteClipboard() {}

// Size returns 0, 0.
func (t *Terminal) Size() (int, int) { return 0, 0 }
