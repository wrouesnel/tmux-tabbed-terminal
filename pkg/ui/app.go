// Package ui is the GTK3 user interface: windows with a list of tmux sessions on the left
// and one or more terminals, each attached to a session, on the right.
//
// All GTK calls happen on the main thread. Work on other goroutines hands its results back
// with glib.IdleAdd.
package ui

import (
	"context"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/pkg/errors"
	logutil "github.com/wrouesnel/go.logutil"
	"go.uber.org/zap"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/activity"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/sessionlist"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/vte"
	"github.com/wrouesnel/tmux-tabbed-terminal/version"
)

// AppID is the D-Bus application ID. It also names the desktop file and icon.
const AppID = "io.github.wrouesnel.TmuxTabbedTerminal"

// IconName is the themed icon of the application.
const IconName = AppID

const (
	// commandTimeout bounds every tmux command run from the UI thread.
	commandTimeout = 5 * time.Second
	// minKickInterval limits extra polls triggered by terminal output.
	minKickInterval = 250 * time.Millisecond
)

//nolint:gochecknoinits
func init() {
	// GTK must only be used from the thread that initialised it. Keep main() on it.
	runtime.LockOSThread()
}

// Options are the command line settings for Run.
type Options struct {
	// Separate runs a new application instance instead of opening a window in an existing
	// one.
	Separate bool
}

// App is the running application.
type App struct {
	ctx     context.Context
	log     *zap.Logger
	cfg     Config
	gtkApp  *gtk.Application
	tracker *activity.Tracker
	grouper *sessionlist.Grouper
	// groups maps session keys to their application group.
	groups map[string]string
	// grouped is whether session lists are grouped by application.
	grouped bool
	// sidebarRight puts session lists on the right of windows.
	sidebarRight bool
	// showTabs shows the tab bar above windows' panes.
	showTabs bool
	// state is what the UI remembers between runs.
	state uiState
	// dragKey is the session being dragged from a session list, or "".
	dragKey string

	// hosts are the tmux servers listed, the local one first.
	hosts []*Host
	// pollCtx stops every host's poller when the application shuts down.
	pollCtx context.Context

	appearance  *Appearance
	terminalCSS *gtk.CssProvider
	// watched are the GSettings the appearance was read from, and their change handlers,
	// so edits in GNOME Terminal or the desktop settings show up straight away.
	watched []watchedSettings
	// prefs is the open Preferences dialog, or nil.
	prefs *preferences
	// reloadPending is set while an appearance reload is queued.
	reloadPending bool
	windows       map[*Window]struct{}
}

// Run runs the application until its last window closes or ctx is cancelled.
func Run(ctx context.Context, cfg Config, opts Options) error {
	// gotk3 releases GObject references from Go finalizers, which run on another thread.
	// Do it on the main loop instead.
	glib.FinalizerStrategy = func(f glib.Finalizer) { glib.IdleAdd(func() { f() }) }

	if cfg.Activity.PollInterval < minPollInterval {
		cfg.Activity.PollInterval = minPollInterval
	}

	flags := glib.APPLICATION_FLAGS_NONE
	if opts.Separate {
		flags = glib.APPLICATION_NON_UNIQUE
	}
	gtkApp, err := gtk.ApplicationNew(AppID, flags)
	if err != nil {
		return errors.Wrap(err, "creating GTK application")
	}

	pollCtx, stopPolling := context.WithCancel(ctx)
	defer stopPolling()

	app := &App{
		ctx:     ctx,
		log:     logutil.FromCtx(ctx),
		cfg:     cfg,
		gtkApp:  gtkApp,
		tracker: activity.NewTracker(cfg.Activity.Timeout),
		grouper: sessionlist.NewGrouper(cfg.Sidebar.GroupHold),
		groups:  map[string]string{},
		grouped: cfg.Sidebar.GroupByApplication,
		pollCtx: pollCtx,
		windows: map[*Window]struct{}{},

		sidebarRight: cfg.Sidebar.Position == "right",
		showTabs:     true,
	}
	if st, err := loadState(stateFile()); err != nil {
		app.log.Warn("Could not read UI state", zap.Error(err))
	} else {
		app.state = st
		if st.SidebarRight != nil {
			app.sidebarRight = *st.SidebarRight
		}
		if st.GroupByApplication != nil {
			app.grouped = *st.GroupByApplication
		}
		app.showTabs = !st.HideTabBar
	}

	gtkApp.Connect("startup", app.startup)
	gtkApp.Connect("activate", func() { app.NewWindow() })
	gtkApp.Connect("shutdown", stopPolling)

	go func() {
		<-ctx.Done()
		glib.IdleAdd(gtkApp.Quit)
	}()

	// GTK gets no arguments of ours: kong has already parsed them.
	if code := gtkApp.Run([]string{os.Args[0]}); code != 0 {
		return errors.Errorf("GTK application exited with status %d", code)
	}
	return nil
}

// startup runs once in the primary instance, after GTK has initialised.
func (a *App) startup() {
	glib.SetApplicationName(version.Name)
	gtk.WindowSetDefaultIconName(IconName)
	installIcons(a.log)
	installCSS(a.log)
	a.reloadAppearance()
	a.log.Debug("Terminal features", zap.Bool("sixel", vte.SixelSupported()))
	// Theme colors change with the GTK theme.
	if settings, err := gtk.SettingsGetDefault(); err == nil {
		for _, prop := range []string{"notify::gtk-theme-name", "notify::gtk-application-prefer-dark-theme"} {
			settings.Connect(prop, func() { glib.IdleAdd(a.installTerminalCSS) })
		}
	}
	a.installActions()

	// Fill the local session list before the first window opens. Remote hosts fill in as
	// their first polls come back.
	local := newHost(LocalHost, HostConfig{}, a.cfg.Tmux.Client())
	ctx, cancel := a.commandContext()
	defer cancel()
	if snap, err := local.client.Snapshot(ctx); err == nil {
		local.snapshot, local.polled = snap, true
	} else {
		a.log.Warn("Could not read tmux sessions", zap.Error(err))
	}
	a.startHost(local)

	saved, err := loadHosts(hostsFile())
	if err != nil {
		a.log.Warn("Could not read saved hosts", zap.Error(err))
	}
	// A host is reached through its origin, so origins load first: keep passing over the
	// list while hosts can still be added.
	pending := append(append([]HostConfig{}, a.cfg.Hosts...), saved...)
	for progress := true; progress && len(pending) > 0; {
		progress = false
		rest := pending[:0]
		for _, cfg := range pending {
			if cfg.Destination == "" {
				continue
			}
			h, err := a.newRemoteHost(cfg)
			if err != nil {
				rest = append(rest, cfg)
				continue
			}
			if a.host(h.Name) == nil {
				a.startHost(h)
			}
			progress = true
		}
		pending = rest
	}
	for _, cfg := range pending {
		a.log.Warn("Not listing host: its origin isn't listed", zap.String("host", cfg.Destination),
			zap.String("via", cfg.Via))
	}
	a.updateGroups()
}

// watchedSettings is a GSettings object and the handler watching it.
type watchedSettings struct {
	settings *glib.Settings
	handler  glib.SignalHandle
}

// reloadAppearance works out the terminal look again and applies it everywhere: at
// startup, when Preferences change, and when the GNOME Terminal profile or desktop font
// it came from changes.
func (a *App) reloadAppearance() {
	for _, w := range a.watched {
		w.settings.HandlerDisconnect(w.handler)
	}
	a.watched = nil

	appearance, profile := ResolveAppearance(a.cfg.Appearance, a.state.Appearance, a.log)
	a.appearance = appearance
	watch := func(s *glib.Settings) {
		// Several keys change at once when a profile is edited: reload once for them all.
		handler := s.Connect("changed", func() { a.scheduleReload() })
		a.watched = append(a.watched, watchedSettings{settings: s, handler: handler})
	}
	if profile != nil {
		watch(profile)
	}
	if hasSchema(schemaInterface) {
		watch(glib.SettingsNew(schemaInterface))
	}

	for w := range a.windows {
		for _, p := range w.panes() {
			a.appearance.Apply(p.term)
		}
	}
	a.installTerminalCSS()
}

// scheduleReload reloads the appearance soon, once however many settings change.
func (a *App) scheduleReload() {
	if a.reloadPending {
		return
	}
	a.reloadPending = true
	glib.IdleAdd(func() {
		a.reloadPending = false
		a.reloadAppearance()
	})
}

// setAppearancePrefs applies and remembers appearance choices from Preferences.
func (a *App) setAppearancePrefs(prefs AppearancePrefs) {
	a.state.Appearance = prefs
	a.saveState()
	a.reloadAppearance()
}

// findSession returns the session with a name on a host, or nil.
func (a *App) findSession(hostName, name string) *tmux.Session {
	h := a.host(hostName)
	if h == nil {
		return nil
	}
	for i := range h.snapshot.Sessions {
		if h.snapshot.Sessions[i].Name == name {
			return &h.snapshot.Sessions[i]
		}
	}
	return nil
}

// pinIndex returns the position of a pin in the pinned list, or -1.
func (a *App) pinIndex(pin PinnedSession) int {
	for i, p := range a.state.Pinned {
		if p == pin {
			return i
		}
	}
	return -1
}

// pinOf returns the pin a session would have.
func (a *App) pinOf(key string) (PinnedSession, bool) {
	h, s := a.lookup(key)
	if s == nil {
		return PinnedSession{}, false
	}
	return PinnedSession{Host: h.Name, Name: s.Name}, true
}

// setPinned pins or unpins a session.
func (a *App) setPinned(pin PinnedSession, pinned bool) {
	i := a.pinIndex(pin)
	switch {
	case pinned && i < 0:
		a.state.Pinned = append(a.state.Pinned, pin)
	case !pinned && i >= 0:
		a.state.Pinned = append(a.state.Pinned[:i], a.state.Pinned[i+1:]...)
	default:
		return
	}
	a.saveState()
	a.refreshAll()
}

// setPinsCollapsed folds the pinned sessions away under their heading, or shows them.
func (a *App) setPinsCollapsed(collapsed bool) {
	a.state.PinsCollapsed = collapsed
	a.saveState()
	a.refreshAll()
}

// setHideUnavailablePins hides or shows pinned sessions which aren't running.
func (a *App) setHideUnavailablePins(hide bool) {
	a.state.HideUnavailablePins = hide
	a.saveState()
	a.refreshAll()
}

// startHost adds a host to the list and starts polling it.
func (a *App) startHost(h *Host) {
	ctx, cancel := context.WithCancel(a.pollCtx)
	h.cancel = cancel
	a.hosts = append(a.hosts, h)
	go a.pollHost(ctx, h)
}

// host returns the host with a name, or nil.
func (a *App) host(name string) *Host {
	for _, h := range a.hosts {
		if h.Name == name {
			return h
		}
	}
	return nil
}

// lookup returns the host and session of a session key. Either is nil if it's gone.
func (a *App) lookup(key string) (*Host, *tmux.Session) {
	name, id := splitKey(key)
	h := a.host(name)
	if h == nil {
		return nil, nil
	}
	return h, h.snapshot.Session(id)
}

// lookupWindow returns the host and window of a window key, or nils if either is gone.
func (a *App) lookupWindow(key string) (*Host, *tmux.Window) {
	name, id := splitKey(key)
	h := a.host(name)
	if h == nil {
		return nil, nil
	}
	for i := range h.snapshot.Sessions {
		ws := h.snapshot.Sessions[i].Windows
		for j := range ws {
			if ws[j].ID == id {
				return h, &ws[j]
			}
		}
	}
	return nil, nil
}

// sessionName returns a session's name, with its host if it's remote.
func (a *App) sessionName(key string) string {
	h, s := a.lookup(key)
	_, id := splitKey(key)
	name := id
	if s != nil {
		name = s.Name
	}
	if h != nil && !h.Local() {
		return name + " (" + h.Name + ")"
	}
	return name
}

// AddHost lists a remote host's sessions. It checks that tmux on the host can be reached
// before adding it, and saves it for next time.
func (a *App) AddHost(destination, via string, forwardAgent bool, parent *Window) {
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return
	}
	if via == LocalHost {
		via = ""
	}
	cfg := HostConfig{Destination: destination, Via: via, ForwardAgent: forwardAgent && via != ""}
	h, err := a.newRemoteHost(cfg)
	if err != nil {
		parent.showError("Could not add "+destination, err.Error())
		return
	}
	if a.host(h.Name) != nil {
		parent.showError("Host already added", h.Name+" is already in the list.")
		return
	}
	destination = h.Name
	parent.setStatus("Connecting to " + destination + "…")
	runAsync(a, h.client.Snapshot, func(snap *tmux.Snapshot, err error) {
		parent.setStatus("")
		if err != nil {
			parent.showError("Could not reach tmux on "+destination,
				err.Error()+"\n\nThe host needs key or agent authentication for ssh, and tmux installed.")
			return
		}
		if a.host(destination) != nil {
			return
		}
		h.snapshot, h.polled = snap, true
		a.startHost(h)
		a.saveHosts()
		a.updateGroups()
		a.refreshAll()
	})
}

// RemoveHost stops listing a remote host, and the hosts reached through it. Panes
// attached to them keep running.
func (a *App) RemoveHost(name string) {
	h := a.host(name)
	if h == nil || h.Local() {
		return
	}
	remove := map[string]bool{name: true}
	for _, d := range a.dependents(name) {
		remove[d.Name] = true
	}
	kept := a.hosts[:0]
	for _, host := range a.hosts {
		if remove[host.Name] {
			host.cancel()
			continue
		}
		kept = append(kept, host)
	}
	a.hosts = kept
	a.saveHosts()
	a.updateGroups()
	a.refreshAll()
}

// saveHosts writes the remote hosts not already in the configuration file.
func (a *App) saveHosts() {
	inConfig := map[string]bool{}
	for _, cfg := range a.cfg.Hosts {
		if h, err := a.newRemoteHost(cfg); err == nil {
			inConfig[h.Name] = true
		}
	}
	saved := []HostConfig{}
	for _, h := range a.hosts {
		if !h.Local() && !inConfig[h.Name] {
			saved = append(saved, h.Config)
		}
	}
	if err := saveHosts(hostsFile(), saved); err != nil {
		a.log.Error("Could not save hosts", zap.Error(err))
	}
}

// refreshAll redraws every window.
func (a *App) refreshAll() {
	for w := range a.windows {
		w.refresh()
	}
}

// commandContext returns a context bounding one tmux command.
func (a *App) commandContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(a.ctx, commandTimeout)
}

// addWindow makes a window and tracks it until it closes.
func (a *App) addWindow() *Window {
	w := newWindow(a)
	a.windows[w] = struct{}{}
	w.window.Connect("destroy", func() {
		w.destroyed()
		delete(a.windows, w)
	})
	return w
}

// NewWindowFor opens a window showing one session, with the session list hidden, with its
// top left near x, y on the screen. A session dragged out of a window opens this way.
func (a *App) NewWindowFor(key string, x, y int) *Window {
	w := a.addWindow()
	w.setSidebarVisible(false)
	w.activePane.Show(key)
	w.refresh()
	const grabOffset = 40                                         // so the pointer lands in the title bar
	w.window.Move(max(x-grabOffset*4, 0), max(y-grabOffset/2, 0)) //nolint:mnd // a little left of the pointer
	w.window.Present()
	w.activePane.Focus()
	return w
}

// NewWindow opens a window showing the most recently active session.
func (a *App) NewWindow() *Window {
	w := a.addWindow()

	if id := a.pickSession(nil); id != "" {
		w.activePane.Show(id)
	} else if a.cfg.Behaviour.CreateSessionOnStart {
		w.NewSession(w.activePane)
	}
	w.refresh()
	w.window.Present()
	return w
}

// pickSession returns the key of the local session with the latest activity, preferring
// ones not in exclude. It returns "" if there are none.
func (a *App) pickSession(exclude map[string]bool) string {
	local := a.host(LocalHost)
	if local == nil {
		return ""
	}
	best, bestHidden := "", ""
	var bestTime, bestHiddenTime time.Time
	for _, s := range local.snapshot.Sessions {
		key, act := sessionKey(LocalHost, s.ID), s.Activity()
		if best == "" || act.After(bestTime) {
			best, bestTime = key, act
		}
		if !exclude[key] && (bestHidden == "" || act.After(bestHiddenTime)) {
			bestHidden, bestHiddenTime = key, act
		}
	}
	if bestHidden != "" {
		return bestHidden
	}
	return best
}

// requestPoll asks a host for a snapshot soon, as when a terminal shows new output.
func (a *App) requestPoll(hostName string) {
	if h := a.host(hostName); h != nil {
		h.requestPoll()
	}
}

// visibleSessions returns the keys of the sessions shown in any pane of any window.
func (a *App) visibleSessions() map[string]bool {
	visible := map[string]bool{}
	for w := range a.windows {
		for _, p := range w.panes() {
			if p.Running() {
				visible[p.SessionKey()] = true
			}
		}
	}
	return visible
}

// ownTTYs returns the ttys of this application's local tmux clients.
func (a *App) ownTTYs() map[string]bool {
	ttys := map[string]bool{}
	for w := range a.windows {
		for _, p := range w.panes() {
			if p.tty != "" && p.hostName == LocalHost {
				ttys[p.tty] = true
			}
		}
	}
	return ttys
}

// ownClients counts this application's panes attached to each session.
func (a *App) ownClients() map[string]int {
	counts := map[string]int{}
	for w := range a.windows {
		for _, p := range w.panes() {
			if p.Running() {
				counts[p.SessionKey()]++
			}
		}
	}
	return counts
}

// applySnapshot records a host's new state, or why it couldn't be read, and updates
// every window.
func (a *App) applySnapshot(h *Host, snap *tmux.Snapshot, err error) {
	if a.host(h.Name) != h {
		// The host was removed while the poll ran.
		return
	}
	h.polled, h.err = true, err
	if err != nil {
		// The sessions can't be reached, so don't offer them.
		snap = &tmux.Snapshot{}
	}
	h.snapshot = snap

	// A local client may have switched session inside tmux.
	if h.Local() {
		for w := range a.windows {
			for _, p := range w.panes() {
				p.syncSession(snap)
			}
		}
	}

	visible := a.visibleSessions()
	now := time.Now()
	keys := map[string]bool{}
	for _, host := range a.hosts {
		for i := range host.snapshot.Sessions {
			s := &host.snapshot.Sessions[i]
			key := sessionKey(host.Name, s.ID)
			keys[key] = true
			if host == h {
				a.tracker.Observe(key, s.Activity(), visible[key], now)
			}
			// Windows for the tab bar: one is on screen if it's the current window of a
			// session in a pane.
			for _, win := range s.Windows {
				wkey := windowKey(host.Name, win.ID)
				keys[wkey] = true
				if host == h {
					a.tracker.Observe(wkey, win.Activity, visible[key] && win.Active, now)
				}
			}
		}
	}
	a.tracker.Retain(keys)
	a.updateGroups()
	a.refreshAll()
}

// updateGroups assigns every session to its application group.
func (a *App) updateGroups() {
	now := time.Now()
	groups := map[string]string{}
	keys := map[string]bool{}
	for _, h := range a.hosts {
		for i := range h.snapshot.Sessions {
			s := &h.snapshot.Sessions[i]
			key := sessionKey(h.Name, s.ID)
			// The grouper remembers sessions by ID: give it the key instead.
			keyed := *s
			keyed.ID = key
			groups[key] = a.grouper.Group(&keyed, now)
			keys[key] = true
		}
	}
	a.grouper.Retain(keys)
	a.groups = groups
}

// setGrouped turns grouping by application on or off in every window.
func (a *App) setGrouped(grouped bool) {
	a.grouped = grouped
	a.state.GroupByApplication = &grouped
	a.saveState()
	for w := range a.windows {
		w.syncGroupAction()
		w.refresh()
	}
}

// setShowTabs shows or hides the tab bar of every window.
func (a *App) setShowTabs(show bool) {
	a.showTabs = show
	a.state.HideTabBar = !show
	a.saveState()
	for w := range a.windows {
		w.syncTabsAction()
	}
}

// setSidebarRight moves the session list of every window to the right or left.
func (a *App) setSidebarRight(right bool) {
	a.sidebarRight = right
	a.state.SidebarRight = &right
	a.saveState()
	for w := range a.windows {
		w.placeSidebar()
	}
}

// saveState writes the UI state.
func (a *App) saveState() {
	if err := saveState(stateFile(), a.state); err != nil {
		a.log.Warn("Could not save UI state", zap.Error(err))
	}
}

// endDrag clears a finished session drag and its drop highlights.
func (a *App) endDrag() {
	a.dragKey = ""
	for w := range a.windows {
		for _, p := range w.panes() {
			p.hideDropZone()
		}
	}
}

// activeWindow returns the focused window of the application, or any window.
func (a *App) activeWindow() *Window {
	if active := a.gtkApp.GetActiveWindow(); active != nil {
		for w := range a.windows {
			if w.window.Window.Native() == active.Native() {
				return w
			}
		}
	}
	for w := range a.windows {
		return w
	}
	return nil
}

// installActions adds the application-wide actions and every keyboard shortcut.
func (a *App) installActions() {
	addAction(a.gtkApp.IActionMap, "new-window", func() { a.NewWindow() })
	addAction(a.gtkApp.IActionMap, "about", func() { a.showAbout() })
	addAction(a.gtkApp.IActionMap, "preferences", func() { a.showPreferences() })
	addAction(a.gtkApp.IActionMap, "quit", func() {
		for w := range a.windows {
			w.window.Destroy()
		}
	})

	for action, accels := range accelerators {
		a.gtkApp.SetAccelsForAction(action, accels)
	}
}

// showAbout shows the about dialog.
func (a *App) showAbout() {
	dlg, err := gtk.AboutDialogNew()
	if err != nil {
		return
	}
	dlg.SetProgramName(version.Name)
	dlg.SetVersion(version.Version)
	dlg.SetComments(version.Description)
	dlg.SetLogoIconName(IconName)
	dlg.SetWebsite("https://github.com/wrouesnel/tmux-tabbed-terminal")
	dlg.SetLicenseType(gtk.LICENSE_MIT_X11)
	if w := a.activeWindow(); w != nil {
		dlg.SetTransientFor(w.window)
	}
	dlg.Connect("response", func() { dlg.Destroy() })
	dlg.Show()
}

// addAction adds a parameterless action to a map.
func addAction(m glib.IActionMap, name string, fn func()) *glib.SimpleAction {
	action := glib.SimpleActionNew(name, nil)
	action.Connect("activate", func() { fn() })
	m.AddAction(action)
	return action
}

// addStringAction adds an action taking a string parameter, such as a session ID.
func addStringAction(m glib.IActionMap, name string, fn func(string)) *glib.SimpleAction {
	action := glib.SimpleActionNew(name, glib.VARIANT_TYPE_STRING)
	action.Connect("activate", func(_ interface{}, param string) { fn(param) })
	m.AddAction(action)
	return action
}

// eventHasControl reports whether a button event has Control held.
func eventHasControl(ev *gdk.EventButton) bool {
	return ev.State()&uint(gdk.CONTROL_MASK) != 0
}

// newTerminal creates a terminal widget with the application's appearance.
func (a *App) newTerminal() *vte.Terminal {
	term := vte.New()
	// SIXEL images show wherever this VTE was built with them; elsewhere it's off.
	if vte.SixelSupported() {
		term.SetEnableSixel(true)
	}
	a.appearance.Apply(term)
	term.Connect("style-updated", func() {
		if a.appearance.UseThemeColors {
			a.appearance.Apply(term)
		}
	})
	return term
}
