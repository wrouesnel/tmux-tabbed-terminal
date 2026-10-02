package ui

import (
	"fmt"
	"os"
	"strconv"

	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"go.uber.org/zap"

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

	outer      *gtk.Paned
	sidebar    *sidebar
	layout     *layout
	activePane *Pane

	actions       map[string]*glib.SimpleAction
	sidebarAction *glib.SimpleAction
	groupAction   *glib.SimpleAction
	fullscreen    bool
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

	w.outer, _ = gtk.PanedNew(gtk.ORIENTATION_HORIZONTAL)
	w.outer.Pack1(w.sidebar.root, false, false)
	w.outer.Pack2(w.layout.area, true, false)
	w.outer.SetPosition(app.cfg.Appearance.SidebarWidth)
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

	add("new-session", func() { w.NewSession(w.activePane) })
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
	add("rename-session", func() { w.RenameSession(w.activePane.SessionID()) })
	add("kill-session", func() { w.KillSession(w.activePane.SessionID()) })
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

	add("find-session", func() {
		w.setSidebarVisible(true)
		w.sidebar.FocusSearch()
	})

	group := glib.SimpleActionNewStateful("group-sessions", nil, glib.VariantFromBoolean(w.app.grouped))
	group.Connect("activate", func() { w.app.setGrouped(!w.app.grouped) })
	m.AddAction(group)
	w.groupAction = group

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

// updateActionState enables the actions which make sense for the active pane.
func (w *Window) updateActionState() {
	p := w.activePane
	w.actions["copy"].SetEnabled(p.running && p.term.HasSelection())
	w.actions["paste"].SetEnabled(p.running)
	hasSession := p.SessionID() != "" && w.app.snapshot.Session(p.SessionID()) != nil
	w.actions["rename-session"].SetEnabled(hasSession)
	w.actions["kill-session"].SetEnabled(hasSession)
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
	w.layout.Split(w.activePane, p, orientation)
	w.refresh()
	return p
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
	id := p.SessionID()
	// Read the sessions now: the last poll may not know a session made since.
	ctx, cancel := w.app.commandContext()
	snap, err := w.app.client.Snapshot(ctx)
	cancel()
	if err != nil {
		w.app.log.Warn("Could not read tmux sessions", zap.Error(err))
	} else {
		w.app.applySnapshot(snap)
	}

	switch {
	case w.app.snapshot.Session(id) != nil:
		// The client detached, but the session is still there.
		p.showEmpty("Detached", fmt.Sprintf("The session “%s” is still running.", w.sessionName(id)), true)
	case len(w.panes()) > 1:
		w.ClosePane(p)
		return
	default:
		// The session ended. Move on to another one, as tmux does with detach-on-destroy off.
		if next := w.app.pickSession(w.app.visibleSessions()); next != "" && next != id {
			p.attach(next)
		} else {
			p.showEmpty("No sessions", "There are no tmux sessions running.", false)
		}
	}
	w.app.requestPoll()
	w.refresh()
}

// sessionName returns the name of a session, or its ID if it isn't known.
func (w *Window) sessionName(id string) string {
	if s := w.app.snapshot.Session(id); s != nil {
		return s.Name
	}
	return id
}

// newSessionDir returns the directory for a new session: that of the active pane's
// session if known, so a new session opens where the user is working.
func (w *Window) newSessionDir() string {
	if s := w.app.snapshot.Session(w.activePane.SessionID()); s != nil {
		if aw := s.ActiveWindow(); aw != nil && aw.Path != "" {
			return aw.Path
		}
	}
	if dir := w.app.cfg.Behaviour.NewSessionDirectory; dir != "" {
		return os.ExpandEnv(dir)
	}
	home, _ := os.UserHomeDir()
	return home
}

// NewSession creates a session and shows it in a pane.
func (w *Window) NewSession(p *Pane) {
	ctx, cancel := w.app.commandContext()
	defer cancel()
	id, err := w.app.client.NewSession(ctx, "", w.newSessionDir())
	if err != nil {
		w.app.log.Error("Could not create tmux session", zap.Error(err))
		w.showError("Could not create a tmux session", err.Error())
		return
	}
	p.Show(id)
	p.Focus()
	w.app.requestPoll()
}

// cycleSession shows the next or previous session of the list in the active pane.
func (w *Window) cycleSession(delta int) {
	ids := w.sidebar.visibleIDs()
	if len(ids) == 0 {
		return
	}
	current := -1
	for i, id := range ids {
		if id == w.activePane.SessionID() {
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
	snap := w.app.snapshot
	panes := w.panes()
	split := len(panes) > 1

	shown := map[string]bool{}
	for _, p := range panes {
		if p.running {
			shown[p.SessionID()] = true
		}
	}
	selected := ""
	if w.activePane.running {
		selected = w.activePane.SessionID()
	}
	w.sidebar.update(snap, w.app.tracker, shown, selected, w.app.ownTTYs())

	now := timeNow()
	for _, p := range panes {
		name := "Empty"
		busy := false
		if p.running {
			name = w.sessionName(p.SessionID())
			busy = w.app.tracker.State(p.SessionID(), now).Active
		}
		p.updateHeader(name, busy, split)
	}

	title, subtitle := version.Name, ""
	if s := snap.Session(selected); s != nil {
		title = s.Name
		if aw := s.ActiveWindow(); aw != nil {
			subtitle = windowSubtitle(aw)
		}
	}
	w.header.SetTitle(title)
	w.header.SetSubtitle(subtitle)
	w.window.SetTitle(title)
	w.updateActionState()
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

// RenameSession asks for a new name for a session.
func (w *Window) RenameSession(id string) {
	if id == "" {
		return
	}
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
	entry.SetText(w.sessionName(id))
	entry.SetActivatesDefault(true)
	entry.SetMarginStart(12)  //nolint:mnd // dialog padding
	entry.SetMarginEnd(12)    //nolint:mnd
	entry.SetMarginTop(12)    //nolint:mnd
	entry.SetMarginBottom(12) //nolint:mnd
	content, _ := dlg.GetContentArea()
	content.PackStart(entry, true, true, 0)
	dlg.ShowAll()

	if dlg.Run() != gtk.RESPONSE_ACCEPT {
		return
	}
	name, _ := entry.GetText()
	if name == "" {
		return
	}
	ctx, cancel := w.app.commandContext()
	defer cancel()
	if err := w.app.client.RenameSession(ctx, id, name); err != nil {
		w.showError("Could not rename the session", err.Error())
	}
	w.app.requestPoll()
}

// KillSession destroys a session, after confirming if configured to.
func (w *Window) KillSession(id string) {
	if id == "" {
		return
	}
	if w.app.cfg.Behaviour.ConfirmKill {
		dlg := gtk.MessageDialogNew(w.window, gtk.DIALOG_MODAL|gtk.DIALOG_DESTROY_WITH_PARENT,
			gtk.MESSAGE_WARNING, gtk.BUTTONS_NONE, "Kill session “%s”?", w.sessionName(id))
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
	ctx, cancel := w.app.commandContext()
	defer cancel()
	if err := w.app.client.KillSession(ctx, id); err != nil {
		w.showError("Could not kill the session", err.Error())
	}
	w.app.requestPoll()
}

// showError shows an error dialog.
func (w *Window) showError(primary, secondary string) {
	dlg := gtk.MessageDialogNew(w.window, gtk.DIALOG_MODAL|gtk.DIALOG_DESTROY_WITH_PARENT,
		gtk.MESSAGE_ERROR, gtk.BUTTONS_CLOSE, "%s", primary)
	dlg.FormatSecondaryText("%s", secondary)
	dlg.Run()
	dlg.Destroy()
}
