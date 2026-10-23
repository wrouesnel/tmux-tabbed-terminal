package ui

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/gotk3/gotk3/pango"
	"go.uber.org/zap"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/gtkx"
)

// downloadBarTimeout is how long the toast telling of a finished download stays.
const downloadBarTimeout = 15 * time.Second

// toastMargin is the space below the download toast, which keeps tmux's status line in
// view, and toastMaxChars the most of its message it shows.
const (
	toastMargin   = 36
	toastMaxChars = 60
)

// Responses of the download toast's buttons.
const (
	responseOpen gtk.ResponseType = iota + 1
	responseShowFolder
)

// downloadsDir is where downloads go: the desktop's Downloads folder, or ~/Downloads.
func downloadsDir() string {
	if dir, err := glib.GetUserSpecialDir(glib.USER_DIRECTORY_DOWNLOAD); err == nil && dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, "Downloads")
}

// freePath returns a path in dir for a file called name which no file has yet: name, or
// name with " (2)", " (3)" and so on before its extension.
func freePath(dir, name string) string {
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
		p = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
	}
}

// downloadName is the name a downloaded file is saved under: the last element of its
// path on the host, which can't be a separator or a relative directory.
func downloadName(remotePath string) string {
	name := path.Base(remotePath)
	if name == "." || name == ".." || name == "/" || name == "" {
		return "download"
	}
	return name
}

// downloadLink copies the file a link on another host names into the Downloads folder,
// through the host's ssh connection, and says so in the download toast. Only regular files
// are copied.
func (w *Window) downloadLink(l link) {
	w.downloadLinkTo(l, "")
}

// downloadLinkAs asks where to save the file a link on another host names, starting in
// the Downloads folder with its name, and downloads it there.
func (w *Window) downloadLinkAs(l link) {
	if !l.downloadable() {
		return
	}
	dlg, err := gtk.FileChooserNativeDialogNew("Save As", w.window, gtk.FILE_CHOOSER_ACTION_SAVE,
		"_Save", "_Cancel")
	if err != nil {
		return
	}
	dlg.SetDoOverwriteConfirmation(true)
	dlg.SetCurrentName(downloadName(l.file))
	dir := downloadsDir()
	if !isDir(dir) {
		dir, _ = os.UserHomeDir()
	}
	dlg.SetCurrentFolder(dir)
	response := dlg.Run()
	dest := dlg.GetFilename()
	dlg.Destroy()
	if response != int(gtk.RESPONSE_ACCEPT) || dest == "" {
		return
	}
	w.downloadLinkTo(l, dest)
}

// downloadLinkTo downloads the file a link on another host names to dest, replacing what's
// there, or into the Downloads folder under a name no file has yet if dest is empty.
func (w *Window) downloadLinkTo(l link, dest string) {
	h := w.app.host(l.host)
	if !l.downloadable() || h == nil || h.client.SSH == nil {
		return
	}
	name, dir := downloadName(l.file), downloadsDir()
	if dest != "" {
		name, dir = filepath.Base(dest), filepath.Dir(dest)
	}
	log := w.app.log.With(zap.String("host", l.host), zap.String("file", l.file))
	w.setStatus("Downloading " + name + "…")

	// Downloads can take longer than other commands, so they run until the application
	// stops rather than for the usual command timeout.
	ctx := w.app.ctx
	go func() {
		saved, err := func() (string, error) {
			if err := os.MkdirAll(dir, 0o700); err != nil { //nolint:mnd // owner only
				return "", err
			}
			// The file is written under a hidden name, and renamed once it's complete.
			tmp, err := os.CreateTemp(dir, "."+name+".*.part")
			if err != nil {
				return "", err
			}
			defer os.Remove(tmp.Name())
			copyErr := h.client.SSH.CopyFile(ctx, tmp, l.file)
			if err := tmp.Close(); copyErr == nil {
				copyErr = err
			}
			if copyErr != nil {
				return "", copyErr
			}
			to := dest
			if to == "" {
				to = freePath(dir, name)
			}
			return to, os.Rename(tmp.Name(), to)
		}()
		glib.IdleAdd(func() {
			w.setStatus("")
			if err != nil {
				log.Error("Could not download", zap.Error(err))
				w.showError("Could not download "+name, err.Error())
				return
			}
			log.Info("Downloaded", zap.String("to", saved))
			w.downloads.show(saved)
		})
	}()
}

// downloadBar is a toast telling of the last finished download, with buttons to open it
// or its folder. It hides itself after a while.
type downloadBar struct {
	w     *Window
	bar   *gtk.InfoBar
	label *gtk.Label
	path  string
	timer glib.SourceHandle
}

func newDownloadBar(w *Window) *downloadBar {
	d := &downloadBar{w: w}
	d.bar, _ = gtk.InfoBarNew()
	d.bar.SetMessageType(gtk.MESSAGE_INFO)
	d.bar.SetShowCloseButton(true)
	d.bar.AddButton("_Open", responseOpen)
	d.bar.AddButton("Show in _Folder", responseShowFolder)
	d.label, _ = gtk.LabelNew("")
	d.label.SetEllipsize(pango.ELLIPSIZE_END)
	d.label.SetXAlign(0)
	if area, err := d.bar.GetContentArea(); err == nil {
		area.PackStart(d.label, true, true, 0)
	}
	d.bar.SetNoShowAll(true)
	// A toast at the bottom, clear of tmux's status line, rather than a bar across the
	// top, where links usually are.
	d.bar.SetHAlign(gtk.ALIGN_CENTER)
	d.bar.SetVAlign(gtk.ALIGN_END)
	d.bar.SetMarginBottom(toastMargin)
	d.label.SetMaxWidthChars(toastMaxChars)
	d.bar.Connect("response", func(_ *gtk.InfoBar, response int) {
		switch gtk.ResponseType(response) {
		case responseOpen:
			w.openLink((&url.URL{Scheme: "file", Path: filepath.ToSlash(d.path)}).String())
		case responseShowFolder:
			d.showFolder()
		}
		d.hide()
	})
	return d
}

// show tells of a file just downloaded to path.
func (d *downloadBar) show(path string) {
	d.path = path
	d.label.SetText("Downloaded " + filepath.Base(path) + " to " + filepath.Dir(path))
	d.label.SetTooltipText(path)
	d.label.Show()
	d.bar.Show()
	if d.timer != 0 {
		glib.SourceRemove(d.timer)
	}
	d.timer = glib.TimeoutAdd(uint(downloadBarTimeout.Milliseconds()), func() bool {
		d.timer = 0
		d.hide()
		return false
	})
}

func (d *downloadBar) hide() {
	if d.timer != 0 {
		glib.SourceRemove(d.timer)
		d.timer = 0
	}
	d.bar.Hide()
}

// showFolder opens the folder of the downloaded file in the file manager.
func (d *downloadBar) showFolder() {
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Dir(d.path))}).String()
	if err := gtkx.ShowURI(&d.w.window.Window, uri); err != nil {
		d.w.showError("Could not open the folder", err.Error())
	}
}
