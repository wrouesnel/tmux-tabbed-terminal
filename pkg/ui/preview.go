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
	// previewMaxBytes is the biggest image file previewed.
	previewMaxBytes = 32 << 20
	// The largest a preview is shown, in pixels. Smaller images aren't enlarged.
	previewWidth  = 360
	previewHeight = 270
)

// errPreviewTooBig is returned for an image file over previewMaxBytes.
var errPreviewTooBig = errors.New("the image is too big to preview")

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

// readLocalImage reads an image file on this machine, up to previewMaxBytes.
func readLocalImage(name string) ([]byte, error) {
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

// readHostImage reads an image file on a session's host, through its ssh connection if
// it's remote, up to previewMaxBytes. A remote path may start with ~/, for the home
// directory there, where ssh starts.
func readHostImage(ctx context.Context, h *Host, name string) ([]byte, error) {
	if h.client.SSH == nil {
		return readLocalImage(name)
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
	runAsync(p.win.app, func(ctx context.Context) (*gdk.Pixbuf, error) {
		data, err := readHostImage(ctx, h, l.file)
		if err != nil {
			return nil, err
		}
		return gtkx.PixbufFit(data, previewWidth, previewHeight)
	}, func(pb *gdk.Pixbuf, err error) {
		if p.closed || gen != p.hoverGen {
			return
		}
		if err != nil {
			p.win.app.log.Debug("No preview", zap.String("file", l.file), zap.Error(err))
			return
		}
		p.showPreview(pb, x, y)
	})
}

// showPreview shows an image in a popover pointing at x, y of the terminal. It doesn't
// take the focus, so typing still goes to the terminal.
func (p *Pane) showPreview(pb *gdk.Pixbuf, x, y float64) {
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
	popover.Add(img)
	rect := gdk.Rectangle{}
	rect.SetX(int(x))
	rect.SetY(int(y))
	rect.SetWidth(1)
	rect.SetHeight(1)
	popover.SetPointingTo(rect)
	popover.SetPosition(gtk.POS_BOTTOM)
	img.Show()
	popover.Show()
	p.preview = popover
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
	if p.preview != nil {
		p.preview.Destroy()
		p.preview = nil
	}
}
