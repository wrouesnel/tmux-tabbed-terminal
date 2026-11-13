package ui

import (
	"bytes"
	"context"
	"html"
	"io"
	"os"
	"path"
	"regexp"
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
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/markdown"
)

const (
	// previewDelay is how long the pointer rests on a link before its preview is shown.
	previewDelay = 400 * time.Millisecond
	// previewMaxBytes is the biggest file previewed.
	previewMaxBytes = 32 << 20
	// previewTextBytes is how much of the start of a markdown or HTML file is read for
	// its preview, which shows the start of it.
	previewTextBytes = 64 << 10
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

// hoverPreview is the preview shown for a link, of an image or the start of a markdown or
// HTML file: the popover, and what's needed to grow it or open the file.
type hoverPreview struct {
	popover *gtk.Popover
	// image is the image shown, or text, the window the text shows in.
	image *gtk.Image
	text  *gtk.ScrolledWindow
	link  link
	data  []byte
	grown bool
	// engaged is set once the pointer has been on it. It then stays, for reading, until
	// a click or a key, or until the pointer rests on another link.
	engaged bool
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

// readHostFileStart reads up to n bytes from the start of a file on a session's host, as
// readHostFile reads all of one.
func readHostFileStart(ctx context.Context, h *Host, name string, n int) ([]byte, error) {
	if h.client.SSH == nil {
		f, err := os.Open(name)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
			return nil, errors.Errorf("%s isn't a file", name)
		}
		return io.ReadAll(io.LimitReader(f, int64(n)))
	}
	name = strings.TrimPrefix(name, "~/")
	out, err := h.client.SSH.Output(ctx, "head", "-c", strconv.Itoa(n), "--", name)
	return []byte(out), err
}

// isHTMLPath reports whether a path names an HTML file, going by its extension.
func isHTMLPath(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".html", ".htm", ".xhtml":
		return true
	}
	return false
}

// wholeBlocks returns a markdown file read up to limit bytes. If it filled them, so may
// have been cut off, it leaves out the last block, which may be cut short: it returns the
// file up to its last blank line, if it has one.
func wholeBlocks(data []byte, limit int) []byte {
	if len(data) < limit {
		return data
	}
	if i := bytes.LastIndex(data, []byte("\n\n")); i >= 0 {
		return data[:i+1]
	}
	return data
}

//nolint:gochecknoglobals
var (
	// htmlHidden matches the parts of an HTML document that aren't shown as text.
	htmlHidden = regexp.MustCompile(`(?is)<!--.*?(-->|$)|<(script|style|head|template|svg)\b.*?(</(script|style|head|template|svg)\s*>|$)`)
	// htmlTitle matches an HTML document's title.
	htmlTitle = regexp.MustCompile(`(?is)<title\b[^>]*>(.*?)</title`)
	// htmlBreak matches the tags that start or end a block of text.
	htmlBreak = regexp.MustCompile(`(?i)</?(p|div|br|h[1-6]|li|ul|ol|tr|table|section|article|header|footer|nav|main|aside|pre|blockquote|hr|dt|dd|figure|figcaption)\b[^>]*>`)
	// htmlTag matches any other tag.
	htmlTag = regexp.MustCompile(`(?s)<[^>]*(>|$)`)
)

// htmlText returns the text an HTML document shows, roughly: its title, then its text, a
// line to each paragraph or other block.
func htmlText(doc string) string {
	var lines []string
	if m := htmlTitle.FindStringSubmatch(doc); m != nil {
		if title := strings.Join(strings.Fields(html.UnescapeString(htmlTag.ReplaceAllString(m[1], ""))), " "); title != "" {
			lines = append(lines, title, "")
		}
	}
	doc = htmlHidden.ReplaceAllString(doc, "")
	doc = htmlBreak.ReplaceAllString(doc, "\n")
	doc = html.UnescapeString(htmlTag.ReplaceAllString(doc, ""))
	for line := range strings.Lines(doc) {
		if line := strings.Join(strings.Fields(line), " "); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

// onMotion follows the pointer over links, and previews the image, markdown or HTML file
// a link names once the pointer rests on it. Clicking the preview opens the file, which
// works with the mouse alone even where the program in the pane has the mouse's clicks,
// such as a coding agent. In a split window, where the terminal only sees the row of a link
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
	_, _, readLines := p.readsPaneLines(m)
	switch {
	case p.preview != nil && p.preview.engaged:
		// It stays until another link's preview takes its place.
		p.forgetHover()
		if !matched && !readLines {
			return
		}
	case p.preview != nil:
		// Off the link with its preview showing: the pointer may be on its way to it.
		p.hidePreviewSoon()
		return
	default:
		p.hidePreview()
		if !matched && !readLines {
			return
		}
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
// by the point x, y of the terminal, unless the pointer has moved on by then. Of a
// markdown or HTML file, it shows the start.
func (p *Pane) loadPreview(l link, gen int, x, y float64) {
	if l.file != "" && (isMarkdownPath(l.file) || isHTMLPath(l.file)) {
		p.loadTextPreview(l, gen, x, y)
		return
	}
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
	img, err := gtk.ImageNewFromPixbuf(pb)
	if err != nil {
		return
	}
	hp := &hoverPreview{image: img, link: l, data: data}
	p.popPreview(hp, img, "Click to open it in the preview pane", func() { p.win.previewFile(l.host, l.file) }, x, y)
}

// loadTextPreview reads the start of the markdown or HTML file a link names, and shows
// it by the point x, y of the terminal, unless the pointer has moved on by then.
func (p *Pane) loadTextPreview(l link, gen int, x, y float64) {
	h, _ := p.win.app.lookup(p.SessionKey())
	if h == nil {
		return
	}
	runAsync(p.win.app, func(ctx context.Context) ([]byte, error) {
		return readHostFileStart(ctx, h, l.file, previewTextBytes)
	}, func(data []byte, err error) {
		if p.closed || gen != p.hoverGen {
			return
		}
		if err != nil {
			p.win.app.log.Debug("No preview", zap.String("file", l.file), zap.Error(err))
			return
		}
		p.showTextPreview(l, data, x, y)
	})
}

// showTextPreview shows the start of a markdown or HTML file in a popover pointing at
// x, y of the terminal, as showPreview shows an image: markdown formatted as the preview
// pane formats it, and HTML as its text. A click opens the file: markdown in the preview
// pane, and HTML as a double click on its link would.
func (p *Pane) showTextPreview(l link, data []byte, x, y float64) {
	var view *gtk.TextView
	caption := "Click to open it in the preview pane"
	if isMarkdownPath(l.file) {
		md := newMdView(func(string) {}, func(_ string, then func(*gdk.Pixbuf)) { then(nil) })
		md.name = path.Base(l.file)
		md.show(markdown.Parse(wholeBlocks(data, previewTextBytes)))
		view = md.view
	} else {
		view, _ = gtk.TextViewNew()
		view.SetWrapMode(gtk.WRAP_WORD_CHAR)
		for _, set := range []func(int){view.SetLeftMargin, view.SetRightMargin, view.SetTopMargin, view.SetBottomMargin} {
			set(mdMargin)
		}
		buf, _ := view.GetBuffer()
		buf.SetText(htmlText(string(data)))
		caption = "Click to open it"
	}
	view.SetEditable(false)
	view.SetCursorVisible(false)
	view.SetCanFocus(false)
	scroll, _ := gtk.ScrolledWindowNew(nil, nil)
	scroll.SetPolicy(gtk.POLICY_NEVER, gtk.POLICY_EXTERNAL)
	scroll.SetSizeRequest(previewWidth, previewHeight)
	scroll.Add(view)
	hp := &hoverPreview{text: scroll, link: l}
	p.popPreview(hp, scroll, caption, func() { p.win.openLinkDefault(l) }, x, y)
}

// popPreview shows a hover preview, its content above a caption, in a popover pointing at
// x, y of the terminal, in place of any other. It doesn't take the focus, so typing still
// goes to the terminal. With the pointer on it, it grows, and stays; the wheel scrolls
// text, and a click calls open.
func (p *Pane) popPreview(hp *hoverPreview, content gtk.IWidget, caption string, open func(), x, y float64) {
	popover, err := gtk.PopoverNew(p.term)
	if err != nil {
		return
	}
	hp.popover = popover
	addClass(popover, "ttt-hover-preview")
	popover.SetModal(false)
	rect := gdk.Rectangle{}
	rect.SetX(int(x))
	rect.SetY(int(y))
	rect.SetWidth(1)
	rect.SetHeight(1)
	popover.SetPointingTo(rect)
	popover.SetPosition(gtk.POS_BOTTOM)

	box, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	box.PackStart(content, true, true, 0)
	label, _ := gtk.LabelNew(caption)
	addClass(label, "dim-label")
	addClass(label, "ttt-hover-preview-caption")
	box.PackStart(label, false, false, 0)
	// The box above the content takes the pointer, so a click opens the file rather than
	// selecting text: GtkImage has no window of its own to get events, and a text view
	// would take them.
	events, _ := gtk.EventBoxNew()
	events.SetAboveChild(true)
	events.AddEvents(int(gdk.SCROLL_MASK | gdk.SMOOTH_SCROLL_MASK))
	events.Add(box)
	popover.Add(events)

	events.Connect("enter-notify-event", func() bool {
		if p.preview == hp {
			p.keepPreview()
			hp.engaged = true
			p.growPreview(hp)
		}
		return false
	})
	events.Connect("leave-notify-event", func() bool {
		if p.preview == hp {
			p.hidePreviewSoon()
		}
		return false
	})
	events.Connect("scroll-event", func(_ interface{}, ev *gdk.Event) bool {
		if hp.text != nil {
			scrollBy(hp.text.GetVAdjustment(), gdk.EventScrollNewFromEvent(ev))
		}
		return true
	})
	events.Connect("button-press-event", func(_ interface{}, ev *gdk.Event) bool {
		if gdk.EventButtonNewFromEvent(ev).Button() != gdk.BUTTON_PRIMARY {
			return false
		}
		p.hidePreview()
		open()
		return true
	})
	if old := p.preview; old != nil {
		p.dropPreview(old)
	}
	events.ShowAll()
	popover.Show()
	p.preview = hp
}

// scrollBy scrolls an adjustment by a wheel event, by a few lines a notch.
func scrollBy(adj *gtk.Adjustment, ev *gdk.EventScroll) {
	if adj == nil {
		return
	}
	step := adj.GetPageSize() / 6 //nolint:mnd // a few lines
	var steps float64
	switch ev.Direction() {
	case gdk.SCROLL_UP:
		steps = -1
	case gdk.SCROLL_DOWN:
		steps = 1
	case gdk.SCROLL_SMOOTH:
		steps = ev.DeltaY()
	case gdk.SCROLL_LEFT, gdk.SCROLL_RIGHT:
		return
	}
	value := adj.GetValue() + steps*step
	adj.SetValue(max(adj.GetLower(), min(value, adj.GetUpper()-adj.GetPageSize())))
}

// growPreview shows a preview larger, up to previewGrownShare of the window, once.
// Images already shown whole stay as they are, and text shows more of the file.
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
	if hp.text != nil {
		hp.text.SetSizeRequest(max(width, previewWidth), max(height, previewHeight))
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
// to its link or to it. One the pointer has been on stays.
func (p *Pane) hidePreviewSoon() {
	hp := p.preview
	if hp == nil || hp.engaged || hp.hideTimer != 0 {
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

// hidePreview hides the hover preview, and forgets the link under the pointer.
func (p *Pane) hidePreview() {
	p.forgetHover()
	if hp := p.preview; hp != nil {
		p.dropPreview(hp)
	}
}

// forgetHover forgets the link under the pointer, so a lookup or load still under way for
// it shows nothing.
func (p *Pane) forgetHover() {
	p.hoverGen++
	p.hoverText, p.hoverSpan = "", cellSpan{}
	if p.hoverTimer != 0 {
		glib.SourceRemove(p.hoverTimer)
		p.hoverTimer = 0
	}
}

// dropPreview takes a hover preview down.
func (p *Pane) dropPreview(hp *hoverPreview) {
	if p.preview == hp {
		p.preview = nil
	}
	if hp.hideTimer != 0 {
		glib.SourceRemove(hp.hideTimer)
		hp.hideTimer = 0
	}
	hp.popover.Destroy()
}
