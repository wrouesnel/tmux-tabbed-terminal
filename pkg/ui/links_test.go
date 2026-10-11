package ui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/vte"
)

// TestLinkPatterns checks what each pattern matches in a line, as VTE would find it.
func TestLinkPatterns(t *testing.T) {
	cases := []struct {
		kind linkKind
		line string
		want []string
	}{
		{linkMarkdown, "see [the docs](docs/README.md) and ![shot](img/a.png).",
			[]string{"[the docs](docs/README.md)", "![shot](img/a.png)"}},
		{linkMarkdown, `[t](https://x.org/a "A title") [](<my file.png>)`,
			[]string{`[t](https://x.org/a "A title")`, "[](<my file.png>)"}},
		{linkMarkdown, "[not a link] (x) and [x]() and [a\n](b)", nil},
		{linkURL, "go to https://example.com/a?b=1#c.", []string{"https://example.com/a?b=1#c"}},
		{linkURL, "(see https://en.wikipedia.org/wiki/Tmux_(software)), ftp://h/f;",
			[]string{"https://en.wikipedia.org/wiki/Tmux_(software)", "ftp://h/f"}},
		{linkURL, `"https://q.example/x" <file:///tmp/a.txt>`, []string{"https://q.example/x", "file:///tmp/a.txt"}},
		{linkURL, "nothttps://x and https:// alone", []string{}},
	}
	patterns := map[linkKind]*regexp.Regexp{}
	for _, p := range linkPatterns {
		patterns[p.kind] = regexp.MustCompile("(?i)" + p.pattern)
	}
	for _, c := range cases {
		got := patterns[c.kind].FindAllString(c.line, -1)
		if len(got) != len(c.want) {
			t.Errorf("%q: got %q, want %q", c.line, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%q: got %q, want %q", c.line, got, c.want)
				break
			}
		}
	}
}

func TestResolveLink(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		m        vte.Match
		kind     linkKind
		cwd      string
		remote   bool
		wantURI  string
		wantText string
	}{
		{"url", vte.Match{Text: "https://example.com/a"}, linkURL, "", false,
			"https://example.com/a", "https://example.com/a"},
		{"hyperlink", vte.Match{Text: "https://example.com/b", Hyperlink: true}, linkURL, "", true,
			"https://example.com/b", "https://example.com/b"},
		{"markdown url", vte.Match{Text: `[a](https://x.org/p "T")`}, linkMarkdown, "/w", true,
			"https://x.org/p", "https://x.org/p"},
		{"relative path", vte.Match{Text: "![s](img/a%20b.png)"}, linkMarkdown, "/w/repo", false,
			"file:///w/repo/img/a%20b.png", "/w/repo/img/a b.png"},
		{"angle brackets", vte.Match{Text: "[f](<docs/my file.md>)"}, linkMarkdown, "/w", false,
			"file:///w/docs/my%20file.md", "/w/docs/my file.md"},
		{"absolute path", vte.Match{Text: "[f](/etc/hosts#top)"}, linkMarkdown, "", false,
			"file:///etc/hosts", "/etc/hosts"},
		{"home path", vte.Match{Text: "[f](~/notes.md)"}, linkMarkdown, "/w", false,
			"file://" + filepath.Join(home, "notes.md"), filepath.Join(home, "notes.md")},
		{"remote path", vte.Match{Text: "[f](docs/a.md)"}, linkMarkdown, "/w", true, "", "docs/a.md"},
		{"no cwd", vte.Match{Text: "[f](docs/a.md)"}, linkMarkdown, "", false, "", "docs/a.md"},
		{"anchor", vte.Match{Text: "[f](#usage)"}, linkMarkdown, "/w", false, "", "#usage"},
		{"unsafe scheme", vte.Match{Text: "[x](javascript:alert(1))"}, linkMarkdown, "/w", false, "", "javascript:alert(1)"},
		{"hyperlink scheme", vte.Match{Text: "vscode://open", Hyperlink: true}, linkURL, "", false, "", "vscode://open"},
	}
	for _, c := range cases {
		l := resolveLink(c.m, c.kind, c.cwd, c.remote)
		if l.uri != c.wantURI || l.text != c.wantText {
			t.Errorf("%s: got %+v, want uri %q text %q", c.name, l, c.wantURI, c.wantText)
		}
	}
}

func TestLinkInLines(t *testing.T) {
	url := "https://example.com/" + strings.Repeat("a", 50) // 70 cells
	lines := []string{
		"$ cat notes.md",
		"see " + url + " for more", // 83 cells: rows 1 to 5 at width 20
		"",
		"[文档](docs/中文.md) wide", // wide characters take two cells: 25 cells, rows 7 and 8
	}
	cases := []struct {
		col, row int
		want     string
		kind     linkKind
	}{
		{5, 0, "", 0},
		{4, 1, url, linkURL},  // the link's first cell
		{3, 1, "", 0},         // the space before it
		{10, 3, url, linkURL}, // its third row
		{13, 4, url, linkURL}, // its last cell
		{14, 4, "", 0},        // the space after it
		{0, 5, "", 0},         // the line's last row
		{0, 6, "", 0},         // the empty line
		{0, 7, "[文档](docs/中文.md)", linkMarkdown},
		{2, 7, "[文档](docs/中文.md)", linkMarkdown},  // the second cell of 文
		{19, 7, "[文档](docs/中文.md)", linkMarkdown}, // the closing parenthesis
		{0, 8, "", 0},
		{10, 8, "", 0}, // past the end of the line
		{0, 9, "", 0},  // below every line
	}
	for _, c := range cases {
		text, kind, ok := linkInLines(lines, 20, c.col, c.row)
		if text != c.want || ok != (c.want != "") || (ok && kind != c.kind) {
			t.Errorf("cell %d,%d: got %q kind %d ok %v, want %q kind %d", c.col, c.row, text, kind, ok, c.want, c.kind)
		}
	}
}
