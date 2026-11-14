package ui

import (
	"reflect"
	"testing"
)

// TestLinksInLines finds links in order, without the parts of longer ones.
func TestLinksInLines(t *testing.T) {
	lines := []string{
		"⏺ Write(docs/plan.md) and see https://example.com/a/b.png",
		"Saved /tmp/shot.png, then `notes.md` and ../lib/y.c:12",
	}
	got := linksInLines(lines)
	want := []textLink{
		{"docs/plan.md", linkRelative},
		{"https://example.com/a/b.png", linkURL},
		{"/tmp/shot.png", linkPath},
		{"`notes.md`", linkCode},
		{"../lib/y.c:12", linkPath},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}
}

// TestShelfKindOf lists images, markdown and HTML, and nothing else.
func TestShelfKindOf(t *testing.T) {
	for file, want := range map[string]shelfKind{"a/plan.md": shelfMarkdown, "r.HTML": shelfHTML, "s.png": shelfImage} {
		if got, ok := shelfKindOf(file); !ok || got != want {
			t.Errorf("%s: got %v, %v", file, got, ok)
		}
	}
	if _, ok := shelfKindOf("main.go"); ok {
		t.Error("main.go is on the shelf")
	}
}

// TestMarkdownSummary takes the first heading as the title, and the text without its
// markdown, code or table rules.
func TestMarkdownSummary(t *testing.T) {
	doc := "# Upload plan\n\nDrop files on a **pane**.\n\n```go\nx := 1\n```\n\n## Steps\n\n" +
		"- [x] Pipe it, see [the doc](d.md)\n- [ ] Test it\n\n| a | b |\n|---|---|\n| 1 | 2 |\n"
	title, text := markdownSummary(doc, "plan.md")
	if title != "Upload plan" {
		t.Errorf("title %q", title)
	}
	if want := "Drop files on a pane. · Pipe it, see the doc · Test it · a b 1 2"; text != want {
		t.Errorf("text %q, want %q", text, want)
	}
	if title, _ := markdownSummary("Just text.\n", "n.md"); title != "n.md" {
		t.Errorf("untitled: %q", title)
	}
}

// TestHTMLSummary takes the title element as the title, and the body's text.
func TestHTMLSummary(t *testing.T) {
	doc := "<html><head><title>Test Report</title></head><body><h1>Results</h1><p>All 42 passed.</p></body></html>"
	title, text := htmlSummary(doc, "r.html")
	if title != "Test Report" || text != "Results All 42 passed." {
		t.Errorf("got %q, %q", title, text)
	}
}

// TestClip shortens long text with an ellipsis.
func TestClip(t *testing.T) {
	if got := clip("abcdef", 4); got != "abc…" {
		t.Errorf("got %q", got)
	}
	if got := clip("abc", 4); got != "abc" {
		t.Errorf("got %q", got)
	}
}

// TestShelfShow takes Preferences' choice over the configuration file's, and showing for
// new files when neither is one of the choices.
func TestShelfShow(t *testing.T) {
	never, bad := shelfShowNever, "sometimes"
	for _, c := range []struct {
		cfg   string
		prefs *string
		want  string
	}{
		{shelfShowAlways, nil, shelfShowAlways},
		{shelfShowAlways, &never, shelfShowNever},
		{"", &bad, shelfShowNewFiles},
		{"", nil, shelfShowNewFiles},
	} {
		a := &App{cfg: Config{Shelf: ShelfConfig{Show: c.cfg}}, state: uiState{ShelfShow: c.prefs}}
		if got := a.shelfShow(); got != c.want {
			t.Errorf("config %q, preferences %v: got %q, want %q", c.cfg, c.prefs, got, c.want)
		}
	}
}
