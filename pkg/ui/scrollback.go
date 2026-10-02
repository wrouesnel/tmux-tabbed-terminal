package ui

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"go.uber.org/zap"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/gtkx"
	"github.com/wrouesnel/tmux-tabbed-terminal/version"
)

// statusTimeout is how long a message stays in the title bar.
const statusTimeout = 6 * time.Second

// unsafeFileChars are characters left out of file names made from host and session names.
var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9._@+-]+`)

// fileSafe makes a host or session name into a file name.
func fileSafe(name string) string {
	safe := unsafeFileChars.ReplaceAllString(name, "_")
	if safe == "" || safe == "." || safe == ".." {
		return "_"
	}
	return safe
}

// scrollbackDir is where saved scrollback goes by default.
func scrollbackDir() string {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, version.Name, "scrollback")
}

// scrollbackPath is where Save puts a session's scrollback: by host, session and time.
func scrollbackPath(host, session string, at time.Time) string {
	return filepath.Join(scrollbackDir(), fileSafe(host), fileSafe(session),
		at.Format("2006-01-02_15-04-05")+".txt")
}

// SaveScrollback saves the history of a session's current pane to a file: under the data
// directory by host, session and time, or where the user chooses if choose is set.
func (w *Window) SaveScrollback(key string, choose bool) {
	h, s := w.app.lookup(key)
	if s == nil {
		return
	}
	name, id, now := s.Name, s.ID, time.Now()
	path := scrollbackPath(h.Name, name, now)
	if choose {
		dlg, err := gtk.FileChooserNativeDialogNew("Save Scrollback", w.window, gtk.FILE_CHOOSER_ACTION_SAVE,
			"_Save", "_Cancel")
		if err != nil {
			return
		}
		dlg.SetDoOverwriteConfirmation(true)
		dlg.SetCurrentName(fileSafe(name) + "_" + now.Format("2006-01-02_15-04-05") + ".txt")
		if home, err := os.UserHomeDir(); err == nil {
			dlg.SetCurrentFolder(home)
		}
		response := dlg.Run()
		path = dlg.GetFilename()
		dlg.Destroy()
		if response != int(gtk.RESPONSE_ACCEPT) || path == "" {
			return
		}
	}

	runAsync(w.app, func(ctx context.Context) (string, error) {
		history, err := h.client.CaptureHistory(ctx, id)
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil { //nolint:mnd // owner only
			return "", err
		}
		return path, os.WriteFile(path, []byte(history), 0o600) //nolint:mnd // owner only
	}, func(path string, err error) {
		if err != nil {
			w.app.log.Error("Could not save scrollback", zap.String("session", name), zap.Error(err))
			w.showError("Could not save the scrollback of "+name, err.Error())
			return
		}
		w.flashStatus("Saved scrollback to " + path)
	})
}

// OpenScrollbackDir opens the folder of a session's saved scrollback in the file manager,
// or the folder of all saved scrollback if the session has none.
func (w *Window) OpenScrollbackDir(key string) {
	dir := scrollbackDir()
	if h, s := w.app.lookup(key); s != nil {
		if sessionDir := filepath.Join(dir, fileSafe(h.Name), fileSafe(s.Name)); isDir(sessionDir) {
			dir = sessionDir
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil { //nolint:mnd // owner only
		w.showError("Could not create the scrollback folder", err.Error())
		return
	}
	uri := (&url.URL{Scheme: "file", Path: dir}).String()
	if err := gtkx.ShowURI(&w.window.Window, uri); err != nil {
		w.showError("Could not open the scrollback folder", err.Error())
	}
}

// isDir reports whether a path is a directory.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// flashStatus shows a message in the title bar for a few seconds.
func (w *Window) flashStatus(msg string) {
	w.setStatus(msg)
	glib.TimeoutAdd(uint(statusTimeout.Milliseconds()), func() bool {
		if w.status == msg {
			w.setStatus("")
		}
		return false
	})
}
