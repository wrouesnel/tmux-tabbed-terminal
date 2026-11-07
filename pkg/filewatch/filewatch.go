// Package filewatch tells when a file changes, with the system's own notifications:
// inotify on Linux, kqueue on macOS and ReadDirectoryChangesW on Windows.
package filewatch

import (
	"context"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"github.com/pkg/errors"
)

// changes are the events which change a file's contents, or which file is at its path.
const changes = fsnotify.Write | fsnotify.Create | fsnotify.Rename | fsnotify.Remove

// Watch calls changed whenever the file at path is written, made, replaced or removed,
// until ctx is done or watching fails. It calls started first, once it's watching. It
// watches the file's directory, so a file replaced by renaming another over it, as
// editors and many programs save, is followed, and so is one that doesn't exist yet. It
// returns nil once ctx is done.
func Watch(ctx context.Context, path string, started, changed func()) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return errors.Wrap(err, "watching for changes")
	}
	defer w.Close()
	if err := w.Add(filepath.Dir(path)); err != nil {
		return errors.Wrapf(err, "watching %s", filepath.Dir(path))
	}
	started()
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				return errors.New("the watch ended")
			}
			if ev.Op&changes != 0 && filepath.Clean(ev.Name) == path {
				changed()
			}
		case err, ok := <-w.Errors:
			if !ok {
				return errors.New("the watch ended")
			}
			return errors.Wrapf(err, "watching %s", path)
		}
	}
}
