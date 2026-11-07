package ui

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"go.uber.org/zap"

	"github.com/wrouesnel/tmux-tabbed-terminal/version"
)

const (
	// updateCheckInterval is how often the application looks for a new version of itself
	// installed over the running one.
	updateCheckInterval = time.Minute
	// restoreEnv carries the windows open before a restart to the instance replacing it.
	restoreEnv = "TTT_RESTORE"
)

// installWatch notices the program's file being replaced, as a package upgrade does,
// while it runs.
type installWatch struct {
	path string
	info os.FileInfo
}

// newInstallWatch watches the running program's file. It returns nil if the file can't be
// found.
func newInstallWatch() *installWatch {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	info, err := os.Stat(exe)
	if err != nil {
		return nil
	}
	return &installWatch{path: exe, info: info}
}

// replaced returns the file's details if it isn't the file the program started from.
func (iw *installWatch) replaced() (os.FileInfo, bool) {
	info, err := os.Stat(iw.path)
	if err != nil || os.SameFile(info, iw.info) && info.ModTime().Equal(iw.info.ModTime()) &&
		info.Size() == iw.info.Size() {
		return nil, false
	}
	return info, true
}

// watchInstall starts looking for a new version installed over this one.
func (a *App) watchInstall() {
	if a.install = newInstallWatch(); a.install == nil {
		return
	}
	glib.TimeoutAdd(uint(updateCheckInterval.Milliseconds()), func() bool {
		a.checkInstall()
		return true
	})
}

// checkInstall asks a replaced program file its version, and offers a restart if it's
// another one.
func (a *App) checkInstall() {
	if a.install == nil || a.checkingInstall {
		return
	}
	info, ok := a.install.replaced()
	if !ok {
		return
	}
	a.checkingInstall = true
	path := a.install.path
	runAsync(a, func(ctx context.Context) (string, error) {
		out, err := exec.CommandContext(ctx, path, "--version").Output()
		return strings.TrimSpace(string(out)), err
	}, func(installed string, err error) {
		a.checkingInstall = false
		if err != nil {
			// Perhaps halfway through being installed: look again next time.
			a.log.Debug("Could not read the installed version", zap.Error(err))
			return
		}
		a.install.info = info
		if installed == "" || installed == version.Version || installed == a.updateVersion {
			return
		}
		a.log.Info("A new version is installed", zap.String("installed", installed),
			zap.String("running", version.Version))
		a.updateVersion = installed
		for w := range a.windows {
			w.showUpdate()
		}
	})
}

// restoredWindow is a window open before a restart, to open again after it.
type restoredWindow struct {
	Session   string `json:"session,omitempty"`
	Sidebar   bool   `json:"sidebar"`
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Maximized bool   `json:"maximized,omitempty"`
}

// restart closes every window and starts the installed version, which opens them again
// on the same sessions. The sessions themselves carry on in tmux.
func (a *App) restart() {
	a.restore = a.restore[:0]
	for w := range a.windows {
		p := w.terminalPane
		if p == nil || p.closed {
			p = w.activePane
		}
		r := restoredWindow{Sidebar: w.sidebar.root.GetVisible(), Maximized: w.window.IsMaximized()}
		if p.running {
			r.Session = p.SessionKey()
		}
		r.X, r.Y = w.window.GetPosition()
		r.Width, r.Height = w.window.GetSize()
		a.restore = append(a.restore, r)
	}
	a.restarting = true
	for w := range a.windows {
		w.window.Destroy()
	}
}

// relaunchEnv is the environment for the instance started by a restart, with the windows
// to open again.
func (a *App) relaunchEnv() []string {
	env := os.Environ()
	if data, err := json.Marshal(a.restore); err == nil {
		env = append(env, restoreEnv+"="+string(data))
	}
	return env
}

// takeRestore reads the windows a restart left to open again, and keeps the shells
// started from here from seeing them.
func (a *App) takeRestore() {
	data := os.Getenv(restoreEnv)
	if data == "" {
		return
	}
	_ = os.Unsetenv(restoreEnv)
	if err := json.Unmarshal([]byte(data), &a.restore); err != nil {
		a.log.Warn("Could not read the windows to open again", zap.Error(err))
	}
}

// restoreWindows opens the windows a restart left to open again. It returns false if
// there were none.
func (a *App) restoreWindows() bool {
	if len(a.restore) == 0 {
		return false
	}
	for _, r := range a.restore {
		w := a.addWindow()
		w.setSidebarVisible(r.Sidebar)
		if r.Session != "" {
			w.activePane.Show(r.Session)
		}
		w.refresh()
		if r.Width > 0 && r.Height > 0 {
			w.window.Resize(r.Width, r.Height)
		}
		w.window.Move(r.X, r.Y)
		if r.Maximized {
			w.window.Maximize()
		}
		w.window.Present()
	}
	a.restore = nil
	return true
}

// newUpdateButton makes the title bar button offering a restart into a new version. It
// shows once one is installed.
func (w *Window) newUpdateButton() *gtk.Button {
	b, _ := gtk.ButtonNewWithLabel("Restart to Update")
	addClass(b, "suggested-action")
	b.SetNoShowAll(true)
	b.Connect("clicked", func() { glib.IdleAdd(w.app.restart) })
	return b
}

// showUpdate shows the restart button, if a new version is installed.
func (w *Window) showUpdate() {
	if w.app.updateVersion == "" {
		return
	}
	w.update.SetTooltipText("Version " + w.app.updateVersion + " is installed: restart to use it. " +
		"Your sessions carry on, and the windows open again on them.")
	w.update.Show()
}
