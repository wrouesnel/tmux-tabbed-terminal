package ui

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/gotk3/gotk3/pango"
	"go.uber.org/zap"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/sshconfig"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
	"github.com/wrouesnel/tmux-tabbed-terminal/version"
)

const (
	defaultWindowWidth  = 1100
	defaultWindowHeight = 680
	// sessionShortcuts is how many sessions have Alt+number shortcuts.
	sessionShortcuts = 9
)

// Window is a terminal window: the session list on the left and a tree of panes on the
// right.
type Window struct {
	app    *App
	window *gtk.ApplicationWindow
	header *gtk.HeaderBar

	outer   *gtk.Paned
	sidebar *sidebar
	// content is the terminal side of the window: the tab bar over the panes.
	content    *gtk.Box
	tabs       *tabBar
	layout     *layout
	activePane *Pane

	actions       map[string]*glib.SimpleAction
	sidebarAction *glib.SimpleAction
	groupAction   *glib.SimpleAction
	tabsAction    *glib.SimpleAction
	fullscreen    bool
	// status replaces the title bar subtitle while something is in progress.
	status string
}

func newWindow(app *App) *Window {
	w := &Window{app: app, actions: map[string]*glib.SimpleAction{}}

	w.window, _ = gtk.ApplicationWindowNew(app.gtkApp)
	w.window.SetDefaultSize(defaultWindowWidth, defaultWindowHeight)
	w.window.SetTitle(version.Name)
	w.window.SetIconName(IconName)
	addClass(w.window, "ttt-window")

	w.header = w.buildHeaderBar()
	w.window.SetTitlebar(w.header)

	w.sidebar = newSidebar(w)
	first := newPane(w)
	w.layout = newLayout(first)
	w.activePane = first
	w.tabs = newTabBar(w)
	w.content, _ = gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	w.content.PackStart(w.tabs.root, false, false, 0)
	w.content.PackStart(w.layout.area, true, true, 0)

	w.outer, _ = gtk.PanedNew(gtk.ORIENTATION_HORIZONTAL)
	w.placeSidebar()
	w.window.Add(w.outer)

	w.installActions()
	w.window.ShowAll()
	return w
}

// buildHeaderBar builds the title bar: the sidebar toggle on the left, split and the
// menu on the right. New sessions are made from the session list.
func (w *Window) buildHeaderBar() *gtk.HeaderBar {
	hb, _ := gtk.HeaderBarNew()
	hb.SetShowCloseButton(true)
	hb.SetTitle(version.Name)

	sidebarBtn, _ := gtk.ToggleButtonNew()
	sidebarIcon, _ := gtk.ImageNewFromIconName(firstIcon("sidebar-show-symbolic", "view-sidebar-start-symbolic",
		"view-list-symbolic"), gtk.ICON_SIZE_BUTTON)
	sidebarBtn.SetImage(sidebarIcon)
	sidebarBtn.SetActionName("win.show-sidebar")
	sidebarBtn.SetTooltipText("Show Sessions (F9)")
	hb.PackStart(sidebarBtn)

	menuBtn, _ := gtk.MenuButtonNew()
	menuIcon, _ := gtk.ImageNewFromIconName("open-menu-symbolic", gtk.ICON_SIZE_BUTTON)
	menuBtn.SetImage(menuIcon)
	menuBtn.SetMenuModel(mainMenu())
	menuBtn.SetTooltipText("Menu")
	hb.PackEnd(menuBtn)

	splitBtn, _ := gtk.ButtonNewFromIconName(firstIcon("view-dual-symbolic", "view-paged-symbolic"),
		gtk.ICON_SIZE_BUTTON)
	splitBtn.SetActionName("win.split-right")
	splitBtn.SetTooltipText("Split Right (Ctrl+Shift+E)")
	hb.PackEnd(splitBtn)
	return hb
}

// firstIcon returns the first icon the theme has, or the last one given.
func firstIcon(names ...string) string {
	if theme, err := gtk.IconThemeGetDefault(); err == nil {
		for _, name := range names {
			if theme.HasIcon(name) {
				return name
			}
		}
	}
	return names[len(names)-1]
}

// installActions adds the window actions used by menus, buttons and shortcuts.
func (w *Window) installActions() {
	m := w.window.IActionMap
	add := func(name string, fn func()) {
		w.actions[name] = addAction(m, name, fn)
	}
	addStr := func(name string, fn func(string)) {
		w.actions[name] = addStringAction(m, name, fn)
	}

	add("new-session", func() { w.PromptNewSession(w.activePane, nil) })
	add("split-right", func() { w.SplitActive(gtk.ORIENTATION_HORIZONTAL) })
	add("split-down", func() { w.SplitActive(gtk.ORIENTATION_VERTICAL) })
	add("close-pane", func() { w.ClosePane(w.activePane) })
	add("copy", func() { w.activePane.term.CopyClipboard() })
	add("paste", func() { w.activePane.term.PasteClipboard() })
	add("zoom-in", func() { w.activePane.Zoom(1) })
	add("zoom-out", func() { w.activePane.Zoom(-1) })
	add("zoom-normal", func() { w.activePane.Zoom(0) })
	add("next-session", func() { w.cycleSession(1) })
	add("prev-session", func() { w.cycleSession(-1) })
	add("next-pane", func() { w.cyclePane(1) })
	add("prev-pane", func() { w.cyclePane(-1) })
	add("rename-session", func() { w.RenameSession(w.activePane.SessionKey()) })
	add("kill-session", func() { w.KillSession(w.activePane.SessionKey()) })
	add("add-host", func() { w.AddHostDialog(LocalHost) })
	add("sidebar-other-side", func() { w.app.setSidebarRight(!w.app.sidebarRight) })
	add("fullscreen", w.toggleFullscreen)
	add("close-window", func() { w.window.Close() })
	for i := 1; i <= sessionShortcuts; i++ {
		index := i - 1
		add("switch-to-"+strconv.Itoa(i), func() { w.switchToIndex(index) })
	}

	addStr("session-open", w.ShowSession)
	addStr("session-split-right", func(id string) { w.OpenInSplit(id, gtk.ORIENTATION_HORIZONTAL) })
	addStr("session-split-down", func(id string) { w.OpenInSplit(id, gtk.ORIENTATION_VERTICAL) })
	addStr("session-rename", w.RenameSession)
	addStr("session-kill", w.KillSession)
	addStr("session-save-scrollback", func(key string) { w.SaveScrollback(key, false) })
	addStr("session-save-scrollback-as", func(key string) { w.SaveScrollback(key, true) })
	add("save-scrollback", func() { w.SaveScrollback(w.activePane.SessionKey(), false) })
	add("save-scrollback-as", func() { w.SaveScrollback(w.activePane.SessionKey(), true) })
	addStr("session-scrollback-dir", w.OpenScrollbackDir)
	add("scrollback-dir", func() { w.OpenScrollbackDir(w.activePane.SessionKey()) })
	addStr("host-new-session", func(host string) { w.NewSessionOn(w.activePane, host) })
	addStr("session-pin", func(key string) {
		if pin, ok := w.app.pinOf(key); ok {
			w.app.setPinned(pin, true)
		}
	})
	addStr("session-unpin", func(key string) {
		if pin, ok := w.app.pinOf(key); ok {
			w.app.setPinned(pin, false)
		}
	})
	addStr("pin-remove", func(key string) {
		if pin, ok := parsePinKey(key); ok {
			w.app.setPinned(pin, false)
		}
	})
	addStr("host-remove", w.confirmRemoveHost)
	addStr("host-add-via", w.AddHostDialog)

	add("find-session", func() {
		w.setSidebarVisible(true)
		w.sidebar.FocusSearch()
	})

	group := glib.SimpleActionNewStateful("group-sessions", nil, glib.VariantFromBoolean(w.app.grouped))
	group.Connect("activate", func() { w.app.setGrouped(!w.app.grouped) })
	m.AddAction(group)
	w.groupAction = group

	showTabs := glib.SimpleActionNewStateful("show-tabs", nil, glib.VariantFromBoolean(w.app.showTabs))
	showTabs.Connect("activate", func() { w.app.setShowTabs(!w.app.showTabs) })
	m.AddAction(showTabs)
	w.tabsAction = showTabs

	showSidebar := glib.SimpleActionNewStateful("show-sidebar", nil, glib.VariantFromBoolean(true))
	showSidebar.Connect("activate", func() {
		w.setSidebarVisible(!showSidebar.GetState().GetBoolean())
	})
	m.AddAction(showSidebar)
	w.sidebarAction = showSidebar
}

// setSidebarVisible shows or hides the session list.
func (w *Window) setSidebarVisible(visible bool) {
	w.sidebarAction.SetState(glib.VariantFromBoolean(visible))
	w.sidebar.root.SetVisible(visible)
}

// syncGroupAction shows the application's grouping setting on the window's toggle.
func (w *Window) syncGroupAction() {
	w.groupAction.SetState(glib.VariantFromBoolean(w.app.grouped))
}

// syncTabsAction shows the application's tab bar setting on the window's toggle and bar.
func (w *Window) syncTabsAction() {
	w.tabsAction.SetState(glib.VariantFromBoolean(w.app.showTabs))
	w.tabs.syncVisible()
}

// updateActionState enables the actions which make sense for the active pane.
func (w *Window) updateActionState() {
	p := w.activePane
	w.actions["copy"].SetEnabled(p.running && p.term.HasSelection())
	w.actions["paste"].SetEnabled(p.running)
	_, s := w.app.lookup(p.SessionKey())
	hasSession := s != nil
	w.actions["rename-session"].SetEnabled(hasSession)
	w.actions["kill-session"].SetEnabled(hasSession)
	w.actions["save-scrollback"].SetEnabled(hasSession)
	w.actions["save-scrollback-as"].SetEnabled(hasSession)
}

// panes returns the window's panes.
func (w *Window) panes() []*Pane {
	return w.layout.Panes()
}

// setActivePane records which pane has focus.
func (w *Window) setActivePane(p *Pane) {
	if w.activePane == p {
		return
	}
	w.activePane = p
	w.refresh()
}

// ShowSession shows a session in the active pane.
func (w *Window) ShowSession(id string) {
	w.activePane.Show(id)
	w.activePane.Focus()
}

// OpenInSplit opens a session in a new pane next to the active one.
func (w *Window) OpenInSplit(id string, orientation gtk.Orientation) {
	p := w.split(orientation)
	p.Show(id)
	p.Focus()
}

// SplitActive splits the active pane. The new pane starts empty, ready for a session to
// be picked from the list or created.
func (w *Window) SplitActive(orientation gtk.Orientation) {
	p := w.split(orientation)
	p.showEmpty("Empty pane", "Choose a session from the list, or start a new one.", false)
	p.Focus()
}

func (w *Window) split(orientation gtk.Orientation) *Pane {
	p := newPane(w)
	w.layout.Split(w.activePane, p, orientation, false)
	w.refresh()
	return p
}

// dropSession handles a session dropped on a pane: the center shows it there, and an
// edge splits the pane and shows it on that side.
func (w *Window) dropSession(target *Pane, key string, zone dropZone) {
	if zone == zoneCenter {
		target.Show(key)
		target.Focus()
		return
	}
	orientation, before := zone.split()
	p := newPane(w)
	w.layout.Split(target, p, orientation, before)
	p.Show(key)
	p.Focus()
	w.refresh()
}

// placeSidebar puts the session list on the side the application says, keeping its width.
func (w *Window) placeSidebar() {
	width := w.sidebar.root.GetAllocatedWidth()
	if width <= 1 {
		width = w.app.cfg.Appearance.SidebarWidth
	}
	total := w.outer.GetAllocatedWidth()
	if total <= 1 {
		total, _ = w.window.GetSize()
	}
	if parent, err := w.sidebar.root.GetParent(); err == nil && parent != nil {
		w.outer.Remove(w.sidebar.root)
		w.outer.Remove(w.content)
	}
	if w.app.sidebarRight {
		w.outer.Pack1(w.content, true, false)
		w.outer.Pack2(w.sidebar.root, false, false)
		w.outer.SetPosition(total - width)
	} else {
		w.outer.Pack1(w.sidebar.root, false, false)
		w.outer.Pack2(w.content, true, false)
		w.outer.SetPosition(width)
	}
}

// ClosePane closes a pane. Its session keeps running. Closing the last pane closes the
// window.
func (w *Window) ClosePane(p *Pane) {
	sibling := w.layout.Remove(p)
	if sibling == nil {
		w.window.Close()
		return
	}
	p.close()
	if w.activePane == p {
		w.activePane = sibling.firstLeaf()
	}
	w.activePane.Focus()
	w.refresh()
}

// paneExited handles a pane whose tmux client stopped.
func (w *Window) paneExited(p *Pane) {
	key := p.SessionKey()
	hostName, _ := splitKey(key)
	h := w.app.host(hostName)
	if h == nil {
		p.showEmpty("Host removed", "The host of this session is no longer listed.", false)
		w.refresh()
		return
	}
	// Read the sessions now: the last poll may not know a session made since.
	runAsync(w.app, h.client.Snapshot, func(snap *tmux.Snapshot, err error) {
		if p.closed || p.running {
			return
		}
		if err != nil {
			w.app.log.Warn("Could not read tmux sessions", zap.String("host", h.Name), zap.Error(err))
		}
		w.app.applySnapshot(h, snap, err)

		_, s := w.app.lookup(key)
		switch {
		case s != nil:
			// The client detached, but the session is still there.
			p.showEmpty("Detached", fmt.Sprintf("The session “%s” is still running.", w.app.sessionName(key)), true)
		case err != nil:
			p.showEmpty("Disconnected", fmt.Sprintf("Could not reach %s: %v", h.Name, err), true)
		case len(w.panes()) > 1:
			w.ClosePane(p)
			return
		default:
			// The session ended. Move on to another one, as tmux does with detach-on-destroy off.
			if next := w.app.pickSession(w.app.visibleSessions()); next != "" && next != key {
				p.attach(next)
			} else {
				p.showEmpty("No sessions", "There are no tmux sessions running.", false)
			}
		}
		w.refresh()
	})
}

// newSessionDir returns the directory for a new session on a host: that of the active
// pane's session if it's on the same host, so a new session opens where the user is
// working. Remote sessions otherwise start in the remote home directory.
func (w *Window) newSessionDir(hostName string) string {
	if p := w.activePane; p.hostName == hostName {
		if _, s := w.app.lookup(p.SessionKey()); s != nil {
			if aw := s.ActiveWindow(); aw != nil && aw.Path != "" {
				return aw.Path
			}
		}
	}
	if hostName != LocalHost {
		return ""
	}
	if dir := w.app.cfg.Behaviour.NewSessionDirectory; dir != "" {
		return os.ExpandEnv(dir)
	}
	home, _ := os.UserHomeDir()
	return home
}

// NewSession creates a session and shows it in a pane. It goes on the host of the
// session the pane last showed, or this machine.
func (w *Window) NewSession(p *Pane) {
	hostName := p.hostName
	if hostName == "" || w.app.host(hostName) == nil {
		hostName = LocalHost
	}
	w.NewSessionOn(p, hostName)
}

// hostChooserHeight is the most the host chooser grows before it scrolls.
const hostChooserHeight = 320

// PromptNewSession creates a session for a pane. With one host it goes straight there;
// with several, a menu by anchor asks which, starting at the pane's host. A nil anchor
// points the menu at the pane.
func (w *Window) PromptNewSession(p *Pane, anchor gtk.IWidget) {
	if len(w.app.hosts) <= 1 {
		w.NewSession(p)
		return
	}
	if anchor == nil {
		anchor = p.root
	}
	popover, err := gtk.PopoverNew(anchor)
	if err != nil {
		return
	}
	addClass(popover, "ttt-host-chooser")

	title, _ := gtk.LabelNew("New Session On")
	title.SetXAlign(0)
	addClass(title, "ttt-group-header")
	addClass(title, "dim-label")

	list, _ := gtk.ListBoxNew()
	list.SetSelectionMode(gtk.SELECTION_BROWSE)
	list.SetActivateOnSingleClick(true)
	names := []string{}
	for _, h := range w.app.hosts {
		row, _ := gtk.ListBoxRowNew()
		box, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, rowSpacing)
		iconName := firstIcon("network-server-symbolic", "computer-symbolic")
		if h.Local() {
			iconName = firstIcon("computer-symbolic", "user-home-symbolic")
		}
		icon, _ := gtk.ImageNewFromIconName(iconName, gtk.ICON_SIZE_MENU)
		label, _ := gtk.LabelNew(h.Name)
		label.SetXAlign(0)
		box.PackStart(icon, false, false, 0)
		box.PackStart(label, true, true, 0)
		if h.err != nil {
			status, _ := gtk.LabelNew("unreachable")
			addClass(status, "dim-label")
			box.PackStart(status, false, false, 0)
			row.SetSensitive(false)
		}
		box.SetMarginStart(6)  //nolint:mnd // row padding
		box.SetMarginEnd(6)    //nolint:mnd
		box.SetMarginTop(4)    //nolint:mnd
		box.SetMarginBottom(4) //nolint:mnd
		row.Add(box)
		list.Add(row)
		names = append(names, h.Name)
		if h.Name == p.hostName || (p.hostName == "" && h.Local()) {
			list.SelectRow(row)
		}
	}
	list.Connect("row-activated", func(_ interface{}, row *gtk.ListBoxRow) {
		popover.Popdown()
		if i := row.GetIndex(); i >= 0 && i < len(names) {
			w.NewSessionOn(p, names[i])
		}
	})

	scroller, _ := gtk.ScrolledWindowNew(nil, nil)
	scroller.SetPolicy(gtk.POLICY_NEVER, gtk.POLICY_AUTOMATIC)
	scroller.SetPropagateNaturalHeight(true)
	scroller.SetMaxContentHeight(hostChooserHeight)
	scroller.Add(list)

	box, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	box.PackStart(title, false, false, 0)
	box.PackStart(scroller, true, true, 0)
	box.SetMarginBottom(4) //nolint:mnd // popover padding
	popover.Add(box)
	popover.Connect("closed", func() { popover.Destroy() })
	box.ShowAll()
	popover.Popup()
	if row := list.GetSelectedRow(); row != nil {
		row.GrabFocus()
	}
}

// NewSessionOn creates a session on a host and shows it in a pane.
func (w *Window) NewSessionOn(p *Pane, hostName string) {
	w.NewNamedSession(p, hostName, "")
}

// NewNamedSession creates a session with a name on a host and shows it in a pane. An
// empty name lets tmux choose.
func (w *Window) NewNamedSession(p *Pane, hostName, name string) {
	h := w.app.host(hostName)
	if h == nil {
		return
	}
	dir := w.newSessionDir(hostName)
	runAsync(w.app, func(ctx context.Context) (string, error) {
		return h.client.NewSession(ctx, name, dir)
	}, func(id string, err error) {
		if err != nil {
			w.app.log.Error("Could not create tmux session", zap.String("host", hostName), zap.Error(err))
			w.showError("Could not create a tmux session on "+hostName, err.Error())
			return
		}
		if p.closed {
			return
		}
		p.Show(sessionKey(hostName, id))
		p.Focus()
		h.requestPoll()
	})
}

// cycleSession shows the next or previous session of the list in the active pane.
func (w *Window) cycleSession(delta int) {
	ids := w.sidebar.visibleIDs()
	if len(ids) == 0 {
		return
	}
	current := -1
	for i, id := range ids {
		if id == w.activePane.SessionKey() {
			current = i
		}
	}
	next := (current + delta + len(ids)) % len(ids)
	if current < 0 && delta < 0 {
		next = len(ids) - 1
	}
	w.ShowSession(ids[next])
}

// switchToIndex shows the session at a position in the list.
func (w *Window) switchToIndex(index int) {
	if ids := w.sidebar.visibleIDs(); index < len(ids) {
		w.ShowSession(ids[index])
	}
}

// cyclePane moves focus to the next or previous pane.
func (w *Window) cyclePane(delta int) {
	panes := w.panes()
	for i, p := range panes {
		if p == w.activePane {
			panes[(i+delta+len(panes))%len(panes)].Focus()
			return
		}
	}
}

func (w *Window) toggleFullscreen() {
	if w.fullscreen {
		w.window.Unfullscreen()
	} else {
		w.window.Fullscreen()
	}
	w.fullscreen = !w.fullscreen
}

// refresh redraws the window from the application's current state.
func (w *Window) refresh() {
	panes := w.panes()
	split := len(panes) > 1

	shown, selected := w.shownSessions()
	w.sidebar.update(shown, selected)
	w.tabs.update(w.sidebar.visibleIDs(), shown, selected)

	now := timeNow()
	for _, p := range panes {
		name := "Empty"
		busy := false
		if p.running {
			name = w.app.sessionName(p.SessionKey())
			busy = w.app.tracker.State(p.SessionKey(), now).Active
		}
		p.updateHeader(name, busy, split)
		p.updateScrollbar()
	}

	title, subtitle := version.Name, ""
	if h, s := w.app.lookup(selected); s != nil {
		title = s.Name
		if aw := s.ActiveWindow(); aw != nil {
			subtitle = windowSubtitle(aw)
		}
		if !h.Local() {
			subtitle = h.Name + " · " + subtitle
		}
	}
	if w.status != "" {
		subtitle = w.status
	}
	w.header.SetTitle(title)
	w.header.SetSubtitle(subtitle)
	w.window.SetTitle(title)
	w.updateActionState()
}

// shownSessions returns the sessions shown in the window's panes, and the one in the
// focused pane, or "".
func (w *Window) shownSessions() (map[string]bool, string) {
	shown := map[string]bool{}
	for _, p := range w.panes() {
		if p.running {
			shown[p.SessionKey()] = true
		}
	}
	selected := ""
	if w.activePane.running {
		selected = w.activePane.SessionKey()
	}
	return shown, selected
}

// updateTabs shows the sessions in the session list's current view as tabs, after the
// view changes.
func (w *Window) updateTabs() {
	shown, selected := w.shownSessions()
	w.tabs.update(w.sidebar.visibleIDs(), shown, selected)
}

// setStatus shows a message in the title bar until it's cleared with "".
func (w *Window) setStatus(msg string) {
	w.status = msg
	w.refresh()
}

// windowSubtitle describes a tmux window for the title bar.
func windowSubtitle(aw *tmux.Window) string {
	if aw.Title != "" {
		return fmt.Sprintf("%d: %s — %s", aw.Index, aw.Name, aw.Title)
	}
	return fmt.Sprintf("%d: %s", aw.Index, aw.Name)
}

// destroyed releases the window's panes when it closes.
func (w *Window) destroyed() {
	for _, p := range w.panes() {
		p.close()
	}
}

// RenameSession asks for a new name for a session, by key.
func (w *Window) RenameSession(key string) {
	h, sess := w.app.lookup(key)
	if sess == nil {
		return
	}
	id := sess.ID
	dlg, err := gtk.DialogNewWithButtons("Rename Session", w.window, gtk.DIALOG_MODAL|gtk.DIALOG_DESTROY_WITH_PARENT|
		gtk.DIALOG_USE_HEADER_BAR,
		[]interface{}{"_Cancel", gtk.RESPONSE_CANCEL},
		[]interface{}{"_Rename", gtk.RESPONSE_ACCEPT})
	if err != nil {
		return
	}
	defer dlg.Destroy()
	dlg.SetDefaultResponse(gtk.RESPONSE_ACCEPT)
	if btn, err := dlg.GetWidgetForResponse(gtk.RESPONSE_ACCEPT); err == nil {
		addClass(btn.ToWidget(), "suggested-action")
	}

	entry, _ := gtk.EntryNew()
	entry.SetText(sess.Name)
	entry.SetActivatesDefault(true)
	content, _ := dlg.GetContentArea()
	content.PackStart(padded(entry), true, true, 0)
	dlg.ShowAll()

	if dlg.Run() != gtk.RESPONSE_ACCEPT {
		return
	}
	name, _ := entry.GetText()
	if name == "" {
		return
	}
	runAsync(w.app, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, h.client.RenameSession(ctx, id, name)
	}, func(_ struct{}, err error) {
		if err != nil {
			w.showError("Could not rename the session", err.Error())
		}
		h.requestPoll()
	})
}

// KillSession destroys a session, by key, after confirming if configured to.
func (w *Window) KillSession(key string) {
	h, sess := w.app.lookup(key)
	if sess == nil {
		return
	}
	id := sess.ID
	if w.app.cfg.Behaviour.ConfirmKill {
		dlg := gtk.MessageDialogNew(w.window, gtk.DIALOG_MODAL|gtk.DIALOG_DESTROY_WITH_PARENT,
			gtk.MESSAGE_WARNING, gtk.BUTTONS_NONE, "Kill session “%s”?", w.app.sessionName(key))
		dlg.FormatSecondaryText("All programs running in the session will be terminated.")
		_, _ = dlg.AddButton("_Cancel", gtk.RESPONSE_CANCEL)
		killBtn, _ := dlg.AddButton("_Kill Session", gtk.RESPONSE_ACCEPT)
		addClass(killBtn, "destructive-action")
		dlg.SetDefaultResponse(gtk.RESPONSE_CANCEL)
		response := dlg.Run()
		dlg.Destroy()
		if response != gtk.RESPONSE_ACCEPT {
			return
		}
	}
	runAsync(w.app, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, h.client.KillSession(ctx, id)
	}, func(_ struct{}, err error) {
		if err != nil {
			w.showError("Could not kill the session", err.Error())
		}
		h.requestPoll()
	})
}

// sshHostListHeight is the most the ssh config host list grows before it scrolls.
const sshHostListHeight = 260

// originLabel is how an origin host is shown in the Add Host dialog.
func originLabel(name string) string {
	if name == LocalHost {
		return "This computer"
	}
	return name
}

// AddHostDialog asks for a host to list sessions from: typed in, or picked from the
// concrete hosts of the origin's ~/.ssh/config. The host is reached through the origin,
// which starts as origin: LocalHost for this machine, or any listed remote host.
func (w *Window) AddHostDialog(origin string) {
	if w.app.host(origin) == nil {
		origin = LocalHost
	}
	dlg, err := gtk.DialogNewWithButtons("Add Host", w.window, gtk.DIALOG_MODAL|gtk.DIALOG_DESTROY_WITH_PARENT|
		gtk.DIALOG_USE_HEADER_BAR,
		[]interface{}{"_Cancel", gtk.RESPONSE_CANCEL},
		[]interface{}{"_Add", gtk.RESPONSE_ACCEPT})
	if err != nil {
		return
	}
	defer dlg.Destroy()
	dlg.SetDefaultResponse(gtk.RESPONSE_ACCEPT)
	if btn, err := dlg.GetWidgetForResponse(gtk.RESPONSE_ACCEPT); err == nil {
		addClass(btn.ToWidget(), "suggested-action")
	}

	box, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, paneSpacing)

	// The origin: a dropdown of this machine and the listed remote hosts, which can be
	// searched by typing.
	originBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, paneSpacing)
	originTitle, _ := gtk.LabelNew("Origin")
	addClass(originTitle, "dim-label")
	originCombo, _ := gtk.ComboBoxTextNewWithEntry()
	originCombo.SetHExpand(true)
	byLabel := map[string]string{}
	completions, _ := gtk.ListStoreNew(glib.TYPE_STRING)
	for _, h := range w.app.hosts {
		label := originLabel(h.Name)
		originCombo.Append(h.Name, label)
		byLabel[strings.ToLower(label)] = h.Name
		_ = completions.SetValue(completions.Append(), 0, label)
	}
	originEntry, _ := originCombo.GetEntry()
	completion, _ := gtk.EntryCompletionNew()
	completion.SetModel(completions)
	completion.SetTextColumn(0)
	completion.SetInlineCompletion(true)
	completion.SetPopupCompletion(true)
	originEntry.SetCompletion(completion)
	originBox.PackStart(originTitle, false, false, 0)
	originBox.PackStart(originCombo, true, true, 0)
	box.PackStart(originBox, false, false, 0)

	entry, _ := gtk.EntryNew()
	entry.SetPlaceholderText("user@example.com")
	entry.SetActivatesDefault(true)
	entry.SetWidthChars(36) //nolint:mnd // room for a typical user@host
	hint, _ := gtk.LabelNew("An ssh destination or ~/.ssh/config alias, as the origin knows it. ssh " +
		"runs on the origin, with its configuration and keys. The host needs key or agent " +
		"authentication, and tmux installed.")
	hint.SetLineWrap(true)
	hint.SetMaxWidthChars(48) //nolint:mnd // dialog width
	hint.SetXAlign(0)
	addClass(hint, "dim-label")
	box.PackStart(entry, false, false, 0)
	box.PackStart(hint, false, false, 0)
	forward, _ := gtk.CheckButtonNewWithLabel("Forward my ssh agent through the origin")
	forward.SetTooltipText("Lets ssh on the origin use your keys, for when it has none of its own for " +
		"the host. Anyone with root on the origin can use them while you're connected.")
	box.PackStart(forward, false, false, 0)

	// The origin's ssh config hosts, filtered by what's typed.
	heading, _ := gtk.LabelNew("")
	heading.SetXAlign(0)
	addClass(heading, "ttt-group-header")
	addClass(heading, "dim-label")
	status, _ := gtk.LabelNew("")
	status.SetLineWrap(true)
	status.SetXAlign(0)
	addClass(status, "dim-label")
	holder, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	scroller, _ := gtk.ScrolledWindowNew(nil, nil)
	scroller.SetPolicy(gtk.POLICY_NEVER, gtk.POLICY_AUTOMATIC)
	scroller.SetShadowType(gtk.SHADOW_IN)
	scroller.SetPropagateNaturalHeight(true)
	scroller.SetMaxContentHeight(sshHostListHeight)
	scroller.Add(holder)
	box.PackStart(heading, false, false, 0)
	box.PackStart(status, false, false, 0)
	box.PackStart(scroller, true, true, 0)

	var list *gtk.ListBox
	filling := false
	showHosts := func(origin string, hosts []sshconfig.Host) {
		if list != nil {
			holder.Remove(list)
		}
		list, _ = gtk.ListBoxNew()
		list.SetSelectionMode(gtk.SELECTION_SINGLE)
		list.SetActivateOnSingleClick(false)
		aliases := make([]string, 0, len(hosts))
		for _, h := range hosts {
			name := h.Alias
			if origin != LocalHost {
				name += " via " + origin
			}
			list.Add(sshHostRow(h, w.app.host(name) != nil))
			aliases = append(aliases, h.Alias)
		}
		alias := func(row *gtk.ListBoxRow) string {
			if i := row.GetIndex(); i >= 0 && i < len(aliases) {
				return aliases[i]
			}
			return ""
		}
		// A click fills in the entry; a double click or Enter adds the host.
		list.Connect("row-selected", func(_ interface{}, row *gtk.ListBoxRow) {
			if row != nil && row.Object != nil {
				filling = true
				entry.SetText(alias(row))
				filling = false
			}
		})
		list.Connect("row-activated", func(_ interface{}, row *gtk.ListBoxRow) {
			entry.SetText(alias(row))
			dlg.Response(gtk.RESPONSE_ACCEPT)
		})
		list.SetFilterFunc(func(row *gtk.ListBoxRow) bool {
			text, _ := entry.GetText()
			h := hosts[row.GetIndex()]
			text = strings.ToLower(strings.TrimSpace(text))
			return filling || text == "" || strings.Contains(strings.ToLower(h.Alias+" "+h.HostName+" "+h.User), text)
		})
		holder.PackStart(list, true, true, 0)
		list.ShowAll()
		scroller.SetVisible(len(hosts) > 0)
		status.SetVisible(len(hosts) == 0)
		status.SetText("No hosts in its ~/.ssh/config.")
	}
	entry.Connect("changed", func() {
		if !filling && list != nil {
			list.InvalidateFilter()
		}
	})

	// Loading the list: straight away for this machine, over ssh for a remote origin.
	// generation discards a load which finishes after the origin changed again.
	current, generation := "", 0
	load := func(origin string) {
		current = origin
		generation++
		gen := generation
		heading.SetText("From ~/.ssh/config on " + strings.ToLower(originLabel(origin)[:1]) + originLabel(origin)[1:])
		if origin == LocalHost {
			hosts, err := sshconfig.Load(sshconfig.DefaultPath())
			if err != nil {
				w.app.log.Warn("Could not read the ssh configuration", zap.Error(err))
			}
			showHosts(origin, hosts)
			return
		}
		h := w.app.host(origin)
		if h == nil {
			return
		}
		showHosts(origin, nil)
		status.SetText("Reading ~/.ssh/config from " + origin + "…")
		runAsync(w.app, func(ctx context.Context) ([]sshconfig.Host, error) {
			return loadRemoteSSHConfig(ctx, h.client.SSH)
		}, func(hosts []sshconfig.Host, err error) {
			if gen != generation {
				return
			}
			if err != nil {
				showHosts(origin, nil)
				status.SetText("Could not read ~/.ssh/config from " + origin + ": " + err.Error())
				return
			}
			showHosts(origin, hosts)
		})
	}
	originCombo.Connect("changed", func() {
		id := originCombo.GetActiveID()
		if id == "" {
			// Typed rather than picked: accept it once it names an origin.
			text, _ := originEntry.GetText()
			id = byLabel[strings.ToLower(strings.TrimSpace(text))]
		}
		dlg.SetResponseSensitive(gtk.RESPONSE_ACCEPT, id != "")
		forward.SetSensitive(id != "" && id != LocalHost)
		setClass(originEntry, "error", id == "")
		if id != "" && id != current {
			load(id)
		}
	})
	originCombo.SetActiveID(origin)

	content, _ := dlg.GetContentArea()
	content.PackStart(padded(box), true, true, 0)
	dlg.ShowAll()
	load(origin)
	// Once the dialog is up: it focuses the origin otherwise, its first field.
	glib.IdleAdd(func() { entry.GrabFocus() })

	if dlg.Run() != gtk.RESPONSE_ACCEPT || current == "" {
		return
	}
	destination, _ := entry.GetText()
	w.app.AddHost(destination, current, forward.GetActive(), w)
}

// confirmRemoveHost removes a host, after listing and confirming the hosts reached
// through it, which go too.
func (w *Window) confirmRemoveHost(name string) {
	dependents := w.app.dependents(name)
	if len(dependents) == 0 {
		w.app.RemoveHost(name)
		return
	}
	names := make([]string, 0, len(dependents))
	for _, d := range dependents {
		names = append(names, "• "+d.Name)
	}
	dlg := gtk.MessageDialogNew(w.window, gtk.DIALOG_MODAL|gtk.DIALOG_DESTROY_WITH_PARENT,
		gtk.MESSAGE_WARNING, gtk.BUTTONS_NONE, "Remove %s and the hosts reached through it?", name)
	dlg.FormatSecondaryText("These hosts are reached through %s, so they will be removed too:\n\n%s\n\n"+
		"Panes showing their sessions keep running.", name, strings.Join(names, "\n"))
	_, _ = dlg.AddButton("_Cancel", gtk.RESPONSE_CANCEL)
	removeBtn, _ := dlg.AddButton(fmt.Sprintf("_Remove %d Hosts", len(dependents)+1), gtk.RESPONSE_ACCEPT)
	addClass(removeBtn, "destructive-action")
	dlg.SetDefaultResponse(gtk.RESPONSE_CANCEL)
	response := dlg.Run()
	dlg.Destroy()
	if response == gtk.RESPONSE_ACCEPT {
		w.app.RemoveHost(name)
	}
}

// sshHostRow is a host of the ssh configuration in the Add Host list.
func sshHostRow(h sshconfig.Host, added bool) *gtk.ListBoxRow {
	row, _ := gtk.ListBoxRowNew()
	box, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, rowSpacing)
	box.SetMarginStart(8)  //nolint:mnd // row padding
	box.SetMarginEnd(8)    //nolint:mnd
	box.SetMarginTop(4)    //nolint:mnd
	box.SetMarginBottom(4) //nolint:mnd

	text, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	name, _ := gtk.LabelNew(h.Alias)
	name.SetXAlign(0)
	text.PackStart(name, false, false, 0)
	detail := h.HostName
	if h.User != "" {
		detail = h.User + "@" + detail
		if h.HostName == "" {
			detail = h.User + "@" + h.Alias
		}
	}
	if detail != "" {
		sub, _ := gtk.LabelNew(detail)
		sub.SetXAlign(0)
		sub.SetEllipsize(pango.ELLIPSIZE_END)
		addClass(sub, "dim-label")
		addClass(sub, "ttt-session-subtitle")
		text.PackStart(sub, false, false, 0)
	}
	box.PackStart(text, true, true, 0)
	if added {
		mark, _ := gtk.LabelNew("added")
		addClass(mark, "dim-label")
		box.PackStart(mark, false, false, 0)
		row.SetSensitive(false)
	}
	row.Add(box)
	return row
}

// dialogPadding is the margin around dialog content.
const dialogPadding = 12

// padded gives a dialog's content its margin.
func padded(widget gtk.IWidget) gtk.IWidget {
	box, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	box.SetMarginStart(dialogPadding)
	box.SetMarginEnd(dialogPadding)
	box.SetMarginTop(dialogPadding)
	box.SetMarginBottom(dialogPadding)
	box.PackStart(widget, true, true, 0)
	return box
}

// showError shows an error dialog.
func (w *Window) showError(primary, secondary string) {
	dlg := gtk.MessageDialogNew(w.window, gtk.DIALOG_MODAL|gtk.DIALOG_DESTROY_WITH_PARENT,
		gtk.MESSAGE_ERROR, gtk.BUTTONS_CLOSE, "%s", primary)
	dlg.FormatSecondaryText("%s", secondary)
	dlg.Run()
	dlg.Destroy()
}
