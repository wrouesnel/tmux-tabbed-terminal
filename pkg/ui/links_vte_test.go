//go:build !windows

package ui

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gotk3/gotk3/gtk"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/vte"
)

// vteTestEnv makes TestLinkPatternsInVTE run its checks, rather than run itself again
// on a virtual X server.
const vteTestEnv = "TTT_TEST_VTE"

// pumpGTK runs the GTK main loop for a while, so a terminal processes what it was fed.
func pumpGTK(d time.Duration) {
	for deadline := time.Now().Add(d); time.Now().Before(deadline); {
		gtk.MainIterationDo(false)
		time.Sleep(5 * time.Millisecond) //nolint:mnd
	}
}

// TestLinkPatternsInVTE checks the link patterns with VTE's own regex engine, PCRE2,
// which finds no match where Go's would if a pattern backtracks too much, as on long
// links. Lines here are wrapped by the terminal, as tmux leaves them to be.
func TestLinkPatternsInVTE(t *testing.T) {
	if os.Getenv(vteTestEnv) == "" {
		if _, err := exec.LookPath("xvfb-run"); err != nil {
			t.Skip("xvfb-run is not installed")
		}
		cmd := exec.Command("xvfb-run", "-a", os.Args[0], "-test.run", "^TestLinkPatternsInVTE$", "-test.v") //nolint:gosec // test binary
		cmd.Env = append(os.Environ(), vteTestEnv+"=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("on a virtual X server: %v\n%s", err, out)
		}
		return
	}

	const cols, rows = 80, 10
	runtime.LockOSThread()
	gtk.Init(nil)
	longURL := "https://example.com/" + strings.Repeat("a/", 150) + "end.html"
	longPath := "docs/" + strings.Repeat("d", 200) + ".md"
	wiki := "https://en.wikipedia.org/wiki/" + strings.Repeat("x", 100) + "_(software)"
	cases := []struct {
		name     string
		text     string
		col, row int
		want     string
		kind     linkKind
	}{
		{"short url", "see https://example.com/s.", 6, 0, "https://example.com/s", linkURL},
		{"long url, first row", longURL, 5, 0, longURL, linkURL},
		{"long url, last row", longURL, 2, 4, longURL, linkURL},
		{"url after a wrapped line", strings.Repeat("z", 100) + "\r\n" + strings.Repeat("0", 75) + " https://ex.com/wrapped",
			3, 3, "https://ex.com/wrapped", linkURL},
		{"wrapped parentheses", "(" + wiki + ").", 3, 1, wiki, linkURL},
		{"long markdown, second row", "[docs](" + longPath + ")", 10, 1, "[docs](" + longPath + ")", linkMarkdown},
	}
	for _, c := range cases {
		win, err := gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
		if err != nil {
			t.Fatal(err)
		}
		term := vte.New()
		win.Add(term)
		term.SetSize(cols, rows)
		tags, err := addLinks(term)
		if err != nil {
			t.Fatalf("addLinks: %v", err)
		}
		win.ShowAll()
		pumpGTK(100 * time.Millisecond) //nolint:mnd
		term.Feed([]byte(c.text + "\r\n"))
		pumpGTK(200 * time.Millisecond) //nolint:mnd
		m, ok := term.MatchAtCell(c.col, c.row)
		switch {
		case !ok:
			t.Errorf("%s: no match at %d,%d", c.name, c.col, c.row)
		case m.Text != c.want || tags[m.Tag] != c.kind:
			t.Errorf("%s: matched %q (kind %d), want %q (kind %d)", c.name, m.Text, tags[m.Tag], c.want, c.kind)
		}
		win.Destroy()
	}
}
