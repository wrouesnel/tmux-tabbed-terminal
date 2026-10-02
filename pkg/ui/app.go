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
	client  *tmux.Client
	gtkApp  *gtk.Application
	tracker *activity.Tracker
	grouper *sessionlist.Grouper
	// groups maps session IDs to their application group.
	groups map[string]string
	// grouped is whether session lists are grouped by application.
	grouped bool

	appearance  *Appearance
	terminalCSS *gtk.CssProvider
	snapshot    *tmux.Snapshot
	windows     map[*Window]struct{}

	kick chan struct{}
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

	app := &App{
		ctx:      ctx,
		log:      logutil.FromCtx(ctx),
		cfg:      cfg,
		client:   cfg.Tmux.Client(),
		gtkApp:   gtkApp,
		tracker:  activity.NewTracker(cfg.Activity.Timeout),
		grouper:  sessionlist.NewGrouper(cfg.Sidebar.GroupHold),
		groups:   map[string]string{},
		grouped:  cfg.Sidebar.GroupByApplication,
		snapshot: &tmux.Snapshot{},
		windows:  map[*Window]struct{}{},
		kick:     make(chan struct{}, 1),
	}

	pollCtx, stopPolling := context.WithCancel(ctx)
	defer stopPolling()

	gtkApp.Connect("startup", func() {
		app.startup()
		go app.pollLoop(pollCtx)
	})
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
	a.appearance = ResolveAppearance(a.cfg.Appearance, a.log)
	installCSS(a.log)
	a.installTerminalCSS()
	// Theme colors change with the GTK theme.
	if settings, err := gtk.SettingsGetDefault(); err == nil {
		for _, prop := range []string{"notify::gtk-theme-name", "notify::gtk-application-prefer-dark-theme"} {
			settings.Connect(prop, func() { glib.IdleAdd(a.installTerminalCSS) })
		}
	}
	a.installActions()

	// Fill the session list before the first window opens.
	ctx, cancel := a.commandContext()
	defer cancel()
	if snap, err := a.client.Snapshot(ctx); err == nil {
		a.snapshot = snap
		a.updateGroups()
	} else {
		a.log.Warn("Could not read tmux sessions", zap.Error(err))
	}
}

// commandContext returns a context bounding one tmux command.
func (a *App) commandContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(a.ctx, commandTimeout)
}

// NewWindow opens a window showing the most recently active session.
func (a *App) NewWindow() *Window {
	w := newWindow(a)
	a.windows[w] = struct{}{}
	w.window.Connect("destroy", func() {
		w.destroyed()
		delete(a.windows, w)
	})

	if id := a.pickSession(nil); id != "" {
		w.activePane.Show(id)
	} else if a.cfg.Behaviour.CreateSessionOnStart {
		w.NewSession(w.activePane)
	}
	w.refresh()
	w.window.Present()
	return w
}

// pickSession returns the session with the latest activity, preferring ones not in
// exclude. It returns "" if there are no sessions.
func (a *App) pickSession(exclude map[string]bool) string {
	best, bestHidden := "", ""
	var bestTime, bestHiddenTime time.Time
	for _, s := range a.snapshot.Sessions {
		act := s.Activity()
		if best == "" || act.After(bestTime) {
			best, bestTime = s.ID, act
		}
		if !exclude[s.ID] && (bestHidden == "" || act.After(bestHiddenTime)) {
			bestHidden, bestHiddenTime = s.ID, act
		}
	}
	if bestHidden != "" {
		return bestHidden
	}
	return best
}

// requestPoll asks for a snapshot soon, as when a terminal shows new output.
func (a *App) requestPoll() {
	select {
	case a.kick <- struct{}{}:
	default:
	}
}

// pollLoop reads the tmux state at the poll interval, and when asked to, and hands it to
// the main loop.
func (a *App) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(a.cfg.Activity.PollInterval)
	defer ticker.Stop()
	var last time.Time
	var lastErr string

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-a.kick:
			if wait := minKickInterval - time.Since(last); wait > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(wait):
				}
			}
		}
		last = time.Now()

		cmdCtx, cancel := context.WithTimeout(ctx, commandTimeout)
		snap, err := a.client.Snapshot(cmdCtx)
		cancel()
		if err != nil {
			// Log a persistent error once, not on every poll.
			if err.Error() != lastErr && ctx.Err() == nil {
				a.log.Warn("Could not read tmux sessions", zap.Error(err))
			}
			lastErr = err.Error()
			continue
		}
		lastErr = ""
		glib.IdleAdd(func() { a.applySnapshot(snap) })
	}
}

// visibleSessions returns the sessions shown in any pane of any window.
func (a *App) visibleSessions() map[string]bool {
	visible := map[string]bool{}
	for w := range a.windows {
		for _, p := range w.panes() {
			if p.Running() {
				visible[p.SessionID()] = true
			}
		}
	}
	return visible
}

// ownTTYs returns the ttys of this application's tmux clients.
func (a *App) ownTTYs() map[string]bool {
	ttys := map[string]bool{}
	for w := range a.windows {
		for _, p := range w.panes() {
			if p.tty != "" {
				ttys[p.tty] = true
			}
		}
	}
	return ttys
}

// applySnapshot updates every window from new tmux state.
func (a *App) applySnapshot(snap *tmux.Snapshot) {
	a.snapshot = snap

	// A client may have switched session inside tmux.
	for w := range a.windows {
		for _, p := range w.panes() {
			p.syncSession(snap)
		}
	}

	visible := a.visibleSessions()
	now := time.Now()
	ids := map[string]bool{}
	for i := range snap.Sessions {
		s := &snap.Sessions[i]
		ids[s.ID] = true
		a.tracker.Observe(s.ID, s.Activity(), visible[s.ID], now)
	}
	a.tracker.Retain(ids)
	a.updateGroups()

	for w := range a.windows {
		w.refresh()
	}
}

// updateGroups assigns each session in the snapshot to its application group.
func (a *App) updateGroups() {
	now := time.Now()
	groups := make(map[string]string, len(a.snapshot.Sessions))
	ids := map[string]bool{}
	for i := range a.snapshot.Sessions {
		s := &a.snapshot.Sessions[i]
		groups[s.ID] = a.grouper.Group(s, now)
		ids[s.ID] = true
	}
	a.grouper.Retain(ids)
	a.groups = groups
}

// setGrouped turns grouping by application on or off in every window.
func (a *App) setGrouped(grouped bool) {
	a.grouped = grouped
	for w := range a.windows {
		w.syncGroupAction()
		w.refresh()
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
	a.appearance.Apply(term)
	term.Connect("style-updated", func() {
		if a.appearance.UseThemeColors {
			a.appearance.Apply(term)
		}
	})
	return term
}
