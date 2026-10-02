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
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/gtkx"
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
	key       string
	row       *gtk.ListBoxRow
	name      *gtk.Label
	subtitle  *gtk.Label
	indicator *gtk.Stack
}

// sidebar lists the tmux sessions, optionally grouped by application, with a search
// entry to filter them.
type sidebar struct {
	win      *Window
	root     *gtk.Box
	search   *gtk.SearchEntry
	list     *gtk.ListBox
	rows     map[string]*sessionRow
	hostRows map[string]*hostRow
	// rowKeys maps a row's native pointer to its entry key.
	rowKeys map[uintptr]string
	order   []listEntry
	query   string
	// pressKey is the entry under the last button press, which a drag starts from.
	pressKey string
}

// listEntry is one row of the list: a host, or a session of the host above it.
type listEntry struct {
	// key is the session key, or the host name for a host row.
	key    string
	host   string
	group  string
	isHost bool
}

// hostRow heads a host's sessions. It isn't selectable: its menu acts on the host.
type hostRow struct {
	name   string
	row    *gtk.ListBoxRow
	status *gtk.Label
	icon   *gtk.Image
}

func newSidebar(w *Window) *sidebar {
	sb := &sidebar{
		win: w, rows: map[string]*sessionRow{}, hostRows: map[string]*hostRow{},
		rowKeys: map[uintptr]string{},
	}

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
		if e := sb.entry(sb.rowKeys[row.Native()]); e != nil && !e.isHost {
			w.ShowSession(e.key)
		}
	})
	sb.list.Connect("button-press-event", func(_ interface{}, ev *gdk.Event) bool {
		return sb.onButtonPress(gdk.EventButtonNewFromEvent(ev))
	})
	// Sessions can be dragged onto a pane to open them there or in a new split.
	sb.list.DragSourceSet(gdk.BUTTON1_MASK, dragTargets(), dropAction)
	sb.list.Connect("drag-begin", func(_ interface{}, ctx interface{}) {
		if e := sb.entry(sb.pressKey); e != nil && !e.isHost {
			w.app.dragKey = e.key
		}
		gtkx.DragSetIconName(ctx, "utilities-terminal-symbolic")
	})
	sb.list.Connect("drag-end", func() { w.app.endDrag() })

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
	newBtn.Connect("clicked", func() { w.PromptNewSession(w.activePane, newBtn) })
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
	hostBtn, _ := gtk.ButtonNewWithLabel("Add Host")
	hostIcon, _ := gtk.ImageNewFromIconName(firstIcon("network-server-symbolic", "computer-symbolic"),
		gtk.ICON_SIZE_BUTTON)
	hostBtn.SetImage(hostIcon)
	hostBtn.SetAlwaysShowImage(true)
	hostBtn.SetTooltipText("List the tmux sessions of another host, over ssh")
	hostBtn.SetActionName("win.add-host")
	hostBtn.SetRelief(gtk.RELIEF_NONE)
	sideBtn, _ := gtk.ButtonNewFromIconName(firstIcon("object-flip-horizontal-symbolic", "view-dual-symbolic"),
		gtk.ICON_SIZE_BUTTON)
	sideBtn.SetTooltipText("Move List to Other Side")
	sideBtn.SetActionName("win.sidebar-other-side")
	sideBtn.SetRelief(gtk.RELIEF_NONE)
	toolbar.PackStart(newBtn, false, false, 0)
	toolbar.PackStart(hostBtn, false, false, 0)
	toolbar.PackEnd(groupBtn, false, false, 0)
	toolbar.PackEnd(sideBtn, false, false, 0)

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

// entry returns the list entry with a key, or nil.
func (sb *sidebar) entry(key string) *listEntry {
	for i := range sb.order {
		if sb.order[i].key == key {
			return &sb.order[i]
		}
	}
	return nil
}

// matches reports whether a session matches the search.
func (sb *sidebar) matches(key string) bool {
	h, s := sb.win.app.lookup(key)
	if s == nil {
		return false
	}
	return sessionlist.Matches(s, sb.query, sb.win.app.groups[key], h.Name)
}

// hostMatches reports whether a host row is shown: always without a search, otherwise
// when any of its sessions match.
func (sb *sidebar) hostMatches(name string) bool {
	if strings.TrimSpace(sb.query) == "" {
		return true
	}
	for _, e := range sb.order {
		if !e.isHost && e.host == name && sb.matches(e.key) {
			return true
		}
	}
	return false
}

// filter is the list's filter function.
func (sb *sidebar) filter(row *gtk.ListBoxRow) bool {
	e := sb.entry(sb.rowKeys[row.Native()])
	switch {
	case e == nil:
		return false
	case e.isHost:
		return sb.hostMatches(e.host)
	default:
		return sb.matches(e.key)
	}
}

// header puts an application heading above the first visible session of each group.
func (sb *sidebar) header(row *gtk.ListBoxRow, before *gtk.ListBoxRow) {
	e := sb.entry(sb.rowKeys[row.Native()])
	if e == nil || e.isHost || e.group == "" {
		row.SetHeader(nil)
		return
	}
	if before != nil && before.Object != nil {
		prev := sb.entry(sb.rowKeys[before.Native()])
		if prev != nil && !prev.isHost && prev.host == e.host && prev.group == e.group {
			row.SetHeader(nil)
			return
		}
	}
	label, _ := gtk.LabelNew(e.group)
	label.SetXAlign(0)
	label.SetEllipsize(pango.ELLIPSIZE_END)
	addClass(label, "ttt-group-header")
	addClass(label, "dim-label")
	label.Show()
	row.SetHeader(label)
}

// visibleIDs returns the keys of the sessions shown in the list, in order.
func (sb *sidebar) visibleIDs() []string {
	keys := []string{}
	for _, e := range sb.order {
		if !e.isHost && sb.matches(e.key) {
			keys = append(keys, e.key)
		}
	}
	return keys
}

// focusFirstRow moves keyboard focus to the first visible session.
func (sb *sidebar) focusFirstRow() {
	if ids := sb.visibleIDs(); len(ids) > 0 {
		sb.rows[ids[0]].row.GrabFocus()
	}
}

// FocusSearch puts the cursor in the search entry.
func (sb *sidebar) FocusSearch() {
	sb.search.GrabFocus()
}

func newHostRow(name string, local bool, newSession func(string)) *hostRow {
	r := &hostRow{name: name}
	r.row, _ = gtk.ListBoxRowNew()
	r.row.SetSelectable(false)
	r.row.SetActivatable(false)
	addClass(r.row, "ttt-host")

	iconName := firstIcon("network-server-symbolic", "computer-symbolic")
	if local {
		iconName = firstIcon("computer-symbolic", "user-home-symbolic")
	}
	r.icon, _ = gtk.ImageNewFromIconName(iconName, gtk.ICON_SIZE_MENU)
	label, _ := gtk.LabelNew(name)
	label.SetXAlign(0)
	label.SetEllipsize(pango.ELLIPSIZE_MIDDLE)
	addClass(label, "ttt-host-name")
	r.status, _ = gtk.LabelNew("")
	addClass(r.status, "dim-label")
	addClass(r.status, "ttt-host-status")

	menuBtn, _ := gtk.MenuButtonNew()
	menuIcon, _ := gtk.ImageNewFromIconName("view-more-symbolic", gtk.ICON_SIZE_MENU)
	menuBtn.SetImage(menuIcon)
	menuBtn.SetRelief(gtk.RELIEF_NONE)
	menuBtn.SetTooltipText("Host Menu")
	menuBtn.SetMenuModel(hostMenu(name, local))

	box, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, rowSpacing)
	box.PackStart(r.icon, false, false, 0)
	box.PackStart(label, true, true, 0)
	addBtn, _ := gtk.ButtonNewFromIconName("list-add-symbolic", gtk.ICON_SIZE_MENU)
	addBtn.SetRelief(gtk.RELIEF_NONE)
	addBtn.SetTooltipText("New Session on " + name)
	addBtn.Connect("clicked", func() { newSession(name) })

	box.PackStart(r.status, false, false, 0)
	box.PackStart(addBtn, false, false, 0)
	box.PackStart(menuBtn, false, false, 0)
	r.row.Add(box)
	r.row.ShowAll()
	return r
}

// update shows a host's connection state.
func (r *hostRow) update(h *Host) {
	switch {
	case h.err != nil:
		r.status.SetText("unreachable")
		r.row.SetTooltipText(h.err.Error())
	case !h.polled:
		r.status.SetText("connecting…")
		r.row.SetTooltipText("")
	default:
		n := len(h.snapshot.Sessions)
		r.status.SetText(fmt.Sprintf("%d", n))
		r.row.SetTooltipText(fmt.Sprintf("%d sessions", n))
	}
	setClass(r.row, "ttt-host-error", h.err != nil)
}

// hostMenu is the menu of a host row. Its actions take the host name.
func hostMenu(name string, local bool) *glib.MenuModel {
	target := glib.VariantFromString(name)
	newSession := glib.MenuItemNewWithLabel("New Session")
	newSession.SetActionAndTargetValue("win.host-new-session", target)
	menu := glib.MenuNew()
	menu.AppendItem(newSession)
	if !local {
		remove := glib.MenuItemNewWithLabel("Remove Host")
		remove.SetActionAndTargetValue("win.host-remove", target)
		menu.AppendItem(remove)
	}
	return &menu.MenuModel
}

func newSessionRow(key string) *sessionRow {
	r := &sessionRow{key: key}
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

// update makes the list match the hosts' current state.
func (sb *sidebar) update(shown map[string]bool, selected string) {
	app := sb.win.app
	now := time.Now()
	ownTTYs, ownClients := app.ownTTYs(), app.ownClients()

	order := []listEntry{}
	for _, h := range app.hosts {
		hr, ok := sb.hostRows[h.Name]
		if !ok {
			hr = newHostRow(h.Name, h.Local(), func(name string) { sb.win.NewSessionOn(sb.win.activePane, name) })
			sb.hostRows[h.Name] = hr
			sb.rowKeys[hr.row.Native()] = h.Name
		}
		hr.update(h)
		order = append(order, listEntry{key: h.Name, host: h.Name, isHost: true})

		// Clients attached from elsewhere: by tty locally, by count remotely, where the
		// ttys are on the other host.
		others := map[string]int{}
		if h.Local() {
			for _, c := range h.snapshot.Clients {
				if !ownTTYs[c.TTY] {
					others[c.SessionID]++
				}
			}
		}
		groups := map[string]string{}
		for i := range h.snapshot.Sessions {
			s := &h.snapshot.Sessions[i]
			key := sessionKey(h.Name, s.ID)
			groups[s.ID] = app.groups[key]
			if !h.Local() {
				others[s.ID] = max(s.Attached-ownClients[key], 0)
			}
			row, ok := sb.rows[key]
			if !ok {
				row = newSessionRow(key)
				sb.rows[key] = row
				sb.rowKeys[row.row.Native()] = key
			}
			row.update(s, app.tracker.State(key, now), shown[key], others[s.ID])
		}
		for _, e := range sessionlist.Order(h.snapshot.Sessions, groups, app.grouped) {
			order = append(order, listEntry{key: sessionKey(h.Name, e.ID), host: h.Name, group: e.Group})
		}
	}

	if !equalEntries(order, sb.order) {
		// Rebuild in display order. The rows are kept, so only their position changes.
		keep := map[string]bool{}
		for _, e := range order {
			keep[e.key] = true
		}
		for _, e := range sb.order {
			row := sb.rowOf(e)
			sb.list.Remove(row)
			if !keep[e.key] {
				delete(sb.rowKeys, row.Native())
				if e.isHost {
					delete(sb.hostRows, e.key)
				} else {
					delete(sb.rows, e.key)
				}
			}
		}
		sb.order = order
		for i, e := range order {
			sb.list.Insert(sb.rowOf(e), i)
		}
		sb.list.InvalidateHeaders()
	}
	// Session names and commands may have changed what the search matches.
	if sb.query != "" {
		sb.list.InvalidateFilter()
	}

	sb.selectSession(selected)
}

// rowOf returns the row widget of an entry.
func (sb *sidebar) rowOf(e listEntry) *gtk.ListBoxRow {
	if e.isHost {
		return sb.hostRows[e.key].row
	}
	return sb.rows[e.key].row
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
		sb.pressKey = ""
		return false
	}
	sb.pressKey = sb.rowKeys[row.Native()]
	e := sb.entry(sb.pressKey)
	if e == nil {
		return false
	}
	if e.isHost {
		if ev.Button() == gdk.BUTTON_SECONDARY {
			return sb.popup(hostMenu(e.host, e.host == LocalHost), ev)
		}
		return false
	}
	id := e.key

	switch ev.Button() {
	case gdk.BUTTON_MIDDLE:
		sb.win.OpenInSplit(id, gtk.ORIENTATION_HORIZONTAL)
		return true
	case gdk.BUTTON_SECONDARY:
		return sb.popup(sessionMenu(id), ev)
	case gdk.BUTTON_PRIMARY:
		if eventHasControl(ev) {
			sb.win.OpenInSplit(id, gtk.ORIENTATION_HORIZONTAL)
			return true
		}
	}
	return false
}

// popup shows a menu at the pointer.
func (sb *sidebar) popup(model *glib.MenuModel, ev *gdk.EventButton) bool {
	popover, err := gtk.PopoverNewFromModel(sb.list, model)
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

func equalEntries(a, b []listEntry) bool {
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
