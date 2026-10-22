package ui

import (
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
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
		{linkPath, "see /var/log/syslog, ~/notes.md and ./out/a.tar.gz.",
			[]string{"/var/log/syslog", "~/notes.md", "./out/a.tar.gz"}},
		{linkPath, "pkg/ui/x.go:12:5: error in ../lib/y.c:3", []string{"../lib/y.c:3"}},
		{linkPath, `--out=/tmp/x "/q/a b" (/r/c) '/s/d'`, []string{"/tmp/x", "/q/a", "/r/c", "/s/d"}},
		{linkPath, "and/or 10/22/2026 +/- w/o / // ./ ~/ a /", []string{}},
		{linkPath, "/a and /usr/lib64/", []string{"/a", "/usr/lib64/"}},
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

func TestLinkSpan(t *testing.T) {
	url := "https://example.com/" + strings.Repeat("a", 50) // 70 cells
	lines := []string{"$ cat notes.md", "see " + url + " for more", "[文档](a.md)"}
	_, _, span, ok := linkInLines(lines, 20, 10, 2)
	if !ok {
		t.Fatal("no link")
	}
	// The URL runs from cell 4 of row 1 to cell 13 of row 4.
	for _, c := range []struct {
		col, row int
		in       bool
	}{
		{4, 1, true}, {3, 1, false}, {19, 2, true}, {0, 3, true}, {13, 4, true}, {14, 4, false},
		{0, 0, false}, {0, 5, false}, {20, 1, false}, {-1, 2, false},
	} {
		if got := span.contains(c.col, c.row); got != c.in {
			t.Errorf("cell %d,%d: got %v", c.col, c.row, got)
		}
	}
	// A span on the screen, where the pane starts at column 30, row 2.
	span.left, span.top = 30, span.top+2
	if !span.contains(34, 3) || span.contains(4, 1) {
		t.Error("span not moved with its pane")
	}
	// Wide characters take two cells: the markdown link is 12 cells, on row 6.
	if _, _, span, ok = linkInLines(lines, 20, 11, 6); !ok || span.start != 0 || span.end != 12 || span.top != 6 {
		t.Errorf("wide link: %+v %v", span, ok)
	}
	if (cellSpan{}).contains(0, 0) {
		t.Error("the zero span contains a cell")
	}
}

func TestResolveLink(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	homeNotes := path.Join(filepath.ToSlash(home), "notes.md")
	cases := []struct {
		name     string
		m        vte.Match
		kind     linkKind
		cwd      string
		remote   bool
		wantURI  string
		wantText string
		wantFile string
	}{
		{"url", vte.Match{Text: "https://example.com/a"}, linkURL, "", false,
			"https://example.com/a", "https://example.com/a", ""},
		{"hyperlink", vte.Match{Text: "https://example.com/b", Hyperlink: true}, linkURL, "", true,
			"https://example.com/b", "https://example.com/b", ""},
		{"markdown url", vte.Match{Text: `[a](https://x.org/p "T")`}, linkMarkdown, "/w", true,
			"https://x.org/p", "https://x.org/p", ""},
		{"relative path", vte.Match{Text: "![s](img/a%20b.png)"}, linkMarkdown, "/w/repo", false,
			"file:///w/repo/img/a%20b.png", "/w/repo/img/a b.png", "/w/repo/img/a b.png"},
		{"angle brackets", vte.Match{Text: "[f](<docs/my file.md>)"}, linkMarkdown, "/w", false,
			"file:///w/docs/my%20file.md", "/w/docs/my file.md", "/w/docs/my file.md"},
		{"absolute path", vte.Match{Text: "[f](/etc/hosts#top)"}, linkMarkdown, "", false,
			"file:///etc/hosts", "/etc/hosts", "/etc/hosts"},
		{"home path", vte.Match{Text: "[f](~/notes.md)"}, linkMarkdown, "/w", false,
			"file://" + homeNotes, homeNotes, homeNotes},
		{"remote path", vte.Match{Text: "[f](docs/a.md)"}, linkMarkdown, "/w", true, "", "docs/a.md", "/w/docs/a.md"},
		{"remote home path", vte.Match{Text: "![i](~/a.png)"}, linkMarkdown, "/w", true, "", "~/a.png", "~/a.png"},
		{"remote, no cwd", vte.Match{Text: "[f](docs/a.md)"}, linkMarkdown, "", true, "", "docs/a.md", ""},
		{"no cwd", vte.Match{Text: "[f](docs/a.md)"}, linkMarkdown, "", false, "", "docs/a.md", ""},
		{"anchor", vte.Match{Text: "[f](#usage)"}, linkMarkdown, "/w", false, "", "#usage", ""},
		{"unsafe scheme", vte.Match{Text: "[x](javascript:alert(1))"}, linkMarkdown, "/w", false, "", "javascript:alert(1)", ""},
		{"hyperlink scheme", vte.Match{Text: "vscode://open", Hyperlink: true}, linkURL, "", false, "", "vscode://open", ""},
		// A hyperlink made of a URL written as [https://...], bracket and all.
		{"hyperlink with a closing bracket", vte.Match{Text: "https://ghe.example.com/o/r/pull/125194]", Hyperlink: true},
			linkURL, "", false, "https://ghe.example.com/o/r/pull/125194", "https://ghe.example.com/o/r/pull/125194", ""},
		{"hyperlink to an IPv6 host", vte.Match{Text: "http://[::1]:8080/a", Hyperlink: true}, linkURL, "", false,
			"http://[::1]:8080/a", "http://[::1]:8080/a", ""},
		{"hyperlink to an IPv6 host, bracketed", vte.Match{Text: "http://[::1]]", Hyperlink: true}, linkURL, "", false,
			"http://[::1]", "http://[::1]", ""},
		{"path", vte.Match{Text: "/var/log/a#1?.log"}, linkPath, "/w", false,
			"file:///var/log/a%231%3F.log", "/var/log/a#1?.log", "/var/log/a#1?.log"},
		{"relative path with a line", vte.Match{Text: "./src/main.go:12:5"}, linkPath, "/w", false,
			"file:///w/src/main.go", "/w/src/main.go", "/w/src/main.go"},
		{"home path", vte.Match{Text: "~/notes.md"}, linkPath, "/w", false,
			"file://" + homeNotes, homeNotes, homeNotes},
		{"remote path", vte.Match{Text: "../a.tar:3"}, linkPath, "/w/x", true, "", "../a.tar", "/w/a.tar"},
		{"remote home path", vte.Match{Text: "~/a.log"}, linkPath, "/w", true, "", "~/a.log", "~/a.log"},
		{"file url", vte.Match{Text: "file:///tmp/a.png"}, linkURL, "", false,
			"file:///tmp/a.png", "file:///tmp/a.png", "/tmp/a.png"},
		{"ls hyperlink", vte.Match{Text: "file://" + hostname + "/tmp/b.png", Hyperlink: true}, linkURL, "", false,
			"file://" + hostname + "/tmp/b.png", "file://" + hostname + "/tmp/b.png", "/tmp/b.png"},
		{"other host's file", vte.Match{Text: "file://elsewhere.invalid/tmp/c.png", Hyperlink: true}, linkURL, "", false,
			"file://elsewhere.invalid/tmp/c.png", "file://elsewhere.invalid/tmp/c.png", ""},
		{"remote ls hyperlink", vte.Match{Text: "file://far/tmp/d.png", Hyperlink: true}, linkURL, "", true,
			"file://far/tmp/d.png", "file://far/tmp/d.png", "/tmp/d.png"},
	}
	for _, c := range cases {
		if runtime.GOOS == "windows" {
			// Paths are copied, not opened or read, until they're translated to Windows
			// paths.
			if !strings.HasPrefix(c.m.Text, "file:") && strings.HasPrefix(c.wantURI, "file:") {
				c.wantURI, c.wantText = "", markdownDestination(c.m.Text)
			}
			c.wantFile = ""
		}
		l := resolveLink(c.m, c.kind, c.cwd, c.remote)
		if l.uri != c.wantURI || l.text != c.wantText || l.file != c.wantFile {
			t.Errorf("%s: got %+v, want uri %q text %q file %q", c.name, l, c.wantURI, c.wantText, c.wantFile)
		}
	}
}

func TestLinkPatternsSkipBrackets(t *testing.T) {
	const url = "https://ghe.example.com/o/r/pull/125194"
	for _, line := range []string{"[" + url + "]", "see [" + url + "] here", "(" + url + ")."} {
		text, kind, _, ok := linkInLines([]string{line}, 200, 10, 0) //nolint:mnd
		if !ok || kind != linkURL || text != url {
			t.Errorf("%q: got %q, %v, %v; want %q", line, text, kind, ok, url)
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
		text, kind, span, ok := linkInLines(lines, 20, c.col, c.row)
		if text != c.want || ok != (c.want != "") || (ok && kind != c.kind) {
			t.Errorf("cell %d,%d: got %q kind %d ok %v, want %q kind %d", c.col, c.row, text, kind, ok, c.want, c.kind)
		}
		if ok && !span.contains(c.col, c.row) {
			t.Errorf("cell %d,%d: not in the link's span %+v", c.col, c.row, span)
		}
	}
}

func TestMissingFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file links hold POSIX paths, and Windows doesn't open them yet")
	}
	dir := t.TempDir()
	there := filepath.Join(dir, "there.md")
	if err := os.WriteFile(there, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(dir, "gone dir", "a.md")
	cases := []struct {
		uri      string
		wantPath string
		want     bool
	}{
		{"file://" + there, "", false},
		{"file://" + dir, "", false},
		{(&url.URL{Scheme: "file", Path: gone}).String(), gone, true},
		{"file://localhost" + filepath.Join(dir, "x"), filepath.Join(dir, "x"), true},
		{"file://otherhost/nope", "", false},
		{"https://example.com/nope", "", false},
	}
	for _, c := range cases {
		path, ok := missingFile(c.uri)
		if path != c.wantPath || ok != c.want {
			t.Errorf("%s: got %q %v, want %q %v", c.uri, path, ok, c.wantPath, c.want)
		}
	}
}
