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
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/sessionlist"
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
	// dot is the activity indicator. Busy dots pulse; unseen ones are still.
	dot = "●"
)

// sessionRow is one session in the sidebar.
type sessionRow struct {
	id        string
	row       *gtk.ListBoxRow
	name      *gtk.Label
	subtitle  *gtk.Label
	indicator *gtk.Stack
}

// sidebar lists the tmux sessions, optionally grouped by application, with a search
// entry to filter them.
type sidebar struct {
	win    *Window
	root   *gtk.Box
	search *gtk.SearchEntry
	list   *gtk.ListBox
	rows   map[string]*sessionRow
	// rowIDs maps a row's native pointer to its session ID.
	rowIDs map[uintptr]string
	order  []sessionlist.Entry
	query  string
}

func newSidebar(w *Window) *sidebar {
	sb := &sidebar{win: w, rows: map[string]*sessionRow{}, rowIDs: map[uintptr]string{}}

	sb.search, _ = gtk.SearchEntryNew()
	sb.search.SetPlaceholderText("Search sessions")
	sb.search.SetTooltipText("Search sessions (Ctrl+Shift+F)")
	addClass(sb.search, "ttt-search")
	sb.search.Connect("search-changed", func() {
		sb.query, _ = sb.search.GetText()
		sb.list.InvalidateFilter()
		sb.list.InvalidateHeaders()
	})
	// Enter opens the first match. Escape clears the search and returns to the terminal.
	sb.search.Connect("activate", func() {
		if ids := sb.visibleIDs(); len(ids) > 0 {
			w.ShowSession(ids[0])
		}
	})
	sb.search.Connect("stop-search", func() {
		sb.search.SetText("")
		w.activePane.Focus()
	})
	sb.search.Connect("key-press-event", func(_ interface{}, ev *gdk.Event) bool {
		if gdk.EventKeyNewFromEvent(ev).KeyVal() == gdk.KEY_Down {
			sb.focusFirstRow()
			return true
		}
		return false
	})

	sb.list, _ = gtk.ListBoxNew()
	sb.list.SetSelectionMode(gtk.SELECTION_SINGLE)
	sb.list.SetActivateOnSingleClick(true)
	addClass(sb.list, "ttt-sidebar")
	sb.list.SetFilterFunc(sb.filter)
	sb.list.SetHeaderFunc(sb.header)
	sb.list.Connect("row-activated", func(_ interface{}, row *gtk.ListBoxRow) {
		if id := sb.rowIDs[row.Native()]; id != "" {
			w.ShowSession(id)
		}
	})
	sb.list.Connect("button-press-event", func(_ interface{}, ev *gdk.Event) bool {
		return sb.onButtonPress(gdk.EventButtonNewFromEvent(ev))
	})

	placeholder, _ := gtk.LabelNew("No sessions")
	addClass(placeholder, "dim-label")
	placeholder.Show()
	sb.list.SetPlaceholder(placeholder)

	scroller, _ := gtk.ScrolledWindowNew(nil, nil)
	scroller.SetPolicy(gtk.POLICY_NEVER, gtk.POLICY_AUTOMATIC)
	scroller.Add(sb.list)
	scroller.SetVExpand(true)

	newBtn, _ := gtk.ButtonNewFromIconName("list-add-symbolic", gtk.ICON_SIZE_BUTTON)
	newBtn.SetTooltipText("New Session (Ctrl+Shift+T)")
	newBtn.SetActionName("win.new-session")
	newBtn.SetRelief(gtk.RELIEF_NONE)
	groupBtn, _ := gtk.ToggleButtonNew()
	groupIcon, _ := gtk.ImageNewFromIconName(firstIcon("view-list-bullet-symbolic", "view-list-symbolic"),
		gtk.ICON_SIZE_BUTTON)
	groupBtn.SetImage(groupIcon)
	groupBtn.SetTooltipText("Group by Application")
	groupBtn.SetActionName("win.group-sessions")
	groupBtn.SetRelief(gtk.RELIEF_NONE)
	toolbar, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	addClass(toolbar, "ttt-sidebar-toolbar")
	toolbar.PackStart(newBtn, false, false, 0)
	toolbar.PackEnd(groupBtn, false, false, 0)

	sb.root, _ = gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	addClass(sb.root, "sidebar")
	addClass(sb.root, "ttt-nav")
	sb.root.SetSizeRequest(sidebarMinWidth, -1)
	sb.root.PackStart(sb.search, false, false, 0)
	sb.root.PackStart(scroller, true, true, 0)
	sep, _ := gtk.SeparatorNew(gtk.ORIENTATION_HORIZONTAL)
	sb.root.PackStart(sep, false, false, 0)
	sb.root.PackStart(toolbar, false, false, 0)
	return sb
}

// entry returns the display entry of a session, or nil.
func (sb *sidebar) entry(id string) *sessionlist.Entry {
	for i := range sb.order {
		if sb.order[i].ID == id {
			return &sb.order[i]
		}
	}
	return nil
}

// matches reports whether a session matches the search.
func (sb *sidebar) matches(id string) bool {
	s := sb.win.app.snapshot.Session(id)
	if s == nil {
		return false
	}
	return sessionlist.Matches(s, sb.win.app.groups[id], sb.query)
}

// filter is the list's filter function.
func (sb *sidebar) filter(row *gtk.ListBoxRow) bool {
	return sb.matches(sb.rowIDs[row.Native()])
}

// header puts a group heading above the first visible row of each group.
func (sb *sidebar) header(row *gtk.ListBoxRow, before *gtk.ListBoxRow) {
	e := sb.entry(sb.rowIDs[row.Native()])
	if e == nil || e.Group == "" {
		row.SetHeader(nil)
		return
	}
	if before != nil && before.Object != nil {
		if prev := sb.entry(sb.rowIDs[before.Native()]); prev != nil && prev.Group == e.Group {
			row.SetHeader(nil)
			return
		}
	}
	label, _ := gtk.LabelNew(e.Group)
	label.SetXAlign(0)
	label.SetEllipsize(pango.ELLIPSIZE_END)
	addClass(label, "ttt-group-header")
	addClass(label, "dim-label")
	label.Show()
	row.SetHeader(label)
}

// visibleIDs returns the sessions shown in the list, in order.
func (sb *sidebar) visibleIDs() []string {
	ids := []string{}
	for _, e := range sb.order {
		if sb.matches(e.ID) {
			ids = append(ids, e.ID)
		}
	}
	return ids
}

// focusFirstRow moves keyboard focus to the first visible session.
func (sb *sidebar) focusFirstRow() {
	if ids := sb.visibleIDs(); len(ids) > 0 {
		sb.rows[ids[0]].row.GrabFocus()
	}
}

// FocusSearch shows the sidebar if needed and puts the cursor in the search entry.
func (sb *sidebar) FocusSearch() {
	sb.search.GrabFocus()
}

func newSessionRow(id string) *sessionRow {
	r := &sessionRow{id: id}
	r.row, _ = gtk.ListBoxRowNew()
	addClass(r.row, "ttt-session")

	r.indicator, _ = gtk.StackNew()
	r.indicator.SetSizeRequest(indicatorWidth, -1)
	r.indicator.SetVAlign(gtk.ALIGN_CENTER)
	none, _ := gtk.LabelNew("")
	busy, _ := gtk.LabelNew(dot)
	addClass(busy, "ttt-busy-dot")
	unseen, _ := gtk.LabelNew(dot)
	addClass(unseen, "ttt-unseen-dot")
	r.indicator.AddNamed(none, indicatorNone)
	r.indicator.AddNamed(busy, indicatorBusy)
	r.indicator.AddNamed(unseen, indicatorUnseen)

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
	case state.Unseen:
		r.indicator.SetVisibleChildName(indicatorUnseen)
	default:
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
	for i := range snap.Sessions {
		s := &snap.Sessions[i]
		row, ok := sb.rows[s.ID]
		if !ok {
			row = newSessionRow(s.ID)
			sb.rows[s.ID] = row
			sb.rowIDs[row.row.Native()] = s.ID
		}
		row.update(s, tracker.State(s.ID, now), shown[s.ID], others[s.ID])
	}

	order := sessionlist.Order(snap.Sessions, sb.win.app.groups, sb.win.app.grouped)
	if !equalEntries(order, sb.order) {
		// Rebuild in display order. The rows are kept, so only their position changes.
		keep := map[string]bool{}
		for _, e := range order {
			keep[e.ID] = true
		}
		for _, e := range sb.order {
			row := sb.rows[e.ID]
			sb.list.Remove(row.row)
			if !keep[e.ID] {
				delete(sb.rowIDs, row.row.Native())
				delete(sb.rows, e.ID)
			}
		}
		sb.order = order
		for i, e := range order {
			sb.list.Insert(sb.rows[e.ID].row, i)
		}
		sb.list.InvalidateHeaders()
	}
	// Session names and commands may have changed what the search matches.
	if sb.query != "" {
		sb.list.InvalidateFilter()
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
	id := sb.rowIDs[row.Native()]
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

func equalEntries(a, b []sessionlist.Entry) bool {
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
