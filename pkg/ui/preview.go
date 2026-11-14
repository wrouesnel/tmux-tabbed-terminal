package ui

import (
	"bytes"
	"context"
	"html"
	"io"
	"math"
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
	// previewReadWidth is the widest a grown text preview is, about GitHub's page width,
	// so its lines stay short enough to read.
	previewReadWidth = 960
	// previewMinWidth and previewMinHeight are the least a preview is, or is resized to.
	previewMinWidth  = 160
	previewMinHeight = 60
	// previewEdge is how near an edge of a preview, in pixels, a drag resizes it.
	previewEdge = 8
	// previewMaxShare is the most of the window a preview is resized to.
	previewMaxShare = 0.95
)

// The edges of a preview a drag resizes.
const (
	edgeLeft = 1 << iota
	edgeRight
	edgeTop
	edgeBottom
)

// hoverPreview is the preview shown for a link, of an image or the start of a markdown or
// HTML file: the popover, and what's needed to grow it or open the file.
type hoverPreview struct {
	popover *gtk.Popover
	// image is the image shown, or text, the window the text shows in.
	image *gtk.Image
	text  *gtk.ScrolledWindow
	view  *gtk.TextView
	// width is the text's width, and maxHeight the most its height grows to fit it.
	width, maxHeight int
	// sized is set once it's been resized by hand, and keeps the size it was given.
	sized bool
	// events takes the pointer over it. edges are the edges a drag from where the
	// pointer is resizes, or resizing, those a drag under way does; lastX, lastY are
	// where the drag's pointer was last, on the screen.
	events       *gtk.EventBox
	edges        int
	resizing     int
	lastX, lastY float64
	link         link
	data         []byte
	grown        bool
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
	// Neither way does the text hold the window open: it's fitted to the window's width, as
	// it's resized, and scrolled by the wheel.
	scroll.SetPolicy(gtk.POLICY_EXTERNAL, gtk.POLICY_EXTERNAL)
	scroll.SetSizeRequest(previewWidth, previewHeight)
	scroll.Add(view)
	hp := &hoverPreview{text: scroll, view: view, link: l, width: previewWidth, maxHeight: previewHeight}
	// It's as tall as its text, up to maxHeight, once the text is laid out.
	scroll.GetVAdjustment().Connect("changed", func() { glib.IdleAdd(func() { p.fitTextHeight(hp) }) })
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
	hp.events = events
	events.AddEvents(int(gdk.POINTER_MOTION_MASK | gdk.BUTTON_RELEASE_MASK))
	events.Connect("motion-notify-event", func(_ interface{}, ev *gdk.Event) bool {
		p.onPreviewMotion(hp, gdk.EventMotionNewFromEvent(ev))
		return true
	})
	events.Connect("button-release-event", func(_ interface{}, ev *gdk.Event) bool {
		if gdk.EventButtonNewFromEvent(ev).Button() != gdk.BUTTON_PRIMARY || hp.resizing == 0 {
			return false
		}
		hp.resizing = 0
		p.refitImage(hp)
		return true
	})
	events.Connect("scroll-event", func(_ interface{}, ev *gdk.Event) bool {
		if hp.text != nil {
			scrollBy(hp.text.GetVAdjustment(), gdk.EventScrollNewFromEvent(ev))
		}
		return true
	})
	events.Connect("button-press-event", func(_ interface{}, ev *gdk.Event) bool {
		b := gdk.EventButtonNewFromEvent(ev)
		if b.Button() != gdk.BUTTON_PRIMARY {
			return false
		}
		if hp.edges != 0 {
			// A drag from an edge resizes it.
			if b.Type() == gdk.EVENT_BUTTON_PRESS {
				hp.resizing, hp.sized, hp.grown = hp.edges, true, true
				hp.lastX, hp.lastY = b.XRoot(), b.YRoot()
			}
			return true
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
		hp.width = max(min(width, previewReadWidth), previewWidth)
		hp.maxHeight = max(height, previewHeight)
		hp.text.SetSizeRequest(hp.width, previewHeight)
		p.fitTextHeight(hp)
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

// fitTextHeight makes a text preview as tall as its text, up to its maxHeight, so it
// leaves no space below a short one. One resized by hand keeps its size.
func (p *Pane) fitTextHeight(hp *hoverPreview) {
	if hp.text == nil || hp.sized || p.preview != hp {
		return
	}
	adj := hp.text.GetVAdjustment()
	content := int(math.Ceil(adj.GetUpper()))
	if adj.GetUpper() <= adj.GetPageSize()+1 {
		// It all fits, so the adjustment only says how tall the window is: the text's
		// height is where its last line ends.
		buf, err := hp.view.GetBuffer()
		if err != nil {
			return
		}
		y, h := hp.view.GetLineYrange(buf.GetEndIter())
		content = y + h + hp.view.GetTopMargin() + hp.view.GetBottomMargin()
	}
	height := max(min(content, hp.maxHeight), previewMinHeight)
	if _, now := hp.text.GetSizeRequest(); now != height {
		hp.text.SetSizeRequest(hp.width, height)
	}
}

// onPreviewMotion shows a resizing cursor by the edges of a preview that can be dragged,
// and resizes it while one is.
func (p *Pane) onPreviewMotion(hp *hoverPreview, ev *gdk.EventMotion) {
	x, y := ev.MotionVal()
	w, h := float64(hp.events.GetAllocatedWidth()), float64(hp.events.GetAllocatedHeight())
	if hp.resizing == 0 {
		edges := 0
		switch {
		case x < previewEdge:
			edges |= edgeLeft
		case x > w-previewEdge:
			edges |= edgeRight
		}
		switch {
		case y < previewEdge:
			edges |= edgeTop
		case y > h-previewEdge:
			edges |= edgeBottom
		}
		// The edge by its link stays put: the one the popover points from.
		if p.previewBelow(hp) {
			edges &^= edgeTop
		} else {
			edges &^= edgeBottom
		}
		if edges != hp.edges {
			hp.edges = edges
			setCursor(&hp.events.Widget, edgeCursor(edges))
		}
		return
	}
	// The edge dragged follows the pointer: the popover moves as it's resized, so each
	// step resizes it by how far the edge is from the pointer now, but by no more than
	// twice as far as the pointer moved, so an edge that can't move doesn't run away.
	rx, ry := ev.MotionValRoot()
	limit := func(want, moved float64) int {
		if want*moved <= 0 {
			return 0
		}
		return int(math.Copysign(min(math.Abs(want), 2*math.Abs(moved)), want))
	}
	var dw, dh int
	switch {
	case hp.resizing&edgeRight != 0:
		dw = limit(x-w, rx-hp.lastX)
	case hp.resizing&edgeLeft != 0:
		dw = limit(-x, hp.lastX-rx)
	}
	switch {
	case hp.resizing&edgeBottom != 0:
		dh = limit(y-h, ry-hp.lastY)
	case hp.resizing&edgeTop != 0:
		dh = limit(-y, hp.lastY-ry)
	}
	hp.lastX, hp.lastY = rx, ry
	if dw == 0 && dh == 0 {
		return
	}
	var content *gtk.Widget
	if hp.text != nil {
		content = &hp.text.Widget
	} else {
		content = &hp.image.Widget
	}
	cw, ch := content.GetSizeRequest()
	if cw <= 0 {
		cw = content.GetAllocatedWidth()
	}
	if ch <= 0 {
		ch = content.GetAllocatedHeight()
	}
	maxW := int(float64(p.win.window.GetAllocatedWidth()) * previewMaxShare)
	maxH := int(float64(p.win.window.GetAllocatedHeight()) * previewMaxShare)
	cw = max(previewMinWidth, min(cw+dw, maxW))
	ch = max(previewMinHeight, min(ch+dh, maxH))
	hp.width = cw
	content.SetSizeRequest(cw, ch)
}

// previewBelow reports whether a preview is below the point it points at.
func (p *Pane) previewBelow(hp *hoverPreview) bool {
	_, y, err := hp.events.TranslateCoordinates(p.term, 0, 0)
	if err != nil {
		return true
	}
	rect, ok := hp.popover.GetPointingTo()
	return !ok || y >= rect.GetY()
}

// edgeCursor names the cursor for resizing by some edges, or the pointer's for none.
func edgeCursor(edges int) string {
	switch edges {
	case edgeLeft, edgeRight:
		return "ew-resize"
	case edgeTop, edgeBottom:
		return "ns-resize"
	case edgeLeft | edgeTop, edgeRight | edgeBottom:
		return "nwse-resize"
	case edgeRight | edgeTop, edgeLeft | edgeBottom:
		return "nesw-resize"
	}
	return "pointer"
}

// refitImage draws an image preview again to fit the size it's been resized to.
func (p *Pane) refitImage(hp *hoverPreview) {
	if hp.image == nil {
		return
	}
	w, h := hp.image.GetSizeRequest()
	if w <= 0 || h <= 0 {
		return
	}
	runAsync(p.win.app, func(context.Context) (*gdk.Pixbuf, error) {
		return gtkx.PixbufFit(hp.data, w, h)
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
