// Package markdown lays out GitHub Flavored Markdown as a list of blocks of styled text,
// for a viewer to draw: paragraphs, headings, list items, code blocks, rules and tables.
// It parses with goldmark, and draws nothing itself.
package markdown

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Style is the inline styles of a run of text, combined.
type Style uint

const (
	Bold Style = 1 << iota
	Italic
	Strike
	Code
)

// Run is a stretch of text in one style. Link is the destination of the link the run is
// in, if any. A run with Image set is an image, with Text its alt text.
type Run struct {
	Text  string
	Style Style
	Link  string
	Image string
}

// Kind is the kind of a block.
type Kind int

const (
	Paragraph Kind = iota
	Heading
	CodeBlock
	Rule
	Table
)

// Block is a block of the document, in the order it's shown. Blocks in lists and block
// quotes say how deep they are, rather than being nested.
type Block struct {
	Kind Kind
	// Level is a heading's level, from 1 to 6.
	Level int
	// Depth is how many lists the block is in, and Quote how many block quotes.
	Depth int
	Quote int
	// Marker is a list item's bullet, number or checkbox, on the item's first block.
	Marker string
	// Tight says the block is in a list without blank lines between its items, so little
	// space follows it. ListStart says it's the first block of a list.
	Tight     bool
	ListStart bool
	Runs      []Run
	// Code is a code block's text, without its last newline, and Lang the language its
	// fence names.
	Code string
	Lang string
	// Table is a table's cells.
	Table *TableData
}

// Align is how a table column's cells are aligned.
type Align int

const (
	AlignNone Align = iota
	AlignLeft
	AlignCenter
	AlignRight
)

// TableData is a table: its column alignments, header cells and rows of cells.
type TableData struct {
	Align  []Align
	Header [][]Run
	Rows   [][][]Run
}

// Bullets are the markers of unordered list items, by depth.
//
//nolint:gochecknoglobals
var bullets = []string{"•", "◦", "▪"}

// Checkboxes for task list items.
const (
	Unchecked = "☐"
	Checked   = "☑"
)

// Parse lays out a markdown document.
func Parse(src []byte) []Block {
	md := goldmark.New(goldmark.WithExtensions(extension.GFM))
	doc := md.Parser().Parse(text.NewReader(src))
	l := &layout{src: src}
	l.blocks(doc, context{})
	return l.out
}

// context is where a block is: how deep in lists and quotes, and whether in a tight list.
type context struct {
	depth, quote int
	tight        bool
}

type layout struct {
	src []byte
	out []Block
	// marker is the marker of a list item whose first block hasn't been laid out yet.
	marker string
}

// add appends a block in a context, giving it any waiting list marker.
func (l *layout) add(b Block, c context) {
	b.Depth, b.Quote, b.Tight = c.depth, c.quote, c.tight
	if b.Marker == "" {
		b.Marker, l.marker = l.marker, ""
	}
	l.out = append(l.out, b)
}

// blocks lays out the block children of n.
func (l *layout) blocks(n ast.Node, c context) {
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		l.block(child, c)
	}
}

func (l *layout) block(n ast.Node, c context) {
	switch n := n.(type) {
	case *ast.Paragraph, *ast.TextBlock:
		runs := l.inlines(n, 0, "")
		marker := ""
		if box, ok := n.FirstChild().(*east.TaskCheckBox); ok {
			marker = Unchecked
			if box.IsChecked {
				marker = Checked
			}
			runs = trimLeadingSpace(runs)
		}
		if marker != "" && l.marker != "" {
			l.marker = ""
		}
		l.add(Block{Kind: Paragraph, Runs: runs, Marker: marker}, c)
	case *ast.Heading:
		l.add(Block{Kind: Heading, Level: n.Level, Runs: l.inlines(n, 0, "")}, c)
	case *ast.ThematicBreak:
		l.add(Block{Kind: Rule}, c)
	case *ast.FencedCodeBlock:
		l.add(Block{Kind: CodeBlock, Code: l.lines(n), Lang: string(n.Language(l.src))}, c)
	case *ast.CodeBlock:
		l.add(Block{Kind: CodeBlock, Code: l.lines(n)}, c)
	case *ast.HTMLBlock:
		// Shown as it's written, apart from comments, which GitHub hides.
		if n.HTMLBlockType != ast.HTMLBlockType2 {
			code := l.lines(n)
			if n.HasClosure() {
				code += string(n.ClosureLine.Value(l.src))
			}
			if code = strings.TrimRight(code, "\n"); code != "" {
				l.add(Block{Kind: Paragraph, Runs: []Run{{Text: code}}}, c)
			}
		}
	case *ast.Blockquote:
		inner := c
		inner.quote++
		l.blocks(n, inner)
	case *ast.List:
		inner := context{depth: c.depth + 1, quote: c.quote, tight: n.IsTight}
		number := n.Start
		first := len(l.out)
		for item := n.FirstChild(); item != nil; item = item.NextSibling() {
			if n.IsOrdered() {
				l.marker = strconv.Itoa(number) + "."
				number++
			} else {
				l.marker = bullets[min(c.depth, len(bullets)-1)]
			}
			l.blocks(item, inner)
			if l.marker != "" {
				// An empty item.
				l.add(Block{Kind: Paragraph}, inner)
			}
		}
		if first < len(l.out) {
			l.out[first].ListStart = true
		}
	case *east.Table:
		l.add(Block{Kind: Table, Table: l.table(n)}, c)
	default:
		l.blocks(n, c)
	}
}

// lines returns the text of a block's lines, without the last newline.
func (l *layout) lines(n ast.Node) string {
	var b bytes.Buffer
	lines := n.Lines()
	for i := range lines.Len() {
		seg := lines.At(i)
		b.Write(seg.Value(l.src))
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func (l *layout) table(n *east.Table) *TableData {
	t := &TableData{}
	for _, a := range n.Alignments {
		switch a {
		case east.AlignLeft:
			t.Align = append(t.Align, AlignLeft)
		case east.AlignCenter:
			t.Align = append(t.Align, AlignCenter)
		case east.AlignRight:
			t.Align = append(t.Align, AlignRight)
		case east.AlignNone:
			t.Align = append(t.Align, AlignNone)
		}
	}
	for row := n.FirstChild(); row != nil; row = row.NextSibling() {
		var cells [][]Run
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			cells = append(cells, l.inlines(cell, 0, ""))
		}
		if _, ok := row.(*east.TableHeader); ok {
			t.Header = cells
		} else {
			t.Rows = append(t.Rows, cells)
		}
	}
	return t
}

// inlines lays out the inline children of n as runs, in a style and inside a link.
func (l *layout) inlines(n ast.Node, style Style, link string) []Run {
	var runs []Run
	add := func(text string, style Style, link string) {
		if text == "" {
			return
		}
		if last := len(runs) - 1; last >= 0 && runs[last].Style == style && runs[last].Link == link &&
			runs[last].Image == "" {
			runs[last].Text += text
			return
		}
		runs = append(runs, Run{Text: text, Style: style, Link: link})
	}
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		switch child := child.(type) {
		case *ast.Text:
			value := child.Segment.Value(l.src)
			if !child.IsRaw() {
				value = unescape(value)
			}
			add(string(value), style, link)
			switch {
			case child.HardLineBreak():
				add("\n", style, link)
			case child.SoftLineBreak():
				add(" ", style, link)
			}
		case *ast.String:
			value := child.Value
			if !child.IsRaw() && !child.IsCode() {
				value = unescape(value)
			}
			add(string(value), style, link)
		case *ast.CodeSpan:
			add(l.plain(child), style|Code, link)
		case *ast.Emphasis:
			s := Italic
			if child.Level >= 2 { //nolint:mnd // ** is strong
				s = Bold
			}
			runs = append(runs, l.inlines(child, style|s, link)...)
		case *east.Strikethrough:
			runs = append(runs, l.inlines(child, style|Strike, link)...)
		case *ast.Link:
			runs = append(runs, l.inlines(child, style, string(child.Destination))...)
		case *ast.AutoLink:
			url := string(child.URL(l.src))
			add(string(child.Label(l.src)), style, url)
		case *ast.Image:
			runs = append(runs, Run{Text: l.plain(child), Style: style, Link: link, Image: string(child.Destination)})
		case *ast.RawHTML:
			// GitHub draws a line break for <br>, and hides other tags, keeping the text
			// between them.
			var raw strings.Builder
			for i := range child.Segments.Len() {
				seg := child.Segments.At(i)
				raw.Write(seg.Value(l.src))
			}
			if tag := strings.ToLower(strings.ReplaceAll(raw.String(), " ", "")); tag == "<br>" || tag == "<br/>" {
				add("\n", style, link)
			}
		case *east.TaskCheckBox:
		default:
			runs = append(runs, l.inlines(child, style, link)...)
		}
	}
	return mergeRuns(runs)
}

// plain returns the text of n's inline children, without styles.
func (l *layout) plain(n ast.Node) string {
	var b strings.Builder
	for _, r := range l.inlines(n, 0, "") {
		b.WriteString(r.Text)
	}
	return b.String()
}

// unescape resolves backslash escapes and character references in text.
func unescape(b []byte) []byte {
	return util.ResolveNumericReferences(util.ResolveEntityNames(util.UnescapePunctuations(b)))
}

// mergeRuns joins neighbouring runs of the same style and link.
func mergeRuns(runs []Run) []Run {
	out := runs[:0]
	for _, r := range runs {
		if last := len(out) - 1; last >= 0 && r.Image == "" && out[last].Image == "" &&
			out[last].Style == r.Style && out[last].Link == r.Link {
			out[last].Text += r.Text
			continue
		}
		out = append(out, r)
	}
	return out
}

// trimLeadingSpace removes the space after a task list item's checkbox.
func trimLeadingSpace(runs []Run) []Run {
	if len(runs) > 0 && runs[0].Image == "" {
		runs[0].Text = strings.TrimLeft(runs[0].Text, " ")
		if runs[0].Text == "" {
			return runs[1:]
		}
	}
	return runs
}
