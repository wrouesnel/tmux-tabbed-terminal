package ui

import (
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path"
	"regexp"
	"runtime"
	"strings"
	"unicode"

	"golang.org/x/text/width"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/vte"
)

// linkKind is the kind of text a terminal link pattern matches.
type linkKind int

const (
	// linkURL is a bare URL, such as https://example.com/a.
	linkURL linkKind = iota
	// linkMarkdown is a markdown link or image, such as [label](url) or ![alt](a.png).
	linkMarkdown
)

// Link patterns, matched by VTE with PCRE2. They're also valid Go regexps, so the tests
// can check them. VTE matches across lines the terminal wrapped, which includes lines
// tmux leaves the terminal to wrap: those of a pane as wide as the window.
//
// PCRE2 gives up, and finds no match, when a repeated group runs to a few dozen
// repetitions, so a pattern mustn't repeat a group once per character: repeat a character
// class instead. Go's regexps have no such limit, so a VTE test checks long links.
//
//nolint:gochecknoglobals
var linkPatterns = []struct {
	kind    linkKind
	pattern string
}{
	// Markdown first, so a URL inside a markdown link opens as the markdown link. The
	// destination is a URL or path, or anything in angle brackets, and may be followed by
	// a quoted title.
	{linkMarkdown, `!?\[[^\[\]\n]*\]\((?:<[^<>\n]+>|[^\s()<>]+)(?:\s+"[^"\n]*")?\)`},
	// A URL may hold balanced parentheses, as Wikipedia's do, but doesn't end in
	// punctuation, which is usually the sentence's. The group repeats once per
	// parenthesis, not per character.
	{linkURL, `\b(?:https?|ftp|file)://(?:[^\s()<>"'\[\]{}]*(?:\([^\s()<>]*\)|[^\s()<>"'\[\]{}.,;:!?]))+`},
}

// linkRegexps are the link patterns compiled with Go's regexps, for finding links in
// tmux's copy of a pane's lines, case-insensitively as VTE does.
//
//nolint:gochecknoglobals
var linkRegexps = func() []*regexp.Regexp {
	res := make([]*regexp.Regexp, len(linkPatterns))
	for i, p := range linkPatterns {
		res[i] = regexp.MustCompile("(?i)" + p.pattern)
	}
	return res
}()

// runeCells is how many cells a character takes on a terminal: two for East Asian wide
// ones, none for combining marks.
func runeCells(r rune) int {
	switch {
	case unicode.Is(unicode.Mn, r):
		return 0
	case width.LookupRune(r).Kind() == width.EastAsianWide, width.LookupRune(r).Kind() == width.EastAsianFullwidth:
		return 2 //nolint:mnd // a wide character
	}
	return 1
}

// linkInLines finds the link at a cell of a pane, given the pane's lines as tmux's
// capture-pane -J returns them, so a line takes as many rows of the pane's width as it
// needs. It returns the link's text and the kind of pattern it matched.
func linkInLines(lines []string, paneWidth, col, row int) (string, linkKind, bool) {
	if paneWidth <= 0 {
		return "", 0, false
	}
	top := 0
	for _, line := range lines {
		cells := 0
		for _, r := range line {
			cells += runeCells(r)
		}
		rows := max(1, (cells+paneWidth-1)/paneWidth)
		if row >= top+rows {
			top += rows
			continue
		}
		// The byte offset of the character at the cell.
		target, at := (row-top)*paneWidth+col, -1
		cell := 0
		for i, r := range line {
			if n := runeCells(r); cell+n > target {
				at = i
				break
			} else {
				cell += n
			}
		}
		if at < 0 {
			return "", 0, false
		}
		for i, re := range linkRegexps {
			for _, m := range re.FindAllStringIndex(line, -1) {
				if m[0] <= at && at < m[1] {
					return line[m[0]:m[1]], linkPatterns[i].kind, true
				}
			}
		}
		return "", 0, false
	}
	return "", 0, false
}

// linkSchemes are the URI schemes links may open. Others, such as javascript:, are only
// copied.
//
//nolint:gochecknoglobals
var linkSchemes = map[string]bool{"http": true, "https": true, "ftp": true, "mailto": true, "file": true}

// link is a link in a terminal: the URI it opens, or "" if it can't be opened from here,
// and the text Copy Link copies.
type link struct {
	uri  string
	text string
}

// markdownDestination returns the destination of a markdown link, such as a.png in
// ![alt](a.png "title").
func markdownDestination(text string) string {
	i := strings.Index(text, "](")
	if i < 0 || !strings.HasSuffix(text, ")") {
		return ""
	}
	dest := strings.TrimSpace(text[i+2 : len(text)-1])
	if strings.HasPrefix(dest, "<") {
		if end := strings.Index(dest, ">"); end > 0 {
			return dest[1:end]
		}
	}
	if end := strings.IndexAny(dest, " \t"); end >= 0 {
		dest = dest[:end]
	}
	return dest
}

// resolveLink returns the link a terminal match is. Markdown destinations without a
// scheme are paths, relative to cwd, the directory of the program which printed them.
// remote says that's on another host, where a path can't be opened from here. The paths
// are tmux's, so POSIX ones on every platform: on Windows, those of MSYS2 or Cygwin,
// which aren't opened until they can be translated to Windows paths.
func resolveLink(m vte.Match, kind linkKind, cwd string, remote bool) link {
	dest := m.Text
	if !m.Hyperlink && kind == linkMarkdown {
		dest = markdownDestination(m.Text)
	}
	l := link{text: dest}
	u, err := url.Parse(dest)
	if dest == "" || err != nil {
		return l
	}
	if u.Scheme != "" {
		if linkSchemes[strings.ToLower(u.Scheme)] {
			l.uri = dest
		}
		return l
	}
	if remote || runtime.GOOS == "windows" || u.Path == "" || u.Host != "" {
		return l
	}
	p := u.Path
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return l
		}
		p = path.Join(home, rest)
	} else if !path.IsAbs(p) {
		if cwd == "" {
			return l
		}
		p = path.Join(cwd, p)
	}
	l.uri = (&url.URL{Scheme: "file", Path: p}).String()
	l.text = p
	return l
}

// missingFile returns the path of a file:// URI on this host whose file doesn't exist.
// The desktop's handler, often a portal, reports nothing back for those, so the window
// says so itself.
func missingFile(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || !strings.EqualFold(u.Scheme, "file") || (u.Host != "" && u.Host != "localhost") {
		return "", false
	}
	if _, err := os.Stat(u.Path); !errors.Is(err, fs.ErrNotExist) {
		return "", false
	}
	return u.Path, true
}

// addLinks makes a terminal underline links under the pointer: OSC 8 hyperlinks, and
// matches of the link patterns. It returns the kind of each pattern's tag. A pattern VTE
// can't compile is left out, and its error returned.
func addLinks(term *vte.Terminal) (map[int]linkKind, error) {
	term.SetAllowHyperlink(true)
	tags := map[int]linkKind{}
	var firstErr error
	for _, p := range linkPatterns {
		tag, err := term.AddMatch(p.pattern)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		tags[tag] = p.kind
	}
	return tags, firstErr
}
