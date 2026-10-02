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
	pinRows  map[string]*sessionRow
	pinHead  *pinHeaderRow
	// rowKeys maps a row's native pointer to its entry key.
	rowKeys map[uintptr]string
	order   []listEntry
	query   string
	// pressKey is the entry under the last button press, which a drag starts from.
	pressKey string
	// hostFilter limits the list to one host's sessions, or is "" for all hosts.
	hostFilter string
	// settingFilter is set while the filter buttons are synced, to ignore their toggles.
	settingFilter bool
}

// listEntry is one row of the list: a host, or a session of the host above it.
type listEntry struct {
	// key is the session key, or the host name for a host row.
	key    string
	host   string
	group  string
	isHost bool
	// pin is a pinned session's row; target is the key of its running session, or "" if
	// it isn't running.
	pin    bool
	target string
	// pinHeader is the heading of the pinned sessions.
	pinHeader bool
}

// isSession reports whether an entry is a session in its host's list.
func (e *listEntry) isSession() bool { return !e.isHost && !e.pin && !e.pinHeader }

// Keys of the pinned section's rows. They start with a byte host names can't.
const (
	pinHeaderKey = "\x1epinned"
	pinKeyPrefix = "\x1epin" + keySep
)

// pinKey returns the list key of a pin.
func pinKey(pin PinnedSession) string { return pinKeyPrefix + pin.Host + keySep + pin.Name }

// parsePinKey returns the pin of a pin key.
func parsePinKey(key string) (PinnedSession, bool) {
	rest, ok := strings.CutPrefix(key, pinKeyPrefix)
	if !ok {
		return PinnedSession{}, false
	}
	host, name, ok := strings.Cut(rest, keySep)
	return PinnedSession{Host: host, Name: name}, ok
}

// pinHeaderRow heads the pinned sessions. Clicking it folds them away.
type pinHeaderRow struct {
	row    *gtk.ListBoxRow
	arrow  *gtk.Image
	hide   *gtk.ToggleButton
	count  *gtk.Label
	update func()
}

// hostRow heads a host's sessions. It isn't selectable: its menu acts on the host.
type hostRow struct {
	name   string
	row    *gtk.ListBoxRow
	status *gtk.Label
	icon   *gtk.Image
	// filter shows only this host's sessions while it's active.
	filter *gtk.ToggleButton
}

func newSidebar(w *Window) *sidebar {
	sb := &sidebar{
		win: w, rows: map[string]*sessionRow{}, hostRows: map[string]*hostRow{}, pinRows: map[string]*sessionRow{},
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
		e := sb.entry(sb.rowKeys[row.Native()])
		switch {
		case e == nil || e.isHost:
		case e.pinHeader:
			w.app.setPinsCollapsed(!w.app.state.PinsCollapsed)
		case e.pin && e.target != "":
			w.ShowSession(e.target)
		case e.pin:
			// The pinned session isn't running: start it again under its name.
			if pin, ok := parsePinKey(e.key); ok && w.app.host(pin.Host) != nil {
				w.NewNamedSession(w.activePane, pin.Host, pin.Name)
			}
		default:
			w.ShowSession(e.key)
		}
	})
	sb.list.Connect("button-press-event", func(_ interface{}, ev *gdk.Event) bool {
		return sb.onButtonPress(gdk.EventButtonNewFromEvent(ev))
	})
	// Sessions can be dragged onto a pane to open them there or in a new split.
	sb.list.DragSourceSet(gdk.BUTTON1_MASK, dragTargets(), dropAction)
	sb.list.Connect("drag-begin", func(_ interface{}, ctx interface{}) {
		if e := sb.entry(sb.pressKey); e != nil && e.isSession() {
			w.app.dragKey = e.key
		} else if e != nil && e.pin {
			w.app.dragKey = e.target
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
	if s == nil || (sb.hostFilter != "" && h.Name != sb.hostFilter) {
		return false
	}
	return sessionlist.Matches(s, sb.query, sb.win.app.groups[key], h.Name)
}

// hostMatches reports whether a host row is shown: always without a search, otherwise
// when any of its sessions match.
func (sb *sidebar) hostMatches(name string) bool {
	if sb.hostFilter != "" && name != sb.hostFilter {
		return false
	}
	if strings.TrimSpace(sb.query) == "" {
		return true
	}
	for _, e := range sb.order {
		if e.isSession() && e.host == name && sb.matches(e.key) {
			return true
		}
	}
	return false
}

// pinMatches reports whether a pinned session is shown, by the search, host filter and
// whether unavailable pins are hidden.
func (sb *sidebar) pinMatches(e *listEntry) bool {
	if e.target != "" {
		return sb.matches(e.target)
	}
	pin, _ := parsePinKey(e.key)
	if sb.win.app.state.HideUnavailablePins || (sb.hostFilter != "" && pin.Host != sb.hostFilter) {
		return false
	}
	for _, word := range strings.Fields(strings.ToLower(sb.query)) {
		if !strings.Contains(strings.ToLower(pin.Name+" "+pin.Host), word) {
			return false
		}
	}
	return true
}

// filter is the list's filter function.
func (sb *sidebar) filter(row *gtk.ListBoxRow) bool {
	e := sb.entry(sb.rowKeys[row.Native()])
	switch {
	case e == nil:
		return false
	case e.isHost:
		return sb.hostMatches(e.host)
	case e.pinHeader:
		return true
	case e.pin:
		return sb.pinMatches(e)
	default:
		return sb.matches(e.key)
	}
}

// header puts an application heading above the first visible session of each group.
func (sb *sidebar) header(row *gtk.ListBoxRow, before *gtk.ListBoxRow) {
	e := sb.entry(sb.rowKeys[row.Native()])
	if e != nil && e.isHost {
		// A line separates each host from the one above it.
		if before != nil && before.Object != nil {
			sep, _ := gtk.SeparatorNew(gtk.ORIENTATION_HORIZONTAL)
			addClass(sep, "ttt-host-separator")
			sep.Show()
			row.SetHeader(sep)
		} else {
			row.SetHeader(nil)
		}
		return
	}
	if e == nil || e.group == "" {
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
		if e.isSession() && sb.matches(e.key) {
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
	r.filter, _ = gtk.ToggleButtonNew()
	filterIcon, _ := gtk.ImageNewFromIconName(iconFilter, gtk.ICON_SIZE_MENU)
	r.filter.SetImage(filterIcon)
	r.filter.SetRelief(gtk.RELIEF_NONE)
	r.filter.SetTooltipText("Show Only " + name)

	addBtn, _ := gtk.ButtonNewFromIconName("list-add-symbolic", gtk.ICON_SIZE_MENU)
	addBtn.SetRelief(gtk.RELIEF_NONE)
	addBtn.SetTooltipText("New Session on " + name)
	addBtn.Connect("clicked", func() { newSession(name) })

	box.PackStart(r.status, false, false, 0)
	box.PackStart(r.filter, false, false, 0)
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

// showUnavailable shows a pinned session which isn't running.
func (r *sessionRow) showUnavailable(pin PinnedSession) {
	r.name.SetText(pin.Name)
	r.subtitle.SetText(pin.Host + " · not running")
	r.row.SetTooltipText(fmt.Sprintf("%s isn't running on %s. Click to start it.", pin.Name, pin.Host))
	r.indicator.SetVisibleChildName(indicatorNone)
	setClass(r.row, "ttt-unseen", false)
	setClass(r.row, "ttt-shown", false)
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

	order := sb.pinEntries(now, shown)
	for _, h := range app.hosts {
		hr, ok := sb.hostRows[h.Name]
		if !ok {
			hr = newHostRow(h.Name, h.Local(), func(name string) { sb.win.NewSessionOn(sb.win.activePane, name) })
			name := h.Name
			hr.filter.Connect("toggled", func() {
				if sb.settingFilter {
					return
				}
				if hr.filter.GetActive() {
					sb.setHostFilter(name)
				} else {
					sb.setHostFilter("")
				}
			})
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
			if e.isHost && !keep[e.key] && sb.hostFilter == e.key {
				// The filtered host was removed: show every host again.
				defer sb.setHostFilter("")
			}
			if !keep[e.key] && !e.pinHeader {
				delete(sb.rowKeys, row.Native())
				switch {
				case e.isHost:
					delete(sb.hostRows, e.key)
				case e.pin:
					delete(sb.pinRows, e.key)
				default:
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
	// Names, commands, pins and filter settings may have changed what's shown.
	sb.list.InvalidateFilter()

	sb.selectSession(selected)
}

// setHostFilter shows only one host's sessions, or every host's for "".
func (sb *sidebar) setHostFilter(name string) {
	sb.hostFilter = name
	sb.settingFilter = true
	for hostName, hr := range sb.hostRows {
		hr.filter.SetActive(hostName == name)
	}
	sb.settingFilter = false
	if name == "" {
		sb.search.SetPlaceholderText("Search sessions")
	} else {
		sb.search.SetPlaceholderText("Search " + name)
	}
	sb.list.InvalidateFilter()
	sb.list.InvalidateHeaders()
}

// rowOf returns the row widget of an entry.
func (sb *sidebar) rowOf(e listEntry) *gtk.ListBoxRow {
	switch {
	case e.isHost:
		return sb.hostRows[e.key].row
	case e.pinHeader:
		return sb.pinHead.row
	case e.pin:
		return sb.pinRows[e.key].row
	default:
		return sb.rows[e.key].row
	}
}

// pinEntries makes the rows of the pinned section, updating them from the hosts' state,
// and returns their entries: the heading, and the pins unless they're folded away.
func (sb *sidebar) pinEntries(now time.Time, shown map[string]bool) []listEntry {
	app := sb.win.app
	if len(app.state.Pinned) == 0 {
		return nil
	}
	if sb.pinHead == nil {
		sb.pinHead = newPinHeaderRow(app)
		sb.rowKeys[sb.pinHead.row.Native()] = pinHeaderKey
	}
	sb.pinHead.update()
	entries := []listEntry{{key: pinHeaderKey, pinHeader: true}}
	if app.state.PinsCollapsed {
		return entries
	}
	multipleHosts := len(app.hosts) > 1
	for _, pin := range app.state.Pinned {
		key := pinKey(pin)
		row, ok := sb.pinRows[key]
		if !ok {
			row = newSessionRow(key)
			addClass(row.row, "ttt-pin")
			sb.pinRows[key] = row
			sb.rowKeys[row.row.Native()] = key
		}
		e := listEntry{key: key, host: pin.Host, pin: true}
		if s := app.findSession(pin.Host, pin.Name); s != nil {
			e.target = sessionKey(pin.Host, s.ID)
			row.update(s, app.tracker.State(e.target, now), shown[e.target], 0)
			if multipleHosts {
				row.subtitle.SetText(pin.Host + " · " + row.subtitle.GetLabel())
			}
		} else {
			row.showUnavailable(pin)
		}
		setClass(row.row, "ttt-unavailable", e.target == "")
		entries = append(entries, e)
	}
	return entries
}

// newPinHeaderRow builds the heading of the pinned sessions.
func newPinHeaderRow(app *App) *pinHeaderRow {
	h := &pinHeaderRow{}
	h.row, _ = gtk.ListBoxRowNew()
	h.row.SetSelectable(false)
	addClass(h.row, "ttt-host")
	addClass(h.row, "ttt-pin-header")

	h.arrow, _ = gtk.ImageNewFromIconName("pan-down-symbolic", gtk.ICON_SIZE_MENU)
	label, _ := gtk.LabelNew("Pinned")
	label.SetXAlign(0)
	addClass(label, "ttt-host-name")
	h.count, _ = gtk.LabelNew("")
	addClass(h.count, "dim-label")
	addClass(h.count, "ttt-host-status")

	h.hide, _ = gtk.ToggleButtonNew()
	hideIcon, _ := gtk.ImageNewFromIconName(firstIcon("view-conceal-symbolic", "edit-clear-symbolic"),
		gtk.ICON_SIZE_MENU)
	h.hide.SetImage(hideIcon)
	h.hide.SetRelief(gtk.RELIEF_NONE)
	h.hide.SetTooltipText("Hide Pinned Sessions Which Aren't Running")
	settingHide := false
	h.hide.Connect("toggled", func() {
		if !settingHide {
			app.setHideUnavailablePins(h.hide.GetActive())
		}
	})

	box, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, rowSpacing)
	box.PackStart(h.arrow, false, false, 0)
	box.PackStart(label, true, true, 0)
	box.PackStart(h.count, false, false, 0)
	box.PackStart(h.hide, false, false, 0)
	h.row.Add(box)
	h.row.ShowAll()

	h.update = func() {
		icon := "pan-down-symbolic"
		if app.state.PinsCollapsed {
			icon = "pan-end-symbolic"
		}
		h.arrow.SetFromIconName(icon, gtk.ICON_SIZE_MENU)
		running := 0
		for _, pin := range app.state.Pinned {
			if app.findSession(pin.Host, pin.Name) != nil {
				running++
			}
		}
		h.count.SetText(fmt.Sprintf("%d/%d", running, len(app.state.Pinned)))
		h.row.SetTooltipText(fmt.Sprintf("%d of %d pinned sessions running", running, len(app.state.Pinned)))
		settingHide = true
		h.hide.SetActive(app.state.HideUnavailablePins)
		settingHide = false
	}
	return h
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
	switch {
	case e.isHost:
		if ev.Button() == gdk.BUTTON_SECONDARY {
			return sb.popup(hostMenu(e.host, e.host == LocalHost), ev)
		}
		return false
	case e.pinHeader:
		return false
	case e.pin:
		if ev.Button() == gdk.BUTTON_SECONDARY {
			return sb.popup(pinMenu(e), ev)
		}
		if e.target == "" {
			return false
		}
	}
	id := e.key
	if e.pin {
		id = e.target
	}

	switch ev.Button() {
	case gdk.BUTTON_MIDDLE:
		sb.win.OpenInSplit(id, gtk.ORIENTATION_HORIZONTAL)
		return true
	case gdk.BUTTON_SECONDARY:
		_, pinned := sb.pinnedSessions()[id]
		return sb.popup(sessionMenu(id, pinned), ev)
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

// pinnedSessions returns the keys of the running sessions which are pinned.
func (sb *sidebar) pinnedSessions() map[string]bool {
	pinned := map[string]bool{}
	for _, pin := range sb.win.app.state.Pinned {
		if s := sb.win.app.findSession(pin.Host, pin.Name); s != nil {
			pinned[sessionKey(pin.Host, s.ID)] = true
		}
	}
	return pinned
}

// pinMenu is the context menu of a pinned session.
func pinMenu(e *listEntry) *glib.MenuModel {
	if e.target != "" {
		return sessionMenu(e.target, true)
	}
	remove := glib.MenuItemNewWithLabel("Unpin")
	remove.SetActionAndTargetValue("win.pin-remove", glib.VariantFromString(e.key))
	menu := glib.MenuNew()
	menu.AppendItem(remove)
	return &menu.MenuModel
}

// sessionMenu is the context menu of a session row. Its actions take the session key.
func sessionMenu(id string, pinned bool) *glib.MenuModel {
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
	if pinned {
		manage.AppendItem(item("Unpin", "win.session-unpin"))
	} else {
		manage.AppendItem(item("Pin", "win.session-pin"))
	}
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
