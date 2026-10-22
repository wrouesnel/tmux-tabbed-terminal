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
	// linkPath is a file path: absolute, in the home directory or explicitly relative,
	// such as /var/log/syslog, ~/notes.md or ./out/a.tar, perhaps with a line and column
	// after it, as in compilers' messages.
	linkPath
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
	// Paths last, so the path part of a URL opens as the URL. A path starts with /, ~/,
	// ./ or ../, and not straight after a letter or digit, which \B checks, so that and/or
	// and 10/22 aren't paths. The first character after the slash is a letter, digit, _,
	// . or ~, so +/- isn't one either. Like a URL, it doesn't end in punctuation. \x60 is
	// a backquote.
	{linkPath, `\B(?:~|\.\.?)?/(?:[\w.~][^\s"'<>|;&(){}\[\]\x60]*[^\s"'<>|;&(){}\[\]\x60.,:;!?]|\w)`},
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

// cellSpan is the cells a link covers on the screen: from offset start to end of the
// rows from row top, each width cells wide from column left.
type cellSpan struct {
	left, top, width int
	start, end       int
}

// contains reports whether the span covers a cell. The zero span covers none.
func (s cellSpan) contains(col, row int) bool {
	col, row = col-s.left, row-s.top
	if col < 0 || col >= s.width || row < 0 {
		return false
	}
	i := row*s.width + col
	return i >= s.start && i < s.end
}

// cellsOf is how many cells a string takes on a terminal.
func cellsOf(text string) int {
	cells := 0
	for _, r := range text {
		cells += runeCells(r)
	}
	return cells
}

// linkInLines finds the link at a cell of a pane, given the pane's lines as tmux's
// capture-pane -J returns them, so a line takes as many rows of the pane's width as it
// needs. It returns the link's text, the kind of pattern it matched and the cells of the
// pane it covers.
func linkInLines(lines []string, paneWidth, col, row int) (string, linkKind, cellSpan, bool) {
	if paneWidth <= 0 {
		return "", 0, cellSpan{}, false
	}
	top := 0
	for _, line := range lines {
		rows := max(1, (cellsOf(line)+paneWidth-1)/paneWidth)
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
			return "", 0, cellSpan{}, false
		}
		for i, re := range linkRegexps {
			for _, m := range re.FindAllStringIndex(line, -1) {
				if m[0] <= at && at < m[1] {
					span := cellSpan{top: top, width: paneWidth, start: cellsOf(line[:m[0]]), end: cellsOf(line[:m[1]])}
					return line[m[0]:m[1]], linkPatterns[i].kind, span, true
				}
			}
		}
		return "", 0, cellSpan{}, false
	}
	return "", 0, cellSpan{}, false
}

// linkSchemes are the URI schemes links may open. Others, such as javascript:, are only
// copied.
//
//nolint:gochecknoglobals
var linkSchemes = map[string]bool{"http": true, "https": true, "ftp": true, "mailto": true, "file": true}

// link is a link in a terminal: the URI it opens, or "" if it can't be opened from here,
// the text Copy Link copies, and the path of the file it names on the session's host, or
// "". A path on another host may start with ~/, for the home directory there. span is
// where it is on the screen, when tmux was asked, as it is in split windows. host is the
// name of the session's host.
type link struct {
	uri  string
	text string
	file string
	host string
	span cellSpan
}

// downloadable reports whether the link names a file on another host, which can be
// copied here.
func (l link) downloadable() bool {
	return l.file != "" && l.host != "" && l.host != LocalHost
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

// trimUnbalancedBrackets removes closing square brackets from the end of a URL which no
// opening one matches. A URL holds square brackets only around an IPv6 host, so these
// are the text around the link: some programs which make hyperlinks of the URLs they
// print include the bracket after one written as [https://example.com].
func trimUnbalancedBrackets(s string) string {
	for strings.HasSuffix(s, "]") && strings.Count(s, "]") > strings.Count(s, "[") {
		s = s[:len(s)-1]
	}
	return s
}

// resolveLink returns the link a terminal match is. Markdown destinations without a
// scheme are paths, relative to cwd, the directory of the program which printed them.
// remote says that's on another host, where a path can't be opened from here, though the
// file can still be read through the host's connection. The paths are tmux's, so POSIX
// ones on every platform: on Windows, those of MSYS2 or Cygwin, which aren't used until
// they can be translated to Windows paths.
func resolveLink(m vte.Match, kind linkKind, cwd string, remote bool) link {
	dest := m.Text
	switch {
	case m.Hyperlink:
		dest = trimUnbalancedBrackets(dest)
	case kind == linkMarkdown:
		dest = markdownDestination(m.Text)
	case kind == linkPath:
		// A path is taken as it is, not as a URL, whose ? and # it may hold.
		dest = lineSuffix.ReplaceAllString(dest, "")
		if runtime.GOOS == "windows" {
			return link{text: dest}
		}
		return resolvePath(link{text: dest}, dest, cwd, remote)
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
		// ls --hyperlink names the host the file is on.
		if strings.EqualFold(u.Scheme, "file") && runtime.GOOS != "windows" && (remote || isThisHost(u.Host)) {
			l.file = u.Path
		}
		return l
	}
	if runtime.GOOS == "windows" || u.Path == "" || u.Host != "" {
		return l
	}
	return resolvePath(l, u.Path, cwd, remote)
}

// lineSuffix is the line, and perhaps column, after a path in compilers' messages, such
// as :12 or :12:5.
//
//nolint:gochecknoglobals
var lineSuffix = regexp.MustCompile(`(?::\d+){1,2}$`)

// resolvePath sets the file, and on this host the URI, of a link to path p, which is
// relative to cwd unless it's absolute or starts with ~/. On another host, it's left
// starting with ~/ for the home directory there.
func resolvePath(l link, p, cwd string, remote bool) link {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if remote {
			l.file = p
			return l
		}
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
	l.file = p
	if remote {
		return l
	}
	l.uri = (&url.URL{Scheme: "file", Path: p}).String()
	l.text = p
	return l
}

// isThisHost reports whether a file URI's host is this machine.
func isThisHost(host string) bool {
	if host == "" || strings.EqualFold(host, "localhost") {
		return true
	}
	name, err := os.Hostname()
	return err == nil && strings.EqualFold(host, name)
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
