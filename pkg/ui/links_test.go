package ui

import (
	"os"
	"path/filepath"
	"regexp"
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
