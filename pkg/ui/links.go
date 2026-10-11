package ui

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

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
// can check them. A pattern matches within one line: tmux draws each line separately.
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
	// punctuation, which is usually the sentence's.
	{linkURL, `\b(?:https?|ftp|file)://(?:[^\s()<>"'\[\]{}]|\([^\s()<>]*\))*(?:[^\s()<>"'\[\]{}.,;:!?]|\([^\s()<>]*\))`},
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
// remote says that's on another host, where a path can't be opened from here.
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
	if remote || u.Path == "" || u.Host != "" {
		return l
	}
	path := u.Path
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return l
		}
		path = filepath.Join(home, rest)
	} else if !filepath.IsAbs(path) {
		if cwd == "" {
			return l
		}
		path = filepath.Join(cwd, path)
	}
	l.uri = (&url.URL{Scheme: "file", Path: path}).String()
	l.text = path
	return l
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
