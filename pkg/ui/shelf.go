package ui

import (
	"context"
	"net/url"
	"os"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/gotk3/gotk3/pango"
	"go.uber.org/zap"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/gtkx"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/vte"
)

// The file shelf lists the images, markdown and HTML files a session's output mentions,
// newest first, beside the panes, on the side opposite the session list.
const (
	// shelfWidth is the shelf's width, at least.
	shelfWidth = 240
	// The largest an image's thumbnail is.
	shelfThumbWidth  = 208
	shelfThumbHeight = 156
	// shelfScanDelay is how long after a pane's output changes it's read for files, at
	// most once in that time.
	shelfScanDelay = 1500 * time.Millisecond
	// shelfHistory is how many lines of a pane's history are read the first time its
	// session is read, so files mentioned before are found too.
	shelfHistory = 2000
	// shelfMaxItems is the most files a session's shelf keeps.
	shelfMaxItems = 60
	// shelfTextBytes is how much of the start of a markdown or HTML file is read for its
	// summary, and shelfSnippetChars how long the summary is.
	shelfTextBytes    = 8 << 10
	shelfSnippetChars = 240
	// shelfRetry is how long a file that couldn't be read, perhaps not written yet, is
	// left before it's tried again.
	shelfRetry = 30 * time.Second
	// shelfRecheck is how often a local file mentioned again is checked for changes.
	shelfRecheck = 3 * time.Second
	// shelfDragTarget is the drag type of a file dragged from the shelf. Nothing takes it,
	// so a drop anywhere opens the file in a window of its own.
	shelfDragTarget = "application/x-tmux-tabbed-terminal-shelf-file"
)

// shelfKind is the kind of file on the shelf.
type shelfKind int

const (
	shelfImage shelfKind = iota
	shelfMarkdown
	shelfHTML
)

// shelfKindOf returns the kind of a file, going by its extension, and whether the shelf
// lists that kind.
func shelfKindOf(file string) (shelfKind, bool) {
	switch {
	case isMarkdownPath(file):
		return shelfMarkdown, true
	case isHTMLPath(file):
		return shelfHTML, true
	case isImagePath(file):
		return shelfImage, true
	}
	return 0, false
}

// shelfItem is a file on the shelf: an image's thumbnail, or the title and start of the
// text of a markdown or HTML file.
type shelfItem struct {
	host, file string
	kind       shelfKind
	// seq orders the files by when they were first mentioned, and names the item in
	// menus.
	seq            int
	thumb          *gdk.Pixbuf
	title, snippet string
	// modTime is a local file's modification time when it was read, and checked when it
	// was last checked for changes.
	modTime time.Time
	checked time.Time
}

// link returns the link the item opens, as a click on its path in the terminal would.
func (it *shelfItem) link() link {
	l := link{host: it.host, file: it.file, text: it.file}
	if it.host == LocalHost {
		l.uri = (&url.URL{Scheme: "file", Path: it.file}).String()
	}
	return l
}

// shelfFiles are the files a session has mentioned, newest first, and those being read
// or which couldn't be.
type shelfFiles struct {
	items   []*shelfItem
	pending map[string]bool
	failed  map[string]time.Time
	// scanned is set once the session's history has been read.
	scanned bool
}

// find returns the item for a file, or nil.
func (sf *shelfFiles) find(file string) *shelfItem {
	for _, it := range sf.items {
		if it.file == file {
			return it
		}
	}
	return nil
}

// shelfStore keeps each session's files, by session key, for every window's shelf.
type shelfStore struct {
	app      *App
	sessions map[string]*shelfFiles
	seq      int
}

func newShelfStore(app *App) *shelfStore {
	return &shelfStore{app: app, sessions: map[string]*shelfFiles{}}
}

// get returns a session's files, made empty if it has none yet.
func (s *shelfStore) get(key string) *shelfFiles {
	sf := s.sessions[key]
	if sf == nil {
		sf = &shelfFiles{pending: map[string]bool{}, failed: map[string]time.Time{}}
		s.sessions[key] = sf
	}
	return sf
}

// items returns a session's files, newest first.
func (s *shelfStore) items(key string) []*shelfItem {
	if sf := s.sessions[key]; sf != nil {
		return sf.items
	}
	return nil
}

// item returns a session's item by its seq, or nil.
func (s *shelfStore) item(key, seq string) *shelfItem {
	n, err := strconv.Atoi(seq)
	if err != nil {
		return nil
	}
	for _, it := range s.items(key) {
		if it.seq == n {
			return it
		}
	}
	return nil
}

// notice adds the files a session's output mentions, in the order they appear, to its
// shelf: each new file of a kind the shelf lists, once it's been read. A local file seen
// again is read again if it's changed.
func (s *shelfStore) notice(key, host string, files []string) {
	sf := s.get(key)
	now := timeNow()
	for _, f := range files {
		kind, ok := shelfKindOf(f)
		if !ok || sf.pending[f] {
			continue
		}
		if it := sf.find(f); it != nil {
			s.recheck(key, sf, it, now)
			continue
		}
		if t, ok := sf.failed[f]; ok && now.Sub(t) < shelfRetry {
			continue
		}
		s.seq++
		sf.pending[f] = true
		s.load(key, sf, &shelfItem{host: host, file: f, kind: kind, seq: s.seq}, true)
	}
}

// recheck reads a local file on the shelf again if it's changed since it was read.
func (s *shelfStore) recheck(key string, sf *shelfFiles, it *shelfItem, now time.Time) {
	if it.host != LocalHost || now.Sub(it.checked) < shelfRecheck {
		return
	}
	it.checked = now
	sf.pending[it.file] = true
	runAsync(s.app, func(context.Context) (time.Time, error) {
		info, err := os.Stat(it.file)
		if err != nil {
			return time.Time{}, err
		}
		return info.ModTime(), nil
	}, func(mod time.Time, err error) {
		if err != nil || mod.Equal(it.modTime) {
			delete(sf.pending, it.file)
			return
		}
		s.load(key, sf, it, false)
	})
}

// shelfLoaded is what's read of a file for the shelf.
type shelfLoaded struct {
	thumb          *gdk.Pixbuf
	title, snippet string
	modTime        time.Time
}

// load reads a file for its item in the background, and puts a new one on the shelf, or
// shows a changed one again, once it's read. A new file that can't be read is left off,
// to be tried again after shelfRetry.
func (s *shelfStore) load(key string, sf *shelfFiles, it *shelfItem, isNew bool) {
	h := s.app.host(it.host)
	if h == nil {
		delete(sf.pending, it.file)
		return
	}
	runAsync(s.app, func(ctx context.Context) (shelfLoaded, error) {
		return loadShelfFile(ctx, h, it.file, it.kind)
	}, func(r shelfLoaded, err error) {
		delete(sf.pending, it.file)
		if err != nil {
			if isNew {
				sf.failed[it.file] = timeNow()
			}
			s.app.log.Debug("Not on the shelf", zap.String("file", it.file), zap.Error(err))
			return
		}
		it.thumb, it.title, it.snippet, it.modTime = r.thumb, r.title, r.snippet, r.modTime
		it.checked = timeNow()
		if isNew {
			delete(sf.failed, it.file)
			sf.items = append(sf.items, it)
			sort.SliceStable(sf.items, func(i, j int) bool { return sf.items[i].seq > sf.items[j].seq })
			if len(sf.items) > shelfMaxItems {
				sf.items = sf.items[:shelfMaxItems]
			}
		}
		s.app.shelfChanged(key, isNew)
	})
}

// remove takes a file off a session's shelf. It comes back if it's mentioned again
// after shelfRetry.
func (s *shelfStore) remove(key string, it *shelfItem) {
	sf := s.get(key)
	sf.items = slices.DeleteFunc(sf.items, func(x *shelfItem) bool { return x == it })
	sf.failed[it.file] = timeNow()
	s.app.shelfChanged(key, false)
}

// clear takes every file off a session's shelf.
func (s *shelfStore) clear(key string) {
	sf := s.get(key)
	now := timeNow()
	for _, it := range sf.items {
		sf.failed[it.file] = now
	}
	sf.items = nil
	s.app.shelfChanged(key, false)
}

// loadShelfFile reads a file from a host for the shelf: an image's thumbnail, or a
// markdown or HTML file's title and the start of its text.
func loadShelfFile(ctx context.Context, h *Host, file string, kind shelfKind) (shelfLoaded, error) {
	var r shelfLoaded
	if h.client.SSH == nil {
		info, err := os.Stat(file)
		if err != nil {
			return r, err
		}
		r.modTime = info.ModTime()
	}
	name := path.Base(file)
	if kind == shelfImage {
		data, err := readHostFile(ctx, h, file)
		if err != nil {
			return r, err
		}
		r.thumb, err = gtkx.PixbufFit(data, shelfThumbWidth, shelfThumbHeight)
		r.title = name
		return r, err
	}
	data, err := readHostFileStart(ctx, h, file, shelfTextBytes)
	if err != nil {
		return r, err
	}
	if kind == shelfMarkdown {
		r.title, r.snippet = markdownSummary(string(data), name)
		return r, nil
	}
	r.title, r.snippet = htmlSummary(string(data), name)
	return r, nil
}

// Markdown syntax left out of a summary.
//
//nolint:gochecknoglobals
var (
	mdListMarker = regexp.MustCompile(`^(?:[-*+]|\d+[.)])\s+(?:\[[ xX]\]\s+)?|^>\s?`)
	mdLinkText   = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	mdEmphasis   = regexp.MustCompile("[*_`~]+")
	mdTableRule  = regexp.MustCompile(`^\|?\s*:?-{3,}`)
)

// markdownSummary returns a markdown document's title, its first heading or else name,
// and the start of its text, without markdown syntax, later headings or code blocks. List
// items are set apart with a dot.
func markdownSummary(doc, name string) (string, string) {
	title := ""
	var text strings.Builder
	inCode, lastItem := false, false
	for line := range strings.Lines(doc) {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inCode = !inCode
			continue
		}
		if inCode || t == "" || mdTableRule.MatchString(t) {
			continue
		}
		if strings.HasPrefix(t, "#") {
			if title == "" {
				title = strings.TrimSpace(strings.TrimLeft(t, "#"))
			}
			continue
		}
		item := mdListMarker.MatchString(t) && !strings.HasPrefix(t, ">")
		t = mdListMarker.ReplaceAllString(t, "")
		t = mdLinkText.ReplaceAllString(t, "$1")
		t = mdEmphasis.ReplaceAllString(t, "")
		t = strings.Join(strings.Fields(strings.ReplaceAll(t, "|", " ")), " ")
		if t == "" {
			continue
		}
		switch {
		case text.Len() == 0:
		case item || lastItem:
			text.WriteString(" · ")
		default:
			text.WriteString(" ")
		}
		text.WriteString(t)
		lastItem = item
		if text.Len() > shelfSnippetChars {
			break
		}
	}
	if title == "" {
		title = name
	}
	return title, clip(text.String(), shelfSnippetChars)
}

// htmlSummary returns an HTML document's title, or else name, and the start of its text.
func htmlSummary(doc, name string) (string, string) {
	title := name
	if m := htmlTitle.FindStringSubmatch(doc); m != nil {
		if t := strings.Join(strings.Fields(htmlTag.ReplaceAllString(m[1], "")), " "); t != "" {
			title = t
		}
	}
	text := htmlText(doc)
	// htmlText starts with the title, which is shown already.
	text = strings.TrimPrefix(text, title)
	return title, clip(strings.Join(strings.Fields(text), " "), shelfSnippetChars)
}

// clip shortens text to at most n characters, ending it with an ellipsis if it's cut.
func clip(text string, n int) string {
	if utf8.RuneCountInString(text) <= n {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:n-1])) + "…"
}

// textLink is a link found in text: its text and the kind of pattern it matched.
type textLink struct {
	text string
	kind linkKind
}

// linksInLines finds the links in lines of text, in the order they appear. As in the
// terminal, earlier patterns take precedence: a match overlapping one already found is
// left out, such as the path part of a URL.
func linksInLines(lines []string) []textLink {
	var links []textLink
	type found struct {
		start, end int
		kind       linkKind
	}
	for _, line := range lines {
		var taken []found
		for i, re := range linkRegexps {
			for _, m := range re.FindAllStringIndex(line, -1) {
				if !slices.ContainsFunc(taken, func(t found) bool { return m[0] < t.end && t.start < m[1] }) {
					taken = append(taken, found{m[0], m[1], linkPatterns[i].kind})
				}
			}
		}
		sort.Slice(taken, func(i, j int) bool { return taken[i].start < taken[j].start })
		for _, t := range taken {
			links = append(links, textLink{text: line[t.start:t.end], kind: t.kind})
		}
	}
	return links
}

// queueShelfScan reads the pane's session for files to put on the shelf, shelfScanDelay
// from now, unless a read is already waiting. Nothing is read while it's not needed: the
// window's shelf is hidden and doesn't show itself for new files.
func (p *Pane) queueShelfScan() {
	if p.closed || !p.running || p.file != nil || p.shelfTimer != 0 || !p.win.wantsShelfScan() {
		return
	}
	p.shelfTimer = glib.TimeoutAdd(uint(shelfScanDelay/time.Millisecond), func() bool {
		p.shelfTimer = 0
		p.scanForShelf()
		return false
	})
}

// scanForShelf reads the lines of the panes of the active tmux window of the pane's
// session, with their history the first time, and puts the files they mention on the
// session's shelf. Relative paths are taken from each pane's working directory.
func (p *Pane) scanForShelf() {
	if p.closed || !p.running || p.file != nil {
		return
	}
	key := p.SessionKey()
	h, s := p.win.app.lookup(key)
	if h == nil || s == nil || h.version == "" || tmux.CaptureCrashes(h.version) {
		return
	}
	aw := s.ActiveWindow()
	if aw == nil {
		return
	}
	store := p.win.app.shelf
	sf := store.get(key)
	history := 0
	if !sf.scanned {
		history, sf.scanned = shelfHistory, true
	}
	window, host, remote := aw.ID, p.hostName, p.hostName != LocalHost
	runAsync(p.win.app, func(ctx context.Context) ([]string, error) {
		layout, err := h.client.WindowLayout(ctx, window)
		if err != nil {
			return nil, err
		}
		var files []string
		for _, pane := range layout.Panes {
			lines, err := h.client.PaneLinesFrom(ctx, pane.ID, history)
			if err != nil {
				return nil, err
			}
			for _, l := range linksInLines(lines) {
				if f := resolveLink(vte.Match{Text: l.text}, l.kind, pane.Path, remote).file; f != "" {
					files = append(files, f)
				}
			}
		}
		return files, nil
	}, func(files []string, err error) {
		if err != nil {
			p.win.app.log.Debug("Could not read the session for the shelf", zap.Error(err))
			return
		}
		store.notice(key, host, files)
	})
}

// When the shelf shows: when the session mentions a new file, always or never. Its button
// shows or hides it for the moment whatever the choice; showing another session puts it
// back as chosen.
const (
	shelfShowNewFiles = "new-files"
	shelfShowAlways   = "always"
	shelfShowNever    = "never"
)

// validShelfShow reports whether a choice of when the shelf shows is one of them.
func validShelfShow(show string) bool {
	return show == shelfShowNewFiles || show == shelfShowAlways || show == shelfShowNever
}

// shelfShow returns when windows' shelves show: as chosen in Preferences, or else the
// configuration file, or else when a new file is mentioned.
func (a *App) shelfShow() string {
	if a.state.ShelfShow != nil && validShelfShow(*a.state.ShelfShow) {
		return *a.state.ShelfShow
	}
	if validShelfShow(a.cfg.Shelf.Show) {
		return a.cfg.Shelf.Show
	}
	return shelfShowNewFiles
}

// setShelfShow sets, and keeps, when shelves show, and shows or hides every window's as
// it now says.
func (a *App) setShelfShow(show string) {
	a.state.ShelfShow = &show
	a.saveState()
	for w := range a.windows {
		w.shelf.userShown, w.shelf.dismissed = false, false
		w.shelf.applyShow()
	}
}

// shelfChanged shows a session's changed files on the shelves showing it. isNew is set
// when a file was added, which shows the shelf if it shows itself for new files.
func (a *App) shelfChanged(key string, isNew bool) {
	for w := range a.windows {
		w.shelf.changed(key, isNew)
	}
}

// shelf is a window's file shelf, showing the files of the session of its active
// terminal pane.
type shelf struct {
	w     *Window
	root  *gtk.Box
	title *gtk.Label
	clear *gtk.Button
	list  *gtk.ListBox
	empty *gtk.Label
	// key is the session shown, and rows the items of the list's rows, by row.
	key    string
	rows   map[uintptr]*shelfItem
	listed []*gtk.ListBoxRow
	// pressed is the item the last button press was on, which a drag takes.
	pressed *shelfItem
	// userShown is set when it was shown with its button, and dismissed when it was
	// hidden with it, since the session last mentioned a new file.
	userShown, dismissed bool
}

func newShelf(w *Window) *shelf {
	s := &shelf{w: w, rows: map[uintptr]*shelfItem{}}
	s.root, _ = gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	addClass(s.root, "ttt-nav")
	addClass(s.root, "ttt-shelf")
	s.root.SetSizeRequest(shelfWidth, -1)

	header, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	addClass(header, "ttt-shelf-header")
	s.title, _ = gtk.LabelNew("Files")
	s.title.SetXAlign(0)
	s.title.SetTooltipText("Click a file to preview it in the split, or drag it out to open it in a " +
		"window of its own. Right-click for more.")
	addClass(s.title, "ttt-shelf-title")
	header.PackStart(s.title, true, true, 0)
	s.clear, _ = gtk.ButtonNewFromIconName("edit-clear-all-symbolic", gtk.ICON_SIZE_MENU)
	s.clear.SetRelief(gtk.RELIEF_NONE)
	s.clear.SetTooltipText("Clear the Shelf")
	s.clear.Connect("clicked", func() {
		if s.key != "" {
			w.app.shelf.clear(s.key)
		}
	})
	header.PackEnd(s.clear, false, false, 0)
	s.root.PackStart(header, false, false, 0)

	s.list, _ = gtk.ListBoxNew()
	s.list.SetSelectionMode(gtk.SELECTION_NONE)
	s.list.SetActivateOnSingleClick(true)
	addClass(s.list, "ttt-shelf-list")
	s.list.Connect("row-activated", func(_ interface{}, row *gtk.ListBoxRow) {
		if it := s.rows[row.Native()]; it != nil {
			s.open(it)
		}
	})
	s.list.Connect("button-press-event", func(_ interface{}, ev *gdk.Event) bool {
		return s.onButtonPress(gdk.EventButtonNewFromEvent(ev))
	})
	s.dragFiles()

	s.empty, _ = gtk.LabelNew("Images, markdown and HTML files the session mentions show here.")
	s.empty.SetLineWrap(true)
	s.empty.SetJustify(gtk.JUSTIFY_CENTER)
	s.empty.SetMarginStart(16)
	s.empty.SetMarginEnd(16)
	addClass(s.empty, "dim-label")
	s.empty.Show()
	s.list.SetPlaceholder(s.empty)

	scroller, _ := gtk.ScrolledWindowNew(nil, nil)
	scroller.SetPolicy(gtk.POLICY_NEVER, gtk.POLICY_AUTOMATIC)
	scroller.SetVExpand(true)
	scroller.Add(s.list)
	s.root.PackStart(scroller, true, true, 0)
	return s
}

// visible reports whether the shelf is shown.
func (s *shelf) visible() bool {
	return s.root.GetVisible()
}

// sync follows the window's session: it lists that session's files, and shows or hides
// itself as applyShow says.
func (s *shelf) sync() {
	key := ""
	if p := s.w.sessionPane(); p != nil && p.running && p.file == nil {
		key = p.SessionKey()
	}
	if key == s.key {
		return
	}
	s.key = key
	s.rebuild()
	s.applyShow()
	if s.visible() {
		s.w.scanShelfNow()
	}
}

// applyShow shows or hides the shelf as chosen: always, never, or, when it shows for new
// files, for a session with files, unless it was shown or hidden with its button since
// the session last mentioned a new file.
func (s *shelf) applyShow() {
	switch s.w.app.shelfShow() {
	case shelfShowAlways:
		s.w.setShelfVisible(true)
	case shelfShowNever:
		s.w.setShelfVisible(false)
	default:
		if !s.userShown && !s.dismissed {
			s.w.setShelfVisible(len(s.w.app.shelf.items(s.key)) > 0)
		}
	}
}

// changed shows a session's changed files, if the shelf shows that session. A new file
// shows the shelf if shelves show themselves for new files, even if it was hidden by hand.
func (s *shelf) changed(key string, isNew bool) {
	if key != s.key {
		return
	}
	s.rebuild()
	if isNew {
		s.dismissed = false
		if s.w.app.shelfShow() == shelfShowNewFiles && !s.visible() {
			s.w.setShelfVisible(true)
		}
	}
}

// rebuild lists the files of the session shown.
func (s *shelf) rebuild() {
	for _, row := range s.listed {
		row.Destroy()
	}
	s.listed = nil
	s.rows = map[uintptr]*shelfItem{}
	items := s.w.app.shelf.items(s.key)
	for _, it := range items {
		row, _ := gtk.ListBoxRowNew()
		addClass(row, "ttt-shelf-row")
		row.Add(s.itemWidget(it))
		row.ShowAll()
		s.list.Add(row)
		s.rows[row.Native()] = it
		s.listed = append(s.listed, row)
	}
	s.title.SetText("Files")
	if len(items) > 0 {
		s.title.SetText("Files (" + strconv.Itoa(len(items)) + ")")
	}
	s.clear.SetSensitive(len(items) > 0)
}

// itemWidget shows an item: an image's thumbnail, or a markdown or HTML file's title and
// the start of its text, over the file's name.
func (s *shelf) itemWidget(it *shelfItem) gtk.IWidget {
	box, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 4) //nolint:mnd // a little space
	addClass(box, "ttt-shelf-item")
	if it.thumb != nil {
		img, _ := gtk.ImageNewFromPixbuf(it.thumb)
		img.SetHAlign(gtk.ALIGN_CENTER)
		box.PackStart(img, false, false, 0)
	} else {
		card, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 2) //nolint:mnd // a little space
		addClass(card, "ttt-shelf-card")
		title, _ := gtk.LabelNew(it.title)
		title.SetXAlign(0)
		title.SetLineWrap(true)
		title.SetLines(2) //nolint:mnd // a short title
		title.SetEllipsize(pango.ELLIPSIZE_END)
		title.SetMaxWidthChars(1)
		addClass(title, "ttt-shelf-card-title")
		card.PackStart(title, false, false, 0)
		if it.snippet != "" {
			text, _ := gtk.LabelNew(it.snippet)
			text.SetXAlign(0)
			text.SetLineWrap(true)
			text.SetLines(5) //nolint:mnd // a few lines of it
			text.SetEllipsize(pango.ELLIPSIZE_END)
			text.SetMaxWidthChars(1)
			addClass(text, "ttt-shelf-card-text")
			card.PackStart(text, false, false, 0)
		}
		box.PackStart(card, false, false, 0)
	}
	name, _ := gtk.LabelNew(path.Base(it.file))
	name.SetXAlign(0)
	name.SetEllipsize(pango.ELLIPSIZE_MIDDLE)
	name.SetMaxWidthChars(1)
	addClass(name, "dim-label")
	addClass(name, "ttt-shelf-name")
	box.PackStart(name, false, false, 0)
	where := it.file
	if it.host != LocalHost {
		where = it.host + ":" + it.file
	}
	box.SetTooltipText(where)
	return box
}

// open opens an item as a double click on its path in the terminal would: an image or
// markdown file in the window's preview split, and an HTML file in the browser.
func (s *shelf) open(it *shelfItem) {
	if it.kind == shelfHTML {
		s.w.openLinkDefault(it.link())
		return
	}
	s.w.previewFile(it.host, it.file)
}

// openInWindow opens an item in a window of its own, near x, y on the screen if at is set.
// An HTML file opens as open opens it.
func (s *shelf) openInWindow(it *shelfItem, x, y int, at bool) {
	if it.kind == shelfHTML {
		s.open(it)
		return
	}
	s.w.app.NewPreviewWindow(it.host, it.file, x, y, at)
}

// onButtonPress notes the item pressed, for a drag, and opens its menu on a right click.
func (s *shelf) onButtonPress(ev *gdk.EventButton) bool {
	if ev.Type() != gdk.EVENT_BUTTON_PRESS {
		return false
	}
	row := s.list.GetRowAtY(int(ev.Y()))
	s.pressed = nil
	if row != nil {
		s.pressed = s.rows[row.Native()]
	}
	if s.pressed == nil || ev.Button() != gdk.BUTTON_SECONDARY {
		return false
	}
	return s.popup(s.pressed, ev)
}

// popup opens an item's menu at the pointer.
func (s *shelf) popup(it *shelfItem, ev *gdk.EventButton) bool {
	target := glib.VariantFromString(strconv.Itoa(it.seq))
	item := func(label, action string) *glib.MenuItem {
		mi := glib.MenuItemNewWithLabel(label)
		mi.SetActionAndTargetValue(action, target)
		return mi
	}
	open := glib.MenuNew()
	if it.kind == shelfHTML {
		open.AppendItem(item("Open", "win.shelf-open"))
	} else {
		open.AppendItem(item("Preview in Split", "win.shelf-open"))
		open.AppendItem(item("Preview in New Window", "win.shelf-open-window"))
	}
	if it.host == LocalHost && it.kind != shelfHTML {
		open.AppendItem(item("Open With Default Application", "win.shelf-open-default"))
	}
	edit := glib.MenuNew()
	edit.AppendItem(item("Copy Path", "win.shelf-copy-path"))
	edit.AppendItem(item("Remove From Shelf", "win.shelf-remove"))
	menu := glib.MenuNew()
	menu.AppendSectionWithoutLabel(&open.MenuModel)
	menu.AppendSectionWithoutLabel(&edit.MenuModel)
	popover, err := gtk.PopoverNewFromModel(s.list, &menu.MenuModel)
	if err != nil {
		return false
	}
	rect := gdk.Rectangle{}
	rect.SetX(int(ev.X()))
	rect.SetY(int(ev.Y()))
	rect.SetWidth(1)
	rect.SetHeight(1)
	popover.SetPointingTo(rect)
	popover.SetPosition(gtk.POS_BOTTOM)
	popover.Popup()
	return true
}

// installActions adds the actions of the shelf's menus, which name an item of the
// session shown by its seq.
func (s *shelf) installActions(m glib.IActionMap) {
	on := func(name string, fn func(*shelfItem)) {
		addStringAction(m, name, func(seq string) {
			if it := s.w.app.shelf.item(s.key, seq); it != nil {
				fn(it)
			}
		})
	}
	on("shelf-open", s.open)
	on("shelf-open-window", func(it *shelfItem) { s.openInWindow(it, 0, 0, false) })
	on("shelf-open-default", func(it *shelfItem) { s.w.openLink(it.link().uri) })
	on("shelf-copy-path", func(it *shelfItem) {
		if clip, err := gtk.ClipboardGet(gdk.SELECTION_CLIPBOARD); err == nil {
			clip.SetText(it.file)
		}
	})
	on("shelf-remove", func(it *shelfItem) { s.w.app.shelf.remove(s.key, it) })
}

// dragFiles lets files be dragged off the shelf. Nothing takes them, so a file dropped
// anywhere opens in a window of its own, where it was dropped.
func (s *shelf) dragFiles() {
	te, err := gtk.TargetEntryNew(shelfDragTarget, gtk.TARGET_SAME_APP, 0)
	if err != nil {
		return
	}
	s.list.DragSourceSet(gdk.BUTTON1_MASK, []gtk.TargetEntry{*te}, gdk.ACTION_COPY)
	var dragged *shelfItem
	s.list.Connect("drag-begin", func(_ interface{}, ctx interface{}) {
		dragged = s.pressed
		icon := "text-x-generic-symbolic"
		if dragged != nil && dragged.kind == shelfImage {
			icon = "image-x-generic-symbolic"
		}
		gtkx.DragSetIconName(ctx, icon)
	})
	s.list.Connect("drag-failed", func(_ interface{}, _ interface{}, result interface{}) bool {
		it := dragged
		if it == nil || enumValue(result) != dragResultNoTarget {
			return false
		}
		x, y, ok := pointerPosition()
		glib.IdleAdd(func() { s.openInWindow(it, x, y, ok) })
		// Handled: no animation of the drag returning to where it started.
		return true
	})
	s.list.Connect("drag-end", func() { dragged = nil })
}
