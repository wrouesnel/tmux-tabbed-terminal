package ui

import (
	"fmt"
	"html"
	"strings"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/gotk3/gotk3/pango"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/markdown"
)

// Markdown layout, after GitHub's: sizes in pixels, and heading sizes as scales of the
// body text.
const (
	mdMargin       = 24
	mdBlockSpace   = 12
	mdTightSpace   = 2
	mdRuleSpace    = 4
	mdListIndent   = 28
	mdQuoteIndent  = 16
	mdMarkerIndent = 20
	mdCodePadding  = 16
	mdCodeScale    = 0.9
	// mdImageMaxSize bounds the size images are decoded at, before they're scaled to fit.
	mdImageMaxSize = 4096
)

//nolint:gochecknoglobals
var (
	mdHeadingScales = []float64{2.0, 1.5, 1.25, 1.0, 0.875, 0.85}
	mdHeadingSpace  = []int{24, 24, 18, 16, 14, 14}
)

// Colors that read on light and dark themes alike.
const (
	mdMuted     = "rgba(128,128,128,1)"
	mdCodeBg    = "rgba(128,128,128,0.15)"
	mdLinkColor = "#4493f8"
)

// mdView shows a markdown document, read only, in a text view, styled after GitHub's
// rendering. Links call onLink with their destination. Images call loadImage with theirs,
// which calls back with the picture, or nil if there's none.
type mdView struct {
	view   *gtk.TextView
	buf    *gtk.TextBuffer
	onLink func(dest string)
	// loadImage reads an image, by its destination in the document, and calls then on the
	// main loop with it, or nil.
	loadImage func(dest string, then func(*gdk.Pixbuf))

	links []mdLink
	// fits size the widgets in the text to its width: rules and code blocks as wide, and
	// images no wider. width is the width they were last fitted to, and fitting is set
	// while a fit waits to run.
	fits    []func(width int)
	width   int
	fitting bool
	// afterList is set when the last block was in a tight list, whose items have little
	// space after them, so the next block outside it adds some.
	afterList bool
	// margins are the left margin tags made so far, by name.
	margins map[string]bool
	hand    bool
}

// mdLink is a link's tag and destination.
type mdLink struct {
	tag  *gtk.TextTag
	dest string
}

func newMdView(onLink func(string), loadImage func(string, func(*gdk.Pixbuf))) *mdView {
	v := &mdView{onLink: onLink, loadImage: loadImage, margins: map[string]bool{}}
	v.view, _ = gtk.TextViewNew()
	v.view.SetEditable(false)
	v.view.SetCursorVisible(false)
	v.view.SetWrapMode(gtk.WRAP_WORD_CHAR)
	v.view.SetLeftMargin(mdMargin)
	v.view.SetRightMargin(mdMargin)
	v.view.SetTopMargin(mdMargin)
	v.view.SetBottomMargin(mdMargin)
	addClass(v.view, "ttt-markdown")
	v.view.Connect("size-allocate", func() { v.queueFit() })
	v.view.Connect("button-release-event", func(_ interface{}, ev *gdk.Event) bool {
		return v.onRelease(gdk.EventButtonNewFromEvent(ev))
	})
	v.view.Connect("motion-notify-event", func(_ interface{}, ev *gdk.Event) bool {
		x, y := gdk.EventMotionNewFromEvent(ev).MotionVal()
		v.setHand(v.linkAt(x, y) != "")
		return false
	})
	return v
}

// show replaces what the view shows with a document.
func (v *mdView) show(blocks []markdown.Block) {
	// The view has the buffer first: widgets go in at anchors of its own buffer.
	v.buf, _ = gtk.TextBufferNew(nil)
	v.view.SetBuffer(v.buf)
	v.links, v.fits, v.width, v.afterList = nil, nil, 0, false
	v.margins = map[string]bool{}
	v.createTags()
	for i, b := range blocks {
		v.block(b, i == len(blocks)-1)
	}
	v.queueFit()
}

// createTags makes the buffer's fixed tags.
func (v *mdView) createTags() {
	for i, scale := range mdHeadingScales {
		props := map[string]interface{}{
			"scale": scale, "weight": int(pango.WEIGHT_SEMIBOLD),
			"pixels-above-lines": mdHeadingSpace[i], "pixels-below-lines": mdBlockSpace,
		}
		if i == len(mdHeadingScales)-1 {
			props["foreground"] = mdMuted
		}
		v.buf.CreateTag(fmt.Sprintf("h%d", i+1), props)
	}
	v.buf.CreateTag("block", map[string]interface{}{"pixels-below-lines": mdBlockSpace})
	v.buf.CreateTag("tight", map[string]interface{}{"pixels-below-lines": mdTightSpace})
	v.buf.CreateTag("bold", map[string]interface{}{"weight": int(pango.WEIGHT_BOLD)})
	v.buf.CreateTag("italic", map[string]interface{}{"style": pango.STYLE_ITALIC})
	v.buf.CreateTag("strike", map[string]interface{}{"strikethrough": true})
	v.buf.CreateTag("code", map[string]interface{}{
		"family": "monospace", "scale": mdCodeScale, "background": mdCodeBg,
	})
	v.buf.CreateTag("link", map[string]interface{}{
		"foreground": mdLinkColor, "underline": pango.UNDERLINE_SINGLE,
	})
	v.buf.CreateTag("quote", map[string]interface{}{"foreground": mdMuted})
	// GTK only lays text out again for a tag which sets something affecting size, so this
	// one sets its default.
	v.buf.CreateTag("relayout", map[string]interface{}{"pixels-inside-wrap": 0})
	v.buf.CreateTag("after-list", map[string]interface{}{"pixels-above-lines": mdBlockSpace - mdTightSpace})
	// Created after the heading tags, so its spacing wins over theirs.
	v.buf.CreateTag("ruled", map[string]interface{}{"pixels-below-lines": mdRuleSpace})
}

// margin returns the name of the tag which indents a block by its lists and quotes, and
// the indent. A list item's first line hangs out by its marker.
func (v *mdView) margin(b markdown.Block) (string, int) {
	left := b.Depth*mdListIndent + b.Quote*mdQuoteIndent
	indent := 0
	if b.Depth > 0 {
		left += mdMarkerIndent - mdListIndent
		if b.Marker != "" {
			indent = -mdMarkerIndent
		}
	}
	name := fmt.Sprintf("margin-%d-%d", left, indent)
	if !v.margins[name] {
		v.margins[name] = true
		v.buf.CreateTag(name, map[string]interface{}{"left-margin": mdMargin + left, "indent": indent})
	}
	return name, left
}

// block adds a block to the end of the buffer.
func (v *mdView) block(b markdown.Block, last bool) {
	start := v.buf.GetCharCount()
	space := "block"
	if b.Tight {
		space = "tight"
	}
	margin, indent := v.margin(b)
	tags := []string{margin}
	if b.Quote > 0 {
		tags = append(tags, "quote")
	}

	switch b.Kind {
	case markdown.Paragraph:
		if b.Marker != "" {
			v.insert(b.Marker + " ")
		}
		v.runs(b.Runs, indent)
		tags = append(tags, space)
	case markdown.Heading:
		v.runs(b.Runs, indent)
		tags = append(tags, fmt.Sprintf("h%d", min(max(b.Level, 1), len(mdHeadingScales))))
		if ruledHeading(b) {
			// GitHub rules off the two largest headings, close under them.
			v.applyTags(start, append(tags, "ruled"))
			v.insert("\n", "")
			v.rule(indent, "block")
			tags = nil
		}
	case markdown.CodeBlock:
		v.codeBlock(b.Code, indent)
		tags = append(tags, "block")
	case markdown.Rule:
		v.rule(indent, space)
	case markdown.Table:
		v.table(b.Table)
		tags = append(tags, "block")
	}
	if v.afterList && (!b.Tight || (b.ListStart && b.Depth == 1)) {
		tags = append(tags, "after-list")
	}
	v.afterList = b.Tight
	v.applyTags(start, tags)
	// A rule ends its own paragraph.
	if !last && b.Kind != markdown.Rule && !ruledHeading(b) {
		v.insert("\n", "")
	}
}

// ruledHeading reports whether a block is a heading with a rule under it.
func ruledHeading(b markdown.Block) bool {
	return b.Kind == markdown.Heading && b.Level <= 2 //nolint:mnd // GitHub rules off h1 and h2
}

// applyTags tags the buffer from a character offset to its end. Text is only ever added
// at the end, so offsets stay where they were.
func (v *mdView) applyTags(from int, tags []string) {
	start, end := v.buf.GetIterAtOffset(from), v.buf.GetEndIter()
	for _, t := range tags {
		v.buf.ApplyTagByName(t, start, end)
	}
}

// insert adds text with tags to the end of the buffer.
func (v *mdView) insert(text string, tags ...string) {
	start := v.buf.GetCharCount()
	v.buf.Insert(v.buf.GetEndIter(), text)
	v.applyTags(start, slicesWithout(tags, ""))
}

// slicesWithout returns s without the elements equal to x.
func slicesWithout(s []string, x string) []string {
	out := s[:0:0]
	for _, e := range s {
		if e != x {
			out = append(out, e)
		}
	}
	return out
}

// runs adds styled text, with its links and images, to the end of the buffer.
func (v *mdView) runs(runs []markdown.Run, indent int) {
	for _, r := range runs {
		if r.Image != "" {
			v.image(r, indent)
			continue
		}
		tags := styleTags(r.Style)
		if r.Link != "" {
			tags = append(tags, "link", v.linkTag(r.Link))
		}
		v.insert(r.Text, tags...)
	}
}

// styleTags returns the tags of an inline style.
func styleTags(s markdown.Style) []string {
	var tags []string
	for _, t := range []struct {
		style markdown.Style
		tag   string
	}{{markdown.Bold, "bold"}, {markdown.Italic, "italic"}, {markdown.Strike, "strike"}, {markdown.Code, "code"}} {
		if s&t.style != 0 {
			tags = append(tags, t.tag)
		}
	}
	return tags
}

// linkTag makes a tag for a link's destination, and returns its name.
func (v *mdView) linkTag(dest string) string {
	name := fmt.Sprintf("link-%d", len(v.links))
	tag := v.buf.CreateTag(name, map[string]interface{}{})
	v.links = append(v.links, mdLink{tag: tag, dest: dest})
	return name
}

// rule adds a horizontal line, the width of the text, and ends its paragraph.
func (v *mdView) rule(indent int, space string) {
	sep, _ := gtk.SeparatorNew(gtk.ORIENTATION_HORIZONTAL)
	v.anchor(sep)
	v.fits = append(v.fits, func(width int) { sep.SetSizeRequest(max(width-indent, 1), -1) })
	v.insert("\n", space)
}

// codeBlock adds a code block: its text, wrapped, on a box as wide as the text.
func (v *mdView) codeBlock(code string, indent int) {
	label, _ := gtk.LabelNew(code)
	label.SetXAlign(0)
	label.SetSelectable(true)
	label.SetLineWrap(true)
	label.SetLineWrapMode(pango.WRAP_CHAR)
	// Its width is set below: it mustn't ask for the width of its longest line.
	label.SetMaxWidthChars(1)
	addClass(label, "ttt-md-code-text")
	box, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	addClass(box, "ttt-md-code")
	box.PackStart(label, true, true, 0)
	v.anchor(box)
	v.fits = append(v.fits, func(width int) {
		label.SetSizeRequest(max(width-indent-2*mdCodePadding, 1), -1)
	})
}

// image adds an image, loaded in the background. Until it's loaded, or if it can't be, it
// shows an icon, with the alt text as its tooltip.
func (v *mdView) image(r markdown.Run, indent int) {
	img, _ := gtk.ImageNewFromIconName("image-x-generic-symbolic", gtk.ICON_SIZE_DIALOG)
	alt := r.Text
	if alt == "" {
		alt = r.Image
	}
	img.SetTooltipText(alt)
	v.anchor(img)
	var full *gdk.Pixbuf
	shown := 0
	// Its full size, or scaled down to the text's width.
	fit := func(width int) {
		if full == nil || width <= 0 {
			return
		}
		w := min(full.GetWidth(), max(width-indent, 1))
		if w == shown {
			return
		}
		shown = w
		if w == full.GetWidth() {
			img.SetFromPixbuf(full)
		} else if pb, err := full.ScaleSimple(w, max(full.GetHeight()*w/full.GetWidth(), 1), gdk.INTERP_BILINEAR); err == nil {
			img.SetFromPixbuf(pb)
		}
	}
	v.fits = append(v.fits, fit)
	if v.loadImage == nil {
		return
	}
	buf := v.buf
	v.loadImage(r.Image, func(pb *gdk.Pixbuf) {
		if buf != v.buf {
			return
		}
		if pb == nil {
			img.SetFromIconName("image-missing-symbolic", gtk.ICON_SIZE_DIALOG)
			return
		}
		full = pb
		fit(v.width)
	})
}

// anchor puts a widget at the end of the buffer.
func (v *mdView) anchor(w gtk.IWidget) {
	anchor, err := v.buf.CreateChildAnchor(v.buf.GetEndIter())
	if err != nil {
		return
	}
	v.view.AddChildAtAnchor(w, anchor)
	w.ToWidget().ShowAll()
}

// table adds a table, as a grid of labels.
func (v *mdView) table(t *markdown.TableData) {
	grid, _ := gtk.GridNew()
	addClass(grid, "ttt-md-table")
	var labels []*gtk.Label
	cell := func(runs []markdown.Run, col, row int, header bool) {
		label, _ := gtk.LabelNew("")
		label.SetMarkup(runsMarkup(runs))
		label.SetLineWrap(true)
		label.SetMaxWidthChars(60) //nolint:mnd // a readable column
		label.SetSelectable(false)
		xalign := 0.0
		if col < len(t.Align) {
			switch t.Align[col] {
			case markdown.AlignCenter:
				xalign = 0.5
				label.SetJustify(gtk.JUSTIFY_CENTER)
			case markdown.AlignRight:
				xalign = 1
				label.SetJustify(gtk.JUSTIFY_RIGHT)
			case markdown.AlignNone, markdown.AlignLeft:
			}
		}
		label.SetXAlign(xalign)
		label.SetHExpand(true)
		label.Connect("activate-link", func(_ *gtk.Label, uri string) bool {
			v.onLink(uri)
			return true
		})
		addClass(label, "ttt-md-cell")
		if header {
			addClass(label, "ttt-md-header")
		} else if row%2 == 0 {
			addClass(label, "ttt-md-even")
		}
		if col == 0 {
			addClass(label, "ttt-md-first")
		}
		grid.Attach(label, col, row, 1, 1)
		labels = append(labels, label)
	}
	for col, runs := range t.Header {
		cell(runs, col, 0, true)
	}
	for row, cells := range t.Rows {
		for col := range t.Header {
			var runs []markdown.Run
			if col < len(cells) {
				runs = cells[col]
			}
			cell(runs, col, row+1, false)
		}
	}
	v.anchor(grid)
	// The text view gives widgets their least size, at which a label wraps every word:
	// each is asked for its natural width instead, now it's shown.
	for _, label := range labels {
		_, natural := label.GetPreferredWidth()
		label.SetSizeRequest(natural, -1)
	}
}

// runsMarkup returns styled text as Pango markup, for a label.
func runsMarkup(runs []markdown.Run) string {
	var b strings.Builder
	for _, r := range runs {
		text := html.EscapeString(r.Text)
		if r.Image != "" {
			text = "<i>" + text + "</i>"
		}
		for _, w := range []struct {
			style      markdown.Style
			open, shut string
		}{
			{markdown.Code, `<span font_family="monospace" bgcolor="#808080" bgalpha="25%">`, "</span>"},
			{markdown.Strike, "<s>", "</s>"},
			{markdown.Italic, "<i>", "</i>"},
			{markdown.Bold, "<b>", "</b>"},
		} {
			if r.Style&w.style != 0 {
				text = w.open + text + w.shut
			}
		}
		if r.Link != "" {
			text = `<a href="` + html.EscapeString(r.Link) + `">` + text + "</a>"
		}
		b.WriteString(text)
	}
	return b.String()
}

// queueFit fits rules and images to the text's width, once the view has its size.
func (v *mdView) queueFit() {
	if v.fitting {
		return
	}
	v.fitting = true
	glib.IdleAdd(func() {
		v.fitting = false
		width := v.view.GetAllocatedWidth() - 2*mdMargin
		if width <= 0 || width == v.width {
			return
		}
		v.width = width
		for _, fit := range v.fits {
			fit(width)
		}
		v.relayout()
	})
}

// relayout lays the text out again once the widgets in it have their new sizes: the text
// view keeps the sizes they had when it last laid them out. Tagging all the text makes it
// lay it all out.
func (v *mdView) relayout() {
	buf := v.buf
	glib.IdleAddPriority(glib.PRIORITY_LOW, func() {
		if buf != v.buf {
			return
		}
		start, end := buf.GetBounds()
		buf.ApplyTagByName("relayout", start, end)
		buf.RemoveTagByName("relayout", start, end)
	})
}

// linkAt returns the destination of the link at a point of the view, or "".
func (v *mdView) linkAt(x, y float64) string {
	if v.buf == nil || len(v.links) == 0 {
		return ""
	}
	bx, by := v.view.WindowToBufferCoords(gtk.TEXT_WINDOW_WIDGET, int(x), int(y))
	iter := v.view.GetIterAtLocation(bx, by)
	if iter == nil {
		return ""
	}
	for _, l := range v.links {
		if iter.HasTag(l.tag) {
			return l.dest
		}
	}
	return ""
}

// onRelease follows a link clicked with the left button, unless the click selected text.
func (v *mdView) onRelease(ev *gdk.EventButton) bool {
	if ev.Button() != gdk.BUTTON_PRIMARY || v.buf == nil || v.buf.GetHasSelection() {
		return false
	}
	if dest := v.linkAt(ev.X(), ev.Y()); dest != "" {
		v.onLink(dest)
		return true
	}
	return false
}

// setHand shows a hand pointer over links.
func (v *mdView) setHand(on bool) {
	if on == v.hand {
		return
	}
	v.hand = on
	win := v.view.GetWindow(gtk.TEXT_WINDOW_TEXT)
	if win == nil {
		return
	}
	name := "text"
	if on {
		name = "pointer"
	}
	if display, err := gdk.DisplayGetDefault(); err == nil {
		if cursor, err := gdk.CursorNewFromName(display, name); err == nil {
			win.SetCursor(cursor)
		}
	}
}
