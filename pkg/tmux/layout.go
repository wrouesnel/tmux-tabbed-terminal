package tmux

import (
	"context"
	"strconv"
	"strings"

	"github.com/pkg/errors"
)

// Pane is one pane of a window.
type Pane struct {
	ID string
	// Left, Top, Width and Height are where the pane is in its window, in cells.
	Left, Top, Width, Height int
	// Path is the working directory of the pane's program.
	Path string
	// InMode is set when the pane is in a mode, such as copy mode, which shows something
	// other than its screen.
	InMode bool
}

// Contains reports whether a cell of the window is in the pane.
func (p *Pane) Contains(col, row int) bool {
	return col >= p.Left && col < p.Left+p.Width && row >= p.Top && row < p.Top+p.Height
}

// Layout is where a window's panes are on a client's screen.
type Layout struct {
	Panes []Pane
	// Top is how many rows the status line takes above the window.
	Top int
}

// PaneAt returns the pane showing a cell of the client's screen, and the cell's place in
// the pane, or nil over the status line or a border.
func (l *Layout) PaneAt(col, row int) (pane *Pane, paneCol, paneRow int) {
	row -= l.Top
	for i := range l.Panes {
		if p := &l.Panes[i]; p.Contains(col, row) {
			return p, col - p.Left, row - p.Top
		}
	}
	return nil, 0, 0
}

//nolint:gochecknoglobals
var (
	paneFormat = strings.Join([]string{
		"P", "#{pane_id}", "#{pane_left}", "#{pane_top}", "#{pane_width}", "#{pane_height}",
		"#{pane_in_mode}", "#{pane_current_path}",
	}, fieldSep)
	statusFormat = strings.Join([]string{"O", "#{status}", "#{status-position}"}, fieldSep)
)

// WindowLayout returns where a window's panes are.
func (c *Client) WindowLayout(ctx context.Context, window string) (*Layout, error) {
	out, err := c.run(ctx, "list-panes", "-t", window, "-F", paneFormat, ";",
		"display-message", "-p", "-t", window, statusFormat)
	if err != nil {
		return nil, err
	}
	return ParseLayout(out), nil
}

// ParseLayout parses the output of the layout command. Malformed lines are skipped.
//
//nolint:mnd // field counts are fixed by the format strings
func ParseLayout(out string) *Layout {
	l := &Layout{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, fieldSep)
		switch fields[0] {
		case "P":
			if len(fields) < 8 {
				continue
			}
			p := Pane{ID: fields[1], InMode: fields[6] == "1", Path: strings.Join(fields[7:], fieldSep)}
			p.Left, _ = strconv.Atoi(fields[2])
			p.Top, _ = strconv.Atoi(fields[3])
			p.Width, _ = strconv.Atoi(fields[4])
			p.Height, _ = strconv.Atoi(fields[5])
			l.Panes = append(l.Panes, p)
		case "O":
			if len(fields) < 3 || fields[2] != "top" {
				continue
			}
			// The status option is off, on for one line, or a number of lines.
			switch fields[1] {
			case "off":
			case "on":
				l.Top = 1
			default:
				l.Top, _ = strconv.Atoi(fields[1])
			}
		}
	}
	return l
}

// PaneLines returns the lines on a pane's screen, with lines tmux wrapped joined again,
// so a line may take several rows. On a tmux whose capture-pane crashes, it returns
// ErrCaptureCrashes without trying.
func (c *Client) PaneLines(ctx context.Context, pane string) ([]string, error) {
	version, err := c.ServerVersion(ctx)
	if err != nil {
		return nil, err
	}
	if CaptureCrashes(version) {
		return nil, errors.Wrap(ErrCaptureCrashes, "tmux "+version)
	}
	out, err := c.run(ctx, "capture-pane", "-p", "-J", "-t", pane)
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSuffix(out, "\n"), "\n"), nil
}
