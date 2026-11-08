package ui

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"go.uber.org/zap"
)

// parseURIList returns the URIs of text/uri-list data: one a line, with lines starting
// with # comments.
func parseURIList(data []byte) []string {
	var uris []string
	for line := range strings.Lines(string(data)) {
		// Some sources end the data with a NUL.
		line = strings.TrimSpace(strings.Trim(line, "\x00"))
		if line != "" && !strings.HasPrefix(line, "#") {
			uris = append(uris, line)
		}
	}
	return uris
}

// localFiles returns the paths of the local files among dropped URIs.
func localFiles(uris []string) []string {
	var files []string
	for _, uri := range uris {
		u, err := url.Parse(uri)
		if err != nil || u.Scheme != "file" || (u.Host != "" && u.Host != "localhost") || u.Path == "" {
			continue
		}
		p := u.Path
		// file:///C:/dir/file on Windows.
		if runtime.GOOS == "windows" && len(p) > 2 && p[0] == '/' && p[2] == ':' {
			p = p[1:]
		}
		files = append(files, filepath.FromSlash(p))
	}
	return files
}

// takesUploads reports whether files dropped on the pane can be uploaded to its session's
// host.
func (p *Pane) takesUploads() bool {
	if !p.running || p.closed {
		return false
	}
	h, _ := p.win.app.lookup(p.SessionKey())
	return h != nil && (h.Name == LocalHost || h.client.SSH != nil)
}

// uploadFiles copies files dropped at x, y on the pane to the working directory of the
// tmux pane there, on its host, and puts their new paths on the clipboard, to paste into
// whatever runs there.
func (p *Pane) uploadFiles(files []string, x, y int) {
	h, s := p.win.app.lookup(p.SessionKey())
	if h == nil || s == nil {
		return
	}
	aw := s.ActiveWindow()
	if aw == nil || aw.Panes <= 1 {
		dir := ""
		if aw != nil {
			dir = aw.Path
		}
		p.win.uploadFiles(h, files, dir)
		return
	}
	// The window is split by tmux: the directory is that of the tmux pane under the drop.
	col, row := p.term.CellAt(float64(x), float64(y))
	window, dir := aw.ID, aw.Path
	runAsync(p.win.app, func(ctx context.Context) (string, error) {
		layout, err := h.client.WindowLayout(ctx, window)
		if err != nil {
			return "", err
		}
		if pane, _, _ := layout.PaneAt(col, row); pane != nil {
			return pane.Path, nil
		}
		return "", nil
	}, func(path string, err error) {
		if err != nil {
			p.win.app.log.Debug("Could not read the window's panes: uploading to its active one's directory",
				zap.Error(err))
		}
		if path != "" {
			dir = path
		}
		p.win.uploadFiles(h, files, dir)
	})
}

// uploadBatch is files dropped together, whose paths go on the clipboard together once
// they've all finished.
type uploadBatch struct {
	host  string
	dir   string
	left  int
	saved []string
}

// uploadFiles copies files to dir on a host, or to the home directory if dir is "".
func (w *Window) uploadFiles(h *Host, files []string, dir string) {
	if dir == "" {
		dir = "~"
	}
	batch := &uploadBatch{host: h.Name, dir: dir, left: len(files)}
	for _, file := range files {
		w.uploadFile(h, file, batch)
	}
}

// uploadFile copies one of a batch of files to the batch's host and directory, showing
// its progress with the downloads.
func (w *Window) uploadFile(h *Host, file string, batch *uploadBatch) {
	name := filepath.Base(file)
	log := w.app.log.With(zap.String("host", h.Name), zap.String("file", file), zap.String("dir", batch.dir))
	ctx, cancel := context.WithCancel(w.app.ctx)
	d := newDownload(name, h.Name, cancel)
	d.upload = true
	list := w.app.downloadList
	list.add(d)
	go func() {
		saved, err := func() (string, error) {
			f, err := os.Open(file)
			if err != nil {
				return "", err
			}
			defer f.Close()
			info, err := f.Stat()
			if err != nil {
				return "", err
			}
			if !info.Mode().IsRegular() {
				return "", fmt.Errorf("%s isn't a file", file)
			}
			d.total.Store(info.Size())
			r := countingReader{r: f, n: &d.received}
			if h.Name == LocalHost {
				return saveUpload(r, batch.dir, name)
			}
			return h.client.SSH.Upload(ctx, r, batch.dir, name)
		}()
		glib.IdleAdd(func() {
			cancel()
			defer list.changed()
			switch {
			case d.cancelled:
				d.state = downloadCancelled
				log.Info("Upload cancelled")
			case err != nil:
				d.state, d.err = downloadFailed, err
				log.Error("Could not upload", zap.Error(err))
				w.showError("Could not upload "+name, err.Error())
			default:
				d.state, d.path = downloadDone, saved
				log.Info("Uploaded", zap.String("to", saved))
				batch.saved = append(batch.saved, saved)
			}
			if batch.left--; batch.left == 0 {
				w.uploaded(batch)
			}
		})
	}()
}

// uploaded puts the paths of a batch's files on the clipboard once they've all finished,
// and says so.
func (w *Window) uploaded(batch *uploadBatch) {
	if len(batch.saved) == 0 {
		return
	}
	quoted := make([]string, len(batch.saved))
	for i, p := range batch.saved {
		quoted[i] = shellQuote(p)
	}
	setClipboard(strings.Join(quoted, " "))
	where := filepath.Dir(batch.saved[0])
	if batch.host != LocalHost {
		where = batch.host + ":" + strings.TrimSuffix(batch.saved[0], "/"+uploadedName(batch.saved[0]))
	}
	what, copied := uploadedName(batch.saved[0]), "its path is"
	if len(batch.saved) > 1 {
		what, copied = fmt.Sprintf("%d files", len(batch.saved)), "their paths are"
	}
	w.downloads.showMessage(fmt.Sprintf("Uploaded %s to %s: %s on the clipboard", what, where, copied),
		strings.Join(batch.saved, "\n"), false)
}

// uploadedName is the last element of a path on a host.
func uploadedName(p string) string {
	return p[strings.LastIndex(p, "/")+1:]
}

// saveUpload saves what r reads as a new file in dir on this computer, named as
// SSH.Upload names it on another host, and returns its path.
func saveUpload(r io.Reader, dir, name string) (string, error) {
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
	}
	tmp, err := os.CreateTemp(dir, "."+name+".*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	_, copyErr := io.Copy(tmp, r)
	if err := tmp.Close(); copyErr == nil {
		copyErr = err
	}
	if copyErr != nil {
		return "", copyErr
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil { //nolint:gosec,mnd // as a new file usually is
		return "", err
	}
	dest := uploadPath(dir, name)
	return dest, os.Rename(tmp.Name(), dest)
}

// uploadPath returns a path in dir for a file called name which no file has yet: name, or
// name with -2, -3 and so on before its extension.
func uploadPath(dir, name string) string {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if stem == "" {
		stem, ext = name, ""
	}
	p := filepath.Join(dir, name)
	for i := 2; ; i++ {
		if _, err := os.Lstat(p); os.IsNotExist(err) {
			return p
		}
		p = filepath.Join(dir, fmt.Sprintf("%s-%d%s", stem, i, ext))
	}
}

// shellQuote quotes a path for a shell, if it needs it, so pasted it's one word.
func shellQuote(p string) string {
	if p != "" && strings.Trim(p, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-./~+:@%,=") == "" {
		return p
	}
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}

// setClipboard puts text on the desktop clipboard.
func setClipboard(text string) {
	if clip, err := gtk.ClipboardGet(gdk.SELECTION_CLIPBOARD); err == nil {
		clip.SetText(text)
	}
}

// countingReader adds what's read through it to n.
type countingReader struct {
	r io.Reader
	n *atomic.Int64
}

func (c countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}
