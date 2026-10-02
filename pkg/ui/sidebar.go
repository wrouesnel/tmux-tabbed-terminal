package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/gotk3/gotk3/pango"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/activity"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

// Indicator pages of a session row.
const (
	indicatorNone   = "none"
	indicatorBusy   = "busy"
	indicatorUnseen = "unseen"
	indicatorWidth  = 16
	rowSpacing      = 8
	sidebarMinWidth = 120
)

// sessionRow is one session in the sidebar.
type sessionRow struct {
	id        string
	row       *gtk.ListBoxRow
	name      *gtk.Label
	subtitle  *gtk.Label
	indicator *gtk.Stack
	spinner   *gtk.Spinner
}

// sidebar lists the tmux sessions.
type sidebar struct {
	win   *Window
	root  *gtk.Box
	list  *gtk.ListBox
	rows  map[string]*sessionRow
	order []string
}

func newSidebar(w *Window) *sidebar {
	sb := &sidebar{win: w, rows: map[string]*sessionRow{}}

	sb.list, _ = gtk.ListBoxNew()
	sb.list.SetSelectionMode(gtk.SELECTION_SINGLE)
	sb.list.SetActivateOnSingleClick(true)
	addClass(sb.list, "navigation-sidebar")
	addClass(sb.list, "ttt-sidebar")
	sb.list.Connect("row-activated", func(_ interface{}, row *gtk.ListBoxRow) {
		if id := sb.idAt(row.GetIndex()); id != "" {
			w.ShowSession(id)
		}
	})
	sb.list.Connect("button-press-event", func(_ interface{}, ev *gdk.Event) bool {
		return sb.onButtonPress(gdk.EventButtonNewFromEvent(ev))
	})

	placeholder, _ := gtk.LabelNew("No tmux sessions")
	addClass(placeholder, "dim-label")
	placeholder.Show()
	sb.list.SetPlaceholder(placeholder)

	scroller, _ := gtk.ScrolledWindowNew(nil, nil)
	scroller.SetPolicy(gtk.POLICY_NEVER, gtk.POLICY_AUTOMATIC)
	scroller.Add(sb.list)
	scroller.SetVExpand(true)

	newBtn, _ := gtk.ButtonNewFromIconName("list-add-symbolic", gtk.ICON_SIZE_BUTTON)
	newBtn.SetTooltipText("New Session")
	newBtn.SetActionName("win.new-session")
	newBtn.SetRelief(gtk.RELIEF_NONE)
	toolbar, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	addClass(toolbar, "ttt-sidebar-toolbar")
	toolbar.PackStart(newBtn, false, false, 0)

	sb.root, _ = gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	addClass(sb.root, "sidebar")
	sb.root.SetSizeRequest(sidebarMinWidth, -1)
	sb.root.PackStart(scroller, true, true, 0)
	sep, _ := gtk.SeparatorNew(gtk.ORIENTATION_HORIZONTAL)
	sb.root.PackStart(sep, false, false, 0)
	sb.root.PackStart(toolbar, false, false, 0)
	return sb
}

// idAt returns the session ID of the row at index, or "".
func (sb *sidebar) idAt(index int) string {
	if index < 0 || index >= len(sb.order) {
		return ""
	}
	return sb.order[index]
}

func newSessionRow(id string) *sessionRow {
	r := &sessionRow{id: id}
	r.row, _ = gtk.ListBoxRowNew()
	addClass(r.row, "ttt-session")

	r.indicator, _ = gtk.StackNew()
	r.indicator.SetSizeRequest(indicatorWidth, -1)
	r.indicator.SetVAlign(gtk.ALIGN_CENTER)
	none, _ := gtk.LabelNew("")
	r.spinner, _ = gtk.SpinnerNew()
	dot, _ := gtk.LabelNew("●")
	addClass(dot, "ttt-unseen-dot")
	r.indicator.AddNamed(none, indicatorNone)
	r.indicator.AddNamed(r.spinner, indicatorBusy)
	r.indicator.AddNamed(dot, indicatorUnseen)

	r.name, _ = gtk.LabelNew("")
	r.name.SetXAlign(0)
	r.name.SetEllipsize(pango.ELLIPSIZE_END)
	addClass(r.name, "ttt-session-name")
	r.subtitle, _ = gtk.LabelNew("")
	r.subtitle.SetXAlign(0)
	r.subtitle.SetEllipsize(pango.ELLIPSIZE_END)
	addClass(r.subtitle, "ttt-session-subtitle")
	addClass(r.subtitle, "dim-label")

	text, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	text.PackStart(r.name, false, false, 0)
	text.PackStart(r.subtitle, false, false, 0)

	box, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, rowSpacing)
	box.PackStart(r.indicator, false, false, 0)
	box.PackStart(text, true, true, 0)
	r.row.Add(box)
	r.row.ShowAll()
	r.indicator.SetVisibleChildName(indicatorNone)
	return r
}

// update shows a session's state in its row.
func (r *sessionRow) update(s *tmux.Session, state activity.State, shown bool, othersAttached int) {
	r.name.SetText(s.Name)
	r.subtitle.SetText(sessionSubtitle(s, othersAttached))
	r.row.SetTooltipText(sessionTooltip(s))

	switch {
	case state.Active:
		r.indicator.SetVisibleChildName(indicatorBusy)
		r.spinner.Start()
	case state.Unseen:
		r.spinner.Stop()
		r.indicator.SetVisibleChildName(indicatorUnseen)
	default:
		r.spinner.Stop()
		r.indicator.SetVisibleChildName(indicatorNone)
	}
	setClass(r.row, "ttt-unseen", state.Unseen)
	setClass(r.row, "ttt-shown", shown)
}

// sessionSubtitle describes what a session is running. othersAttached counts clients
// other than this application's.
func sessionSubtitle(s *tmux.Session, othersAttached int) string {
	parts := []string{}
	if w := s.ActiveWindow(); w != nil {
		label := w.Name
		if label == "" {
			label = w.Command
		}
		parts = append(parts, label)
	}
	if n := len(s.Windows); n != 1 {
		parts = append(parts, fmt.Sprintf("%d windows", n))
	}
	if othersAttached > 0 {
		parts = append(parts, "attached elsewhere")
	}
	return strings.Join(parts, " · ")
}

// sessionTooltip lists a session's windows.
func sessionTooltip(s *tmux.Session) string {
	lines := []string{s.Name}
	for _, w := range s.Windows {
		marker := " "
		if w.Active {
			marker = "*"
		}
		line := fmt.Sprintf("%s %d: %s", marker, w.Index, w.Name)
		if !w.Activity.IsZero() {
			line += fmt.Sprintf(" (%s ago)", time.Since(w.Activity).Round(time.Second))
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// update makes the list match a snapshot.
func (sb *sidebar) update(snap *tmux.Snapshot, tracker *activity.Tracker, shown map[string]bool, selected string,
	ownTTYs map[string]bool,
) {
	now := time.Now()
	others := map[string]int{}
	for _, c := range snap.Clients {
		if !ownTTYs[c.TTY] {
			others[c.SessionID]++
		}
	}
	order := make([]string, 0, len(snap.Sessions))
	for i := range snap.Sessions {
		s := &snap.Sessions[i]
		order = append(order, s.ID)
		row, ok := sb.rows[s.ID]
		if !ok {
			row = newSessionRow(s.ID)
			sb.rows[s.ID] = row
		}
		row.update(s, tracker.State(s.ID, now), shown[s.ID], others[s.ID])
	}

	if !equalStrings(order, sb.order) {
		// Rebuild in tmux's order. The rows are kept, so only their position changes.
		keep := map[string]bool{}
		for _, id := range order {
			keep[id] = true
		}
		for _, id := range sb.order {
			sb.list.Remove(sb.rows[id].row)
			if !keep[id] {
				delete(sb.rows, id)
			}
		}
		for i, id := range order {
			sb.list.Insert(sb.rows[id].row, i)
		}
		sb.order = order
	}

	sb.selectSession(selected)
}

// selectSession highlights the session in the focused pane.
func (sb *sidebar) selectSession(id string) {
	if row, ok := sb.rows[id]; ok {
		if current := sb.list.GetSelectedRow(); current == nil || current.Native() != row.row.Native() {
			sb.list.SelectRow(row.row)
		}
		return
	}
	sb.list.UnselectAll()
}

// onButtonPress opens a session's menu on right click, and in a new pane on middle click.
func (sb *sidebar) onButtonPress(ev *gdk.EventButton) bool {
	if ev.Type() != gdk.EVENT_BUTTON_PRESS {
		return false
	}
	row := sb.list.GetRowAtY(int(ev.Y()))
	if row == nil {
		return false
	}
	id := sb.idAt(row.GetIndex())
	if id == "" {
		return false
	}

	switch ev.Button() {
	case gdk.BUTTON_MIDDLE:
		sb.win.OpenInSplit(id, gtk.ORIENTATION_HORIZONTAL)
		return true
	case gdk.BUTTON_SECONDARY:
		popover, err := gtk.PopoverNewFromModel(sb.list, sessionMenu(id))
		if err != nil {
			return false
		}
		rect := gdk.Rectangle{}
		rect.SetX(int(ev.X()))
		rect.SetY(int(ev.Y()))
		rect.SetWidth(1)
		rect.SetHeight(1)
		popover.SetPointingTo(rect)
		popover.SetPosition(gtk.POS_BOTTOM)
		popover.Popup()
		return true
	case gdk.BUTTON_PRIMARY:
		if eventHasControl(ev) {
			sb.win.OpenInSplit(id, gtk.ORIENTATION_HORIZONTAL)
			return true
		}
	}
	return false
}

// sessionMenu is the context menu of a session row. Its actions take the session ID.
func sessionMenu(id string) *glib.MenuModel {
	target := glib.VariantFromString(id)
	item := func(label, action string) *glib.MenuItem {
		mi := glib.MenuItemNewWithLabel(label)
		mi.SetActionAndTargetValue(action, target)
		return mi
	}
	open := glib.MenuNew()
	open.AppendItem(item("Open", "win.session-open"))
	open.AppendItem(item("Open in Split Right", "win.session-split-right"))
	open.AppendItem(item("Open in Split Down", "win.session-split-down"))
	manage := glib.MenuNew()
	manage.AppendItem(item("Rename…", "win.session-rename"))
	manage.AppendItem(item("Kill Session…", "win.session-kill"))
	menu := glib.MenuNew()
	menu.AppendSectionWithoutLabel(&open.MenuModel)
	menu.AppendSectionWithoutLabel(&manage.MenuModel)
	return &menu.MenuModel
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
