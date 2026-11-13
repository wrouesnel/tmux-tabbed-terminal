package ui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/gotk3/gotk3/pango"
	"go.uber.org/zap"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/filewatch"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/gtkx"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/markdown"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

// pageFile is the stack page of a pane showing a file.
const pageFile = "file"

// fileCheckInterval is how often a previewed file is checked for changes, when it can't be
// watched.
const fileCheckInterval = 2 * time.Second

// fileSettleTime is how long a watched file is left after it changes before it's read
// again, so a file written in several pieces is read once it's all there.
const fileSettleTime = 100 * time.Millisecond

// fileWatchRetry is how long after watching a file failed it's tried again.
const fileWatchRetry = 10 * time.Second

// Stack pages of a file preview.
const (
	previewImage    = "image"
	previewMarkdown = "markdown"
	previewMessage  = "message"
)

// isMarkdownPath reports whether a path names a markdown file, going by its extension.
func isMarkdownPath(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".md", ".markdown", ".mdown", ".mkd":
		return true
	}
	return false
}

// previewable reports whether a file can be shown in a preview pane: markdown or an image.
func previewable(p string) bool {
	return isMarkdownPath(p) || isImagePath(p)
}

// fileStampScript prints the size and modification time of the file named by its first
// argument, with GNU or BSD stat, to the fraction of a second they give. A name starting
// with ~/ is in the home directory.
const fileStampScript = `case $1 in "~/"*) set -- "$HOME/${1#"~/"}" ;; esac
stat -c '%s %y' -- "$1" 2>/dev/null || stat -f '%z %Fm' -- "$1"`

// fileStamp returns something which changes when a file on a host does: its size and
// modification time.
func fileStamp(ctx context.Context, h *Host, name string) (string, error) {
	if h.client.SSH == nil {
		info, err := os.Stat(name)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%d %d", info.Size(), info.ModTime().UnixNano()), nil
	}
	out, err := h.client.SSH.Output(ctx, "sh", "-c", fileStampScript, "sh", name)
	return strings.TrimSpace(out), err
}

// filePreview shows a file from a session's host in a pane: markdown formatted, or an
// image scaled to fit. It reloads the file when it changes, as when a program rewrites
// it, and follows links between markdown files, with a Back button.
type filePreview struct {
	pane  *Pane
	root  *gtk.Box
	path  *gtk.Label
	back  *gtk.Button
	stack *gtk.Stack

	imageScroller *gtk.ScrolledWindow
	image         *gtk.Image
	full          *gdk.Pixbuf
	fitted        [2]int

	md         *mdView
	mdScroller *gtk.ScrolledWindow
	message    *gtk.Label

	host    string
	file    string
	history []string
	// stamp is the file's size and time when it was last read. generation increases with
	// every load, so a slower, older one is dropped; checking is set while a check for
	// changes runs, and loading while the file's read.
	stamp      string
	generation int
	checking   bool
	loading    bool
	timer      glib.SourceHandle
	// unwatch stops watching the file, and watching is set while it's watched, which
	// stands in for checking it. settle waits for a burst of changes to finish.
	unwatch  context.CancelFunc
	watching bool
	settle   glib.SourceHandle
}

func newFilePreview(p *Pane) *filePreview {
	f := &filePreview{pane: p}
	f.root, _ = gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	addClass(f.root, "ttt-file-preview")

	bar, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, headerSpacing)
	addClass(bar, "ttt-preview-bar")
	f.back, _ = gtk.ButtonNewFromIconName("go-previous-symbolic", gtk.ICON_SIZE_MENU)
	f.back.SetRelief(gtk.RELIEF_NONE)
	f.back.SetTooltipText("Back")
	f.back.Connect("clicked", f.goBack)
	f.path, _ = gtk.LabelNew("")
	f.path.SetEllipsize(pango.ELLIPSIZE_START)
	f.path.SetXAlign(0)
	f.path.SetSelectable(true)
	f.path.SetCanFocus(false)
	addClass(f.path, "dim-label")
	reload, _ := gtk.ButtonNewFromIconName("view-refresh-symbolic", gtk.ICON_SIZE_MENU)
	reload.SetRelief(gtk.RELIEF_NONE)
	reload.SetTooltipText("Reload")
	reload.Connect("clicked", func() { f.load(true) })
	bar.PackStart(f.back, false, false, 0)
	bar.PackStart(f.path, true, true, 0)
	bar.PackEnd(reload, false, false, 0)

	f.stack, _ = gtk.StackNew()
	f.image, _ = gtk.ImageNew()
	f.imageScroller, _ = gtk.ScrolledWindowNew(nil, nil)
	f.imageScroller.Add(f.image)
	f.imageScroller.Connect("size-allocate", func() { glib.IdleAdd(f.fitImage) })
	f.stack.AddNamed(f.imageScroller, previewImage)

	f.md = newMdView(f.follow, f.loadImage)
	f.mdScroller, _ = gtk.ScrolledWindowNew(nil, nil)
	f.mdScroller.SetPolicy(gtk.POLICY_AUTOMATIC, gtk.POLICY_AUTOMATIC)
	f.mdScroller.Add(f.md.view)
	f.stack.AddNamed(f.mdScroller, previewMarkdown)

	f.message, _ = gtk.LabelNew("")
	f.message.SetLineWrap(true)
	f.message.SetJustify(gtk.JUSTIFY_CENTER)
	addClass(f.message, "dim-label")
	f.stack.AddNamed(f.message, previewMessage)

	// Focusing the preview makes its pane the active one.
	for _, w := range []*gtk.Widget{&f.md.view.Widget, &f.imageScroller.Widget} {
		w.Connect("focus-in-event", func() bool {
			p.win.setActivePane(p)
			return false
		})
	}
	f.md.view.Connect("button-press-event", func() bool {
		p.win.setActivePane(p)
		return false
	})
	f.image.SetCanFocus(false)
	f.imageScroller.SetCanFocus(true)

	f.root.PackStart(bar, false, false, 0)
	f.root.PackStart(f.stack, true, true, 0)
	f.root.ShowAll()
	f.timer = glib.TimeoutAdd(uint(fileCheckInterval/time.Millisecond), func() bool {
		f.check()
		return true
	})
	return f
}

// title is the file's name, for the pane header.
func (f *filePreview) title() string {
	return path.Base(f.file)
}

// open shows a file on a host. remember keeps the file shown before for Back.
func (f *filePreview) open(host, name string, remember bool) {
	if remember && f.file != "" && (host != f.host || name != f.file) {
		f.history = append(f.history, f.file)
	} else if !remember {
		f.history = nil
	}
	f.host, f.file, f.stamp = host, name, ""
	shown := tildePath(name)
	if host != LocalHost {
		shown = host + ":" + name
	}
	f.path.SetText(shown)
	f.path.SetTooltipText(shown)
	f.back.SetSensitive(len(f.history) > 0)
	f.load(false)
	f.watch()
	f.pane.win.refresh()
}

// watch watches the shown file for changes, through the host's agent for a remote file,
// and reloads it when it changes. Until it's watching, or if it can't be, the file is
// checked for changes every fileCheckInterval instead.
func (f *filePreview) watch() {
	f.stopWatching()
	h := f.pane.win.app.host(f.host)
	if h == nil {
		return
	}
	ctx, cancel := context.WithCancel(f.pane.win.app.ctx)
	f.unwatch = cancel
	name, log := f.file, f.pane.win.app.log
	// Each runs on the main loop, unless the watch has been stopped by then.
	onMain := func(fn func()) func() {
		return func() {
			glib.IdleAdd(func() {
				if ctx.Err() == nil {
					fn()
				}
			})
		}
	}
	started := onMain(func() { f.watching = true })
	changed := onMain(f.changed)
	go func() {
		err := watchHostFile(ctx, h, name, started, changed)
		onMain(func() {
			f.watching = false
			if errors.Is(err, tmux.ErrNoWatch) {
				return
			}
			log.Debug("Stopped watching a previewed file", zap.String("file", name), zap.Error(err))
			// As when the connection dropped: it's checked meanwhile, and watched again
			// once it can be.
			glib.TimeoutAdd(uint(fileWatchRetry/time.Millisecond), func() bool {
				if ctx.Err() == nil {
					f.watch()
				}
				return false
			})
		})()
	}()
}

// stopWatching stops watching the file.
func (f *filePreview) stopWatching() {
	if f.unwatch != nil {
		f.unwatch()
		f.unwatch = nil
	}
	f.watching = false
	if f.settle != 0 {
		glib.SourceRemove(f.settle)
		f.settle = 0
	}
}

// changed reloads the watched file once it's settled.
func (f *filePreview) changed() {
	if f.settle != 0 {
		glib.SourceRemove(f.settle)
	}
	f.settle = glib.TimeoutAdd(uint(fileSettleTime/time.Millisecond), func() bool {
		f.settle = 0
		f.load(true)
		return false
	})
}

// watchHostFile watches a file on a host, as filewatch.Watch does: directly on this
// machine, or through the agent of a remote host.
func watchHostFile(ctx context.Context, h *Host, name string, started, changed func()) error {
	if h.client.SSH == nil {
		return filewatch.Watch(ctx, name, started, changed)
	}
	return h.client.SSH.Watch(ctx, name, started, changed)
}

// goBack shows the file shown before the last link was followed.
func (f *filePreview) goBack() {
	if n := len(f.history); n > 0 {
		prev := f.history[n-1]
		f.history = f.history[:n-1]
		history := f.history
		f.open(f.host, prev, false)
		f.history = history
		f.back.SetSensitive(len(f.history) > 0)
	}
}

// fileContent is a loaded file: its stamp, and the markdown or the image in it.
type fileContent struct {
	stamp  string
	blocks []markdown.Block
	image  *gdk.Pixbuf
}

// load reads the file and shows it. keepPlace keeps the markdown scrolled where it was,
// for a reload.
func (f *filePreview) load(keepPlace bool) {
	h := f.pane.win.app.host(f.host)
	if h == nil {
		f.showMessage("The host " + f.host + " is no longer listed.")
		return
	}
	f.generation++
	f.loading = true
	gen, name := f.generation, f.file
	runAsync(f.pane.win.app, func(ctx context.Context) (fileContent, error) {
		// The stamp first, so a change while the file's read is seen next time.
		stamp, err := fileStamp(ctx, h, name)
		if err != nil {
			return fileContent{}, err
		}
		c := fileContent{stamp: stamp}
		data, err := readHostFile(ctx, h, name)
		if err != nil {
			return c, err
		}
		if isMarkdownPath(name) {
			c.blocks = markdown.Parse(data)
		} else {
			c.image, err = gtkx.PixbufFit(data, mdImageMaxSize, mdImageMaxSize)
		}
		return c, err
	}, func(c fileContent, err error) {
		if gen != f.generation || f.pane.closed {
			return
		}
		f.loading = false
		if err != nil {
			f.pane.win.app.log.Debug("Could not preview", zap.String("file", name), zap.Error(err))
			// It's tried again when it changes.
			f.stamp = c.stamp
			f.showMessage(fmt.Sprintf("Could not read %s: %v", path.Base(name), err))
			return
		}
		f.stamp = c.stamp
		if c.image != nil {
			f.full, f.fitted = c.image, [2]int{}
			f.stack.SetVisibleChildName(previewImage)
			f.fitImage()
			return
		}
		adj := f.mdScroller.GetVAdjustment()
		place := adj.GetValue()
		f.md.name = path.Base(f.file)
		f.md.show(c.blocks)
		f.stack.SetVisibleChildName(previewMarkdown)
		if keepPlace {
			// Once the new text is laid out, as far down as it now goes.
			glib.IdleAdd(func() { adj.SetValue(min(place, adj.GetUpper()-adj.GetPageSize())) })
		} else {
			adj.SetValue(0)
		}
	})
}

// showMessage shows a message in place of the file.
func (f *filePreview) showMessage(msg string) {
	f.message.SetText(msg)
	f.stack.SetVisibleChildName(previewMessage)
}

// check reloads the file if it's changed since it was read. Files are only checked
// while they're shown.
func (f *filePreview) check() {
	if f.watching || f.checking || f.loading || f.file == "" || !f.root.GetMapped() {
		return
	}
	h := f.pane.win.app.host(f.host)
	if h == nil {
		return
	}
	f.checking = true
	gen, name := f.generation, f.file
	runAsync(f.pane.win.app, func(ctx context.Context) (string, error) {
		return fileStamp(ctx, h, name)
	}, func(stamp string, err error) {
		f.checking = false
		if gen != f.generation || f.pane.closed || err != nil {
			return
		}
		if stamp != f.stamp {
			f.load(true)
		}
	})
}

// fitImage shows the image scaled down to fit the pane, if it's bigger.
func (f *filePreview) fitImage() bool {
	if f.full == nil {
		return false
	}
	w, h := f.imageScroller.GetAllocatedWidth(), f.imageScroller.GetAllocatedHeight()
	if w <= 1 || h <= 1 {
		return false
	}
	fw, fh := f.full.GetWidth(), f.full.GetHeight()
	scale := min(1, float64(w)/float64(fw), float64(h)/float64(fh))
	size := [2]int{max(int(float64(fw)*scale), 1), max(int(float64(fh)*scale), 1)}
	if size == f.fitted {
		return false
	}
	f.fitted = size
	if scale == 1 {
		f.image.SetFromPixbuf(f.full)
	} else if pb, err := f.full.ScaleSimple(size[0], size[1], gdk.INTERP_BILINEAR); err == nil {
		f.image.SetFromPixbuf(pb)
	}
	return false
}

// resolve returns the path a link in the shown file names on its host: relative to the
// file's directory unless it's absolute or in the home directory. A file URI names a file
// on the file's host too, wherever it says, if that's another host. It returns "" for
// other links with a scheme, such as a web page, or to a place in the same file.
func (f *filePreview) resolve(dest string) string {
	u, err := url.Parse(dest)
	if err == nil && strings.EqualFold(u.Scheme, "file") && u.Path != "" &&
		(f.host != LocalHost || isThisHost(u.Host)) {
		return u.Path
	}
	if err != nil || u.Scheme != "" || u.Host != "" || u.Path == "" {
		return ""
	}
	p := u.Path
	if path.IsAbs(p) || strings.HasPrefix(p, "~/") {
		return p
	}
	return path.Join(path.Dir(f.file), p)
}

// follow follows a link in the shown markdown: another markdown file or image opens in
// the preview, other files open as links in the terminal do, and web pages in the
// browser.
func (f *filePreview) follow(dest string) {
	w := f.pane.win
	name := f.resolve(dest)
	switch {
	case name == "":
		if u, err := url.Parse(dest); err == nil && linkSchemes[strings.ToLower(u.Scheme)] {
			w.openLink(dest)
		}
	case previewable(name):
		f.open(f.host, name, true)
	case f.host == LocalHost:
		w.openLink((&url.URL{Scheme: "file", Path: name}).String())
	default:
		w.downloadLink(link{file: name, host: f.host, text: name})
	}
}

// loadImage reads an image the markdown shows, from the file's host. Images on the web
// aren't fetched.
func (f *filePreview) loadImage(dest string, then func(*gdk.Pixbuf)) {
	name := f.resolve(dest)
	h := f.pane.win.app.host(f.host)
	if name == "" || h == nil {
		then(nil)
		return
	}
	runAsync(f.pane.win.app, func(ctx context.Context) (*gdk.Pixbuf, error) {
		data, err := readHostFile(ctx, h, name)
		if err != nil {
			return nil, err
		}
		return gtkx.PixbufFit(data, mdImageMaxSize, mdImageMaxSize)
	}, func(pb *gdk.Pixbuf, err error) {
		if err != nil {
			f.pane.win.app.log.Debug("Could not load an image", zap.String("file", name), zap.Error(err))
		}
		then(pb)
	})
}

// focus moves keyboard focus into the preview.
func (f *filePreview) focus() {
	if f.stack.GetVisibleChildName() == previewMarkdown {
		f.md.view.GrabFocus()
	} else {
		f.imageScroller.GrabFocus()
	}
}

// copySelection copies the markdown text selected, if any.
func (f *filePreview) copySelection() {
	if f.md.buf == nil {
		return
	}
	if clip, err := gtk.ClipboardGet(gdk.SELECTION_CLIPBOARD); err == nil {
		f.md.buf.CopyClipboard(clip)
	}
}

// hasSelection reports whether markdown text is selected.
func (f *filePreview) hasSelection() bool {
	return f.stack.GetVisibleChildName() == previewMarkdown && f.md.buf != nil && f.md.buf.GetHasSelection()
}

// stop stops checking the file for changes.
func (f *filePreview) stop() {
	f.generation++
	f.stopWatching()
	if f.timer != 0 {
		glib.SourceRemove(f.timer)
		f.timer = 0
	}
}
