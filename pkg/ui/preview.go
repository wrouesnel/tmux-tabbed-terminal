package ui

import (
	"context"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/pkg/errors"
	"go.uber.org/zap"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/gtkx"
)

const (
	// previewDelay is how long the pointer rests on a link before its image is shown.
	previewDelay = 400 * time.Millisecond
	// previewMaxBytes is the biggest file previewed.
	previewMaxBytes = 32 << 20
	// The largest a preview is shown, in pixels. Smaller images aren't enlarged.
	previewWidth  = 360
	previewHeight = 270
	// previewGrownShare is the most of the window's width and height a preview grows to
	// while the pointer is on it.
	previewGrownShare = 0.8
	// previewGrace is how long a preview stays once the pointer leaves its link or it, so
	// the pointer can cross from one to the other.
	previewGrace = 500 * time.Millisecond
)

// hoverPreview is the image preview shown for a link: the popover, and what's needed to
// grow it or open the file.
type hoverPreview struct {
	popover *gtk.Popover
	image   *gtk.Image
	link    link
	data    []byte
	grown   bool
	// hideTimer hides it, once the pointer has been off the link and it for previewGrace.
	hideTimer glib.SourceHandle
}

// errPreviewTooBig is returned for a file over previewMaxBytes.
var errPreviewTooBig = errors.New("the file is too big to preview")

//nolint:gochecknoglobals
var (
	imageExtensionsOnce sync.Once
	imageExtensions     map[string]bool
)

// isImagePath reports whether a path names a kind of image GdkPixbuf can load, going by
// its extension.
func isImagePath(p string) bool {
	imageExtensionsOnce.Do(func() {
		imageExtensions = map[string]bool{}
		for _, f := range gdk.PixbufGetFormats() {
			for _, ext := range f.GetExtensions() {
				imageExtensions[strings.ToLower(ext)] = true
			}
		}
	})
	ext := strings.TrimPrefix(path.Ext(p), ".")
	return ext != "" && imageExtensions[strings.ToLower(ext)]
}

// readLocalFile reads a file on this machine, up to previewMaxBytes.
func readLocalFile(name string) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	switch {
	case err != nil:
		return nil, err
	case !info.Mode().IsRegular():
		return nil, errors.Errorf("%s isn't a file", name)
	case info.Size() > previewMaxBytes:
		return nil, errPreviewTooBig
	}
	data, err := io.ReadAll(io.LimitReader(f, previewMaxBytes+1))
	if err == nil && len(data) > previewMaxBytes {
		return nil, errPreviewTooBig
	}
	return data, err
}

// readHostFile reads a file on a session's host, through its ssh connection if
// it's remote, up to previewMaxBytes. A remote path may start with ~/, for the home
// directory there, where ssh starts.
func readHostFile(ctx context.Context, h *Host, name string) ([]byte, error) {
	if h.client.SSH == nil {
		return readLocalFile(name)
	}
	name = strings.TrimPrefix(name, "~/")
	out, err := h.client.SSH.Output(ctx, "head", "-c", strconv.Itoa(previewMaxBytes+1), "--", name)
	if err != nil {
		return nil, err
	}
	if len(out) > previewMaxBytes {
		return nil, errPreviewTooBig
	}
	return []byte(out), nil
}

// onMotion follows the pointer over links, and previews the image a link names once the
// pointer rests on it. In a split window, where the terminal only sees the row of a link
// tmux wrapped that the pointer is on, if that, tmux is asked whenever the pointer rests,
// and the preview stays while the pointer is on the link it found.
func (p *Pane) onMotion(event *gdk.Event) {
	if !p.win.app.cfg.Links.ImagePreviews || !p.running {
		return
	}
	m, matched := p.term.MatchAt(event)
	x, y := gdk.EventMotionNewFromEvent(event).MotionVal()
	col, row := p.term.CellAt(x, y)
	if p.hoverSpan.contains(col, row) || (matched && p.hoverSpan == cellSpan{} && m.Text == p.hoverText) {
		p.keepPreview()
		return
	}
	// Off the link with its preview showing: the pointer may be on its way to it.
	if p.preview != nil {
		p.hidePreviewSoon()
		return
	}
	p.hidePreview()
	if _, _, readLines := p.readsPaneLines(m); !matched && !readLines {
		return
	}
	p.hoverText = m.Text
	gen := p.hoverGen
	p.hoverTimer = glib.TimeoutAdd(uint(previewDelay/time.Millisecond), func() bool {
		p.hoverTimer = 0
		if gen != p.hoverGen {
			return false
		}
		p.linkAt(m, matched, x, y, func(l link, ok bool) {
			if ok && gen == p.hoverGen && !p.closed {
				p.hoverSpan = l.span
				p.loadPreview(l, gen, x, y)
			}
		})
		return false
	})
}

// loadPreview reads and decodes the image a link names, if it names one, and shows it
// by the point x, y of the terminal, unless the pointer has moved on by then.
func (p *Pane) loadPreview(l link, gen int, x, y float64) {
	if l.file == "" || !isImagePath(l.file) {
		return
	}
	h, _ := p.win.app.lookup(p.SessionKey())
	if h == nil {
		return
	}
	type loaded struct {
		data []byte
		pb   *gdk.Pixbuf
	}
	runAsync(p.win.app, func(ctx context.Context) (loaded, error) {
		data, err := readHostFile(ctx, h, l.file)
		if err != nil {
			return loaded{}, err
		}
		pb, err := gtkx.PixbufFit(data, previewWidth, previewHeight)
		return loaded{data, pb}, err
	}, func(img loaded, err error) {
		if p.closed || gen != p.hoverGen {
			return
		}
		if err != nil {
			p.win.app.log.Debug("No preview", zap.String("file", l.file), zap.Error(err))
			return
		}
		p.showPreview(l, img.data, img.pb, x, y)
	})
}

// showPreview shows an image in a popover pointing at x, y of the terminal. It doesn't
// take the focus, so typing still goes to the terminal. With the pointer on it, it grows,
// and a click opens the file in the preview pane.
func (p *Pane) showPreview(l link, data []byte, pb *gdk.Pixbuf, x, y float64) {
	popover, err := gtk.PopoverNew(p.term)
	if err != nil {
		return
	}
	img, err := gtk.ImageNewFromPixbuf(pb)
	if err != nil {
		popover.Destroy()
		return
	}
	addClass(popover, "ttt-image-preview")
	popover.SetModal(false)
	// The image takes the pointer: GtkImage has no window of its own to get events.
	events, _ := gtk.EventBoxNew()
	events.Add(img)
	events.SetTooltipText("Click to open " + path.Base(l.file) + " in the preview pane")
	popover.Add(events)
	rect := gdk.Rectangle{}
	rect.SetX(int(x))
	rect.SetY(int(y))
	rect.SetWidth(1)
	rect.SetHeight(1)
	popover.SetPointingTo(rect)
	popover.SetPosition(gtk.POS_BOTTOM)

	hp := &hoverPreview{popover: popover, image: img, link: l, data: data}
	events.Connect("enter-notify-event", func() bool {
		p.keepPreview()
		p.growPreview(hp)
		return false
	})
	events.Connect("leave-notify-event", func() bool {
		p.hidePreviewSoon()
		return false
	})
	events.Connect("button-press-event", func(_ interface{}, ev *gdk.Event) bool {
		if gdk.EventButtonNewFromEvent(ev).Button() != gdk.BUTTON_PRIMARY {
			return false
		}
		p.hidePreview()
		p.win.previewFile(l.host, l.file)
		return true
	})
	events.ShowAll()
	popover.Show()
	p.preview = hp
}

// growPreview shows a preview larger, up to previewGrownShare of the window, once.
// Images already shown whole stay as they are.
func (p *Pane) growPreview(hp *hoverPreview) {
	if hp.grown {
		return
	}
	hp.grown = true
	width := int(float64(p.win.window.GetAllocatedWidth()) * previewGrownShare)
	height := int(float64(p.win.window.GetAllocatedHeight()) * previewGrownShare)
	if width <= previewWidth && height <= previewHeight {
		return
	}
	runAsync(p.win.app, func(context.Context) (*gdk.Pixbuf, error) {
		return gtkx.PixbufFit(hp.data, width, height)
	}, func(pb *gdk.Pixbuf, err error) {
		if err != nil || p.preview != hp {
			return
		}
		hp.image.SetFromPixbuf(pb)
	})
}

// keepPreview keeps the preview showing: the pointer is on its link or on it.
func (p *Pane) keepPreview() {
	if hp := p.preview; hp != nil && hp.hideTimer != 0 {
		glib.SourceRemove(hp.hideTimer)
		hp.hideTimer = 0
	}
}

// hidePreviewSoon hides the preview after previewGrace, unless the pointer comes back
// to its link or to it.
func (p *Pane) hidePreviewSoon() {
	hp := p.preview
	if hp == nil || hp.hideTimer != 0 {
		return
	}
	hp.hideTimer = glib.TimeoutAdd(uint(previewGrace/time.Millisecond), func() bool {
		hp.hideTimer = 0
		if p.preview == hp {
			p.hidePreview()
		}
		return false
	})
}

// hidePreview hides the image preview, and forgets the link under the pointer, so a
// lookup or load still under way shows nothing.
func (p *Pane) hidePreview() {
	p.hoverGen++
	p.hoverText, p.hoverSpan = "", cellSpan{}
	if p.hoverTimer != 0 {
		glib.SourceRemove(p.hoverTimer)
		p.hoverTimer = 0
	}
	if hp := p.preview; hp != nil {
		p.preview = nil
		if hp.hideTimer != 0 {
			glib.SourceRemove(hp.hideTimer)
		}
		hp.popover.Destroy()
	}
}
