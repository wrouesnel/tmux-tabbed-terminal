//go:build !windows

package ui

import (
	"math"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/gtk"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/markdown"
)

// mdTestEnv makes TestMdView run its checks, rather than run itself again on a virtual X
// server.
const mdTestEnv = "TTT_TEST_MDVIEW"

const mdSample = "# Title\n\nSome *italic* and **bold** text with `code` and a [link](b.md).\n\n" +
	"- one\n- [x] two\n\n```\nfunc main() {}\n```\n\n| A | B |\n|---|--:|\n| 1 | 2 |\n\n![shot](a.png)\n\n---\n\nEnd.\n"

// TestMdView checks a document's text and styles in the markdown view.
func TestMdView(t *testing.T) {
	if os.Getenv(mdTestEnv) == "" {
		if _, err := exec.LookPath("xvfb-run"); err != nil {
			t.Skip("xvfb-run is not installed")
		}
		cmd := exec.Command("xvfb-run", "-a", os.Args[0], "-test.run", "^TestMdView$", "-test.v") //nolint:gosec // test binary
		cmd.Env = append(os.Environ(), mdTestEnv+"=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("on a virtual X server: %v\n%s", err, out)
		}
		return
	}
	runtime.LockOSThread()
	if err := initTestGTK(); err != nil {
		t.Fatal(err)
	}

	var followed string
	var loaded []string
	v := newMdView(func(dest string) { followed = dest }, func(dest string, then func(*gdk.Pixbuf)) {
		loaded = append(loaded, dest)
		then(nil)
	})
	v.show(markdown.Parse([]byte(mdSample)))

	start, end := v.buf.GetBounds()
	text := start.GetSlice(end)
	// Each widget, a rule, the table and the image, is one anchor character.
	const anchor = "\uFFFC"
	want := "Title\n" + anchor + "\nSome italic and bold text with code and a link.\n• one\n☑ two\n" +
		anchor + "\n" + anchor + "\n" + anchor + "\n" + anchor + "\nEnd."
	if text != want {
		t.Errorf("text\n%q\nwant\n%q", text, want)
	}
	if len(loaded) != 1 || loaded[0] != "a.png" {
		t.Errorf("loaded images %q, want a.png", loaded)
	}

	table, _ := v.buf.GetTagTable()
	for _, c := range []struct{ tag, prop string }{
		{"italic", "style-set"}, {"bold", "weight-set"}, {"h1", "scale-set"}, {"link", "underline-set"},
		{"link", "foreground-set"}, {"code", "background-set"}, {"code", "family-set"},
		{"strike", "strikethrough-set"}, {"after-list", "pixels-above-lines-set"},
	} {
		tag, err := table.Lookup(c.tag)
		if err != nil {
			t.Fatalf("no tag %s", c.tag)
		}
		if set, err := tag.GetProperty(c.prop); err != nil || set != true {
			t.Errorf("%s: %s is %v", c.tag, c.prop, set)
		}
	}

	// The link's text has its tag, and a click there follows it.
	iter := v.buf.GetStartIter()
	if m, _, ok := iter.ForwardSearch("link", gtk.TEXT_SEARCH_TEXT_ONLY, nil); !ok || !m.HasTag(v.links[0].tag) {
		t.Errorf("the link's text isn't tagged with it")
	}
	if len(v.links) != 1 || v.links[0].dest != "b.md" {
		t.Errorf("links %+v", v.links)
	}
	v.onLink(v.links[0].dest)
	if followed != "b.md" {
		t.Errorf("followed %q", followed)
	}

	// Zooming makes the text bigger, and resetting it puts it back.
	win, _ := gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	win.SetDefaultSize(600, 400)
	win.Add(v.view)
	win.ShowAll()
	defer win.Destroy()
	lineHeight := func() int {
		for until := time.Now().Add(300 * time.Millisecond); time.Now().Before(until); {
			for gtk.EventsPending() {
				gtk.MainIteration()
			}
			time.Sleep(10 * time.Millisecond)
		}
		_, end := v.buf.GetBounds()
		_, h := v.view.GetLineYrange(end)
		return h
	}
	before := lineHeight()
	v.zoomBy(3)
	if v.zoom <= 1 {
		t.Errorf("zoomed in to %v", v.zoom)
	}
	if after := lineHeight(); after <= before {
		t.Errorf("zoomed in, the last line is %d high, was %d", after, before)
	}
	v.zoomBy(0)
	if v.zoom != 1 || lineHeight() != before {
		t.Errorf("reset to %v, the last line %d high, was %d", v.zoom, lineHeight(), before)
	}
}

func TestMdBodySize(t *testing.T) {
	for _, c := range []struct{ desktop, zoom, want float64 }{
		{11, 1, 12},   // the usual desktop font: GitHub's 16px
		{9, 1, 12},    // a small one: no smaller than GitHub's
		{16.5, 1, 18}, // a big one: bigger still, as GitHub's is than the usual
		{11, 2, 24},
	} {
		if got := mdBodySize(c.desktop, c.zoom); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("mdBodySize(%v, %v) = %v, want %v", c.desktop, c.zoom, got, c.want)
		}
	}
	if got := zoomed(1, 1); got != zoomStep {
		t.Errorf("a step in: %v", got)
	}
	if got := zoomed(1, -100); got != minZoom {
		t.Errorf("far out: %v, want %v", got, minZoom)
	}
	if got := zoomed(3, 0); got != 1 {
		t.Errorf("reset: %v", got)
	}
}

func TestRunsMarkup(t *testing.T) {
	got := runsMarkup([]markdown.Run{
		{Text: "a<b"}, {Text: "x", Style: markdown.Bold | markdown.Code}, {Text: "go", Link: "https://x/?a&b"},
	})
	want := `a&lt;b<b><span font_family="monospace" bgcolor="#808080" bgalpha="25%">x</span></b>` +
		`<a href="https://x/?a&amp;b">go</a>`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestSpansMarkup(t *testing.T) {
	got := spansMarkup([]markdown.Span{{Text: "if"}, {Text: " a<b", Color: "#cf222e", Bold: true, Italic: true}})
	want := `if<span foreground="#cf222e"><b><i> a&lt;b</i></b></span>`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// TestMermaid draws a diagram, at a width, light and dark, and finds a kind that isn't
// supported isn't drawn.
func TestMermaid(t *testing.T) {
	for _, dark := range []bool{false, true} {
		d, ok := mermaidSVG("flowchart LR\n  A[Start] --> B[End]\n", dark)
		if !ok || d.width < 50 || d.height < 10 {
			t.Fatalf("dark %v: %v, %v×%v", dark, ok, d.width, d.height)
		}
		pb, err := drawSVG(d.svg, d.width, 40)
		if err != nil || pb.GetWidth() != 40 {
			t.Errorf("dark %v: drawn %v, %v", dark, pb, err)
		}
	}
	if _, ok := mermaidSVG("zenuml\n  title x\n", false); ok {
		t.Error("drew an unsupported diagram")
	}
}
