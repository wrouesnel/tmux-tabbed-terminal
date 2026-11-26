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
		{linkCode, "edit `pkg/ui/links.go`, `README.md` and `main.go:12`; see `packaging/windows/`.",
			[]string{"`pkg/ui/links.go`", "`README.md`", "`main.go:12`", "`packaging/windows/`"}},
		{linkCode, "`./out/a.tar` and `~/notes.md` and `../x`", []string{"`./out/a.tar`", "`~/notes.md`", "`../x`"}},
		// Code which isn't a path, and spans which hold spaces or start with a slash.
		{linkCode, "run `go test`, set `x = 1.5`, `v1.2` and `a b/c` and `/etc/hosts` and ``", []string{}},
		{linkRelative, "pkg/ui/x.go:12:5: error in ../lib/y.c:3", []string{"pkg/ui/x.go:12:5", "lib/y.c:3"}},
		{linkRelative, "see docs/README.md, src/a-b/c_d.test.ts and .github/workflows/ci.yml.",
			[]string{"docs/README.md", "src/a-b/c_d.test.ts", ".github/workflows/ci.yml"}},
		{linkRelative, "and/or w/o TCP/IP 10/22/2026 1/2.5 a/b README.md v1.2/3", []string{}},
	}
	patterns := map[linkKind]*regexp.Regexp{}
	needs := map[linkKind]string{}
	for _, p := range linkPatterns {
		patterns[p.kind] = regexp.MustCompile("(?i)" + p.pattern)
		needs[p.kind] = p.needs
	}
	for _, c := range cases {
		got := patterns[c.kind].FindAllString(c.line, -1)
		// A line is only searched for a pattern if it holds what every match does.
		for _, m := range got {
			if !strings.Contains(m, needs[c.kind]) {
				t.Errorf("%q: match %q lacks %q, so a line of it wouldn't be searched", c.line, m, needs[c.kind])
			}
		}
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
		{"code span", vte.Match{Text: "`pkg/ui/links.go`"}, linkCode, "/w/repo", false,
			"file:///w/repo/pkg/ui/links.go", "/w/repo/pkg/ui/links.go", "/w/repo/pkg/ui/links.go"},
		{"code span with a line", vte.Match{Text: "`main.go:12`"}, linkCode, "/w", false,
			"file:///w/main.go", "/w/main.go", "/w/main.go"},
		{"code span, home", vte.Match{Text: "`~/notes.md`"}, linkCode, "/w", false,
			"file://" + homeNotes, homeNotes, homeNotes},
		{"remote code span", vte.Match{Text: "`docs/a.md`"}, linkCode, "/w", true, "", "docs/a.md", "/w/docs/a.md"},
		{"code span, no cwd", vte.Match{Text: "`docs/a.md`"}, linkCode, "", false, "", "docs/a.md", ""},
		{"relative path", vte.Match{Text: "pkg/ui/x.go:12:5"}, linkRelative, "/w/repo", false,
			"file:///w/repo/pkg/ui/x.go", "/w/repo/pkg/ui/x.go", "/w/repo/pkg/ui/x.go"},
		{"remote relative path", vte.Match{Text: "src/a.c:3"}, linkRelative, "/w", true, "", "src/a.c", "/w/src/a.c"},
		{"file url", vte.Match{Text: "file:///tmp/a.png"}, linkURL, "", false,
			"file:///tmp/a.png", "file:///tmp/a.png", "/tmp/a.png"},
		{"ls hyperlink", vte.Match{Text: "file://" + hostname + "/tmp/b.png", Hyperlink: true}, linkURL, "", false,
			"file://" + hostname + "/tmp/b.png", "file://" + hostname + "/tmp/b.png", "/tmp/b.png"},
		{"other host's file", vte.Match{Text: "file://elsewhere.invalid/tmp/c.png", Hyperlink: true}, linkURL, "", false,
			"file://elsewhere.invalid/tmp/c.png", "file://elsewhere.invalid/tmp/c.png", ""},
		{"remote ls hyperlink", vte.Match{Text: "file://far/tmp/d.png", Hyperlink: true}, linkURL, "", true,
			"", "file://far/tmp/d.png", "/tmp/d.png"},
		// On another host, even a file URI naming this one is that host's file.
		{"remote hyperlink to this host", vte.Match{Text: "file://" + hostname + "/tmp/e.go", Hyperlink: true}, linkURL, "", true,
			"", "file://" + hostname + "/tmp/e.go", "/tmp/e.go"},
		{"remote file url", vte.Match{Text: "[f](file:///w/a.txt)"}, linkMarkdown, "/w", true,
			"", "file:///w/a.txt", "/w/a.txt"},
	}
	for _, c := range cases {
		if runtime.GOOS == "windows" && !c.remote {
			// Local paths are copied, not opened or read, until they're translated to
			// Windows paths. Paths on other hosts are theirs, so they're unchanged.
			if !strings.HasPrefix(c.m.Text, "file:") && strings.HasPrefix(c.wantURI, "file:") {
				c.wantURI, c.wantText = "", markdownDestination(c.m.Text)
				if c.kind == linkPath || c.kind == linkCode || c.kind == linkRelative {
					c.wantText = trimLineSuffix(strings.Trim(c.m.Text, "`"))
				}
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

// TestLinkPatternOrder checks which pattern a link matches where several could.
func TestLinkPatternOrder(t *testing.T) {
	cases := []struct {
		line string
		col  int
		want string
		kind linkKind
	}{
		{"error in ../lib/y.c:3", 13, "../lib/y.c:3", linkPath},
		{"see `/etc/hosts` here", 8, "/etc/hosts", linkPath},
		{"see `https://x.org/a.md` here", 10, "https://x.org/a.md", linkURL},
		{"see `src/a.go` here", 8, "`src/a.go`", linkCode},
		{"see [a](src/a.go) here", 10, "[a](src/a.go)", linkMarkdown},
		{"src/a.go:3:1: oops", 2, "src/a.go:3:1", linkRelative},
	}
	for _, c := range cases {
		text, kind, _, ok := linkInLines([]string{c.line}, 200, c.col, 0) //nolint:mnd
		if !ok || text != c.want || kind != c.kind {
			t.Errorf("%q at %d: got %q kind %d ok %v, want %q kind %d", c.line, c.col, text, kind, ok, c.want, c.kind)
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

func TestLinkInLinesBrokenAtEdge(t *testing.T) {
	lines := []string{
		"open /home/user/proj", // fills the row: goes on in the next line
		"ects/notes.md now",
		"see https://example.", // fills the row, but the next line starts with a space
		"  com/a",
		"x https://example.co", // broken twice
		"m/aaaaaaaaaaaaaaaaaa",
		"aa/b.md",
		"short /tmp/a.md",
	}
	path := "/home/user/projects/notes.md"
	url := "https://example.com/aaaaaaaaaaaaaaaaaaaa/b.md"
	cases := []struct {
		col, row int
		want     string
	}{
		{5, 0, path},              // the first part
		{19, 0, path},             // the last cell of the row
		{0, 1, path},              // the part after the break
		{12, 1, path},             // its last cell
		{14, 1, ""},               // the word after it
		{4, 2, "https://example"}, // a trailing dot is the sentence's
		{3, 3, ""},
		{2, 4, url},
		{10, 5, url},
		{6, 6, url},
		{7, 6, ""},
		{6, 7, "/tmp/a.md"}, // rows still line up after the joined lines
	}
	for _, c := range cases {
		text, _, span, ok := linkInLines(lines, 20, c.col, c.row)
		if text != c.want || ok != (c.want != "") {
			t.Errorf("cell %d,%d: got %q ok %v, want %q", c.col, c.row, text, ok, c.want)
		}
		if ok && !span.contains(c.col, c.row) {
			t.Errorf("cell %d,%d: not in the link's span %+v", c.col, c.row, span)
		}
	}
}

func TestLinkReadings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("local paths aren't links on Windows")
	}
	host, _ := os.Hostname()
	home, _ := os.UserHomeDir()
	cases := []struct {
		name              string
		m                 vte.Match
		kind              linkKind
		host              string
		uri, file         string
		remoteFile, local string
	}{
		{"remote path", vte.Match{Text: "/srv/a.md"}, linkPath, "box", "", "/srv/a.md", "/srv/a.md", "file:///srv/a.md"},
		{"remote relative path", vte.Match{Text: "out/b.png"}, linkRelative, "box", "", "/w/out/b.png", "/w/out/b.png", "file:///w/out/b.png"},
		{"remote home path", vte.Match{Text: "~/c.md"}, linkPath, "box", "", "~/c.md", "~/c.md", "file://" + home + "/c.md"},
		{"remote hyperlink naming this machine", vte.Match{Text: "file://" + host + "/srv/d.md", Hyperlink: true}, 0, "box",
			"", "/srv/d.md", "/srv/d.md", "file:///srv/d.md"},
		{"remote hyperlink naming another", vte.Match{Text: "file://elsewhere/srv/e.md", Hyperlink: true}, 0, "box",
			"", "/srv/e.md", "/srv/e.md", ""},
		{"local path", vte.Match{Text: "/srv/a.md"}, linkPath, LocalHost, "file:///srv/a.md", "/srv/a.md", "", "file:///srv/a.md"},
		{"remote url", vte.Match{Text: "https://example.com/x"}, linkURL, "box", "https://example.com/x", "", "", ""},
	}
	for _, c := range cases {
		l := linkReadings(c.m, c.kind, "/w", c.host)
		if l.uri != c.uri || l.file != c.file || l.remoteFile != c.remoteFile || l.localURI != c.local || l.host != c.host {
			t.Errorf("%s: got uri %q file %q remote %q local %q host %q, want %q %q %q %q %q", c.name,
				l.uri, l.file, l.remoteFile, l.localURI, l.host, c.uri, c.file, c.remoteFile, c.local, c.host)
		}
	}
}

func TestRemoteFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("local paths aren't links on Windows")
	}
	dir := t.TempDir()
	here := filepath.Join(dir, "here.md")
	if err := os.WriteFile(here, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(dir, "gone.md")
	uri := func(p string) string { return (&url.URL{Scheme: "file", Path: p}).String() }
	cases := []struct {
		name string
		l    link
		want bool
	}{
		{"missing here, from a remote session", link{uri: uri(gone), host: "box", remoteFile: gone}, true},
		{"there here", link{uri: uri(here), host: "box", remoteFile: here}, false},
		{"missing, from a local session", link{uri: uri(gone), host: LocalHost}, false},
		{"a web page", link{uri: "https://example.com/", host: "box", remoteFile: "/x"}, false},
	}
	for _, c := range cases {
		if _, got := remoteFallback(c.l); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
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

// TestTrimLineSuffix checks trimLineSuffix strips what the regexp it replaced did.
func TestTrimLineSuffix(t *testing.T) {
	lineSuffix := regexp.MustCompile(`(?::\d+){1,2}$`)
	for _, s := range []string{
		"", "a", "a:", "a:1", "a:12:5", "a:1:2:3", ":1", "1", "a:b", "a:1b", "a::1", "C:\\x:3",
		"/p/f.go:10:", "f.go:7:9", "x:12:ab", "::",
	} {
		if got, want := trimLineSuffix(s), lineSuffix.ReplaceAllString(s, ""); got != want {
			t.Errorf("trimLineSuffix(%q) = %q, want %q", s, got, want)
		}
	}
}
