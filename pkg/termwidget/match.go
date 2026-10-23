package termwidget

import (
	"fmt"
	"regexp"

	"github.com/gotk3/gotk3/gdk"
)

// Match is a link at a cell: an OSC 8 hyperlink's URI, or the text of an AddMatch
// regex's match and that regex's tag.
type Match struct {
	Text      string
	Tag       int
	Hyperlink bool
}

// matcher finds links on the screen.
type matcher struct {
	regexps []*regexp.Regexp
	// allowHyperlink is whether OSC 8 hyperlinks are followed.
	allowHyperlink bool
	// hoverLink is the hyperlink under the pointer, which is underlined.
	hoverLink uint32
}

// SetAllowHyperlink sets whether the terminal follows OSC 8 hyperlinks. Hovering one
// underlines it.
func (t *Terminal) SetAllowHyperlink(allow bool) { t.matches.allowHyperlink = allow }

// AddMatch adds a regex, in Go's syntax and matched case-insensitively within a line and
// the lines it wrapped onto, whose matches MatchAt reports. It returns the regex's tag.
func (t *Terminal) AddMatch(pattern string) (int, error) {
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return 0, fmt.Errorf("regex %q: %w", pattern, err)
	}
	t.matches.regexps = append(t.matches.regexps, re)
	return len(t.matches.regexps) - 1, nil
}

// MatchAt returns the link under a button or motion event, preferring a hyperlink to a
// regex match.
func (t *Terminal) MatchAt(ev *gdk.Event) (Match, bool) {
	// Button and motion events both start with their type, window, send_event, time, x
	// and y, so either's position can be read as a button event's.
	b := gdk.EventButtonNewFromEvent(ev)
	col, row := t.CellAt(b.X(), b.Y())
	cols, rows := t.emu.Size()
	if col < 0 || row < 0 || col >= cols || row >= rows {
		return Match{}, false
	}
	if t.matches.allowHyperlink {
		if uri := t.emu.Link(t.emu.Line(row).Cells[col].Link); uri != "" {
			return Match{Text: uri, Hyperlink: true}, true
		}
	}
	return t.MatchAtCell(col, row)
}

// HyperlinkText returns the text shown for the hyperlink under a button or motion
// event, as distinct from its URI, which MatchAt returns: the run of cells around it,
// in reading order, that carry the same hyperlink. ok is false when there's none.
func (t *Terminal) HyperlinkText(ev *gdk.Event) (string, bool) {
	b := gdk.EventButtonNewFromEvent(ev)
	if !t.matches.allowHyperlink {
		return "", false
	}
	return t.emu.LinkText(t.CellAt(b.X(), b.Y()))
}

// MatchAtCell returns the AddMatch regex match covering a cell of the screen.
func (t *Terminal) MatchAtCell(col, row int) (Match, bool) {
	cols, rows := t.emu.Size()
	if col < 0 || row < 0 || col >= cols || row >= rows || len(t.matches.regexps) == 0 {
		return Match{}, false
	}
	// The logical line: the rows the cell's row wrapped from and onto.
	first, last := row, row
	for first > 0 && t.emu.Line(first-1).Wrapped {
		first--
	}
	for last < rows-1 && t.emu.Line(last).Wrapped {
		last++
	}
	var text []byte
	target := -1 // the byte offset of the cell's text
	for y := first; y <= last; y++ {
		for x, c := range t.emu.Line(y).Cells {
			if c.Width == 0 {
				continue
			}
			if x == col && y == row {
				target = len(text)
			}
			text = append(text, c.Text()...)
		}
	}
	if target < 0 {
		return Match{}, false
	}
	for tag, re := range t.matches.regexps {
		for _, loc := range re.FindAllIndex(text, -1) {
			if loc[0] <= target && target < loc[1] {
				return Match{Text: string(text[loc[0]:loc[1]]), Tag: tag}, true
			}
		}
	}
	return Match{}, false
}

// hover underlines the hyperlink under the pointer.
func (m *matcher) hover(t *Terminal, x, y float64) {
	var id uint32
	col, row := t.CellAt(x, y)
	if cols, rows := t.emu.Size(); m.allowHyperlink && col >= 0 && row >= 0 && col < cols && row < rows {
		id = t.emu.Line(row).Cells[col].Link
	}
	if id != m.hoverLink {
		m.hoverLink = id
		t.QueueDraw()
	}
}
