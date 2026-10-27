package ui

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/gotk3/gotk3/pango"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/gtkx"
)

// downloadTick is how often the progress of running downloads is redrawn.
const downloadTick = 250 * time.Millisecond

// downloadState is where a download is.
type downloadState int

const (
	downloadRunning downloadState = iota
	downloadDone
	downloadFailed
	downloadCancelled
)

// download is a file being copied, or once copied, from another host.
type download struct {
	name string
	host string
	// path is where the file is saved once it's done.
	path string
	// total is the file's size, or -1 until it's known, and received how much of it has
	// arrived. The copy's goroutine sets them.
	total    atomic.Int64
	received atomic.Int64
	// The rest is the main loop's.
	state     downloadState
	err       error
	cancel    context.CancelFunc
	cancelled bool
}

func newDownload(name, host string, cancel context.CancelFunc) *download {
	d := &download{name: name, host: host, cancel: cancel}
	d.total.Store(-1)
	return d
}

// fraction is how much of the file has arrived, from 0 to 1, or -1 if its size isn't known.
func (d *download) fraction() float64 {
	total := d.total.Load()
	switch {
	case d.state == downloadDone:
		return 1
	case total < 0:
		return -1
	case total == 0:
		return 0
	}
	return min(1, float64(d.received.Load())/float64(total))
}

// progressText describes how far a download is, such as "1.2 MB of 5.0 MB".
func (d *download) progressText() string {
	switch d.state {
	case downloadDone:
		return formatSize(d.received.Load()) + " · in " + tildePath(filepath.Dir(d.path))
	case downloadFailed:
		return "Failed: " + d.err.Error()
	case downloadCancelled:
		return "Cancelled"
	}
	received, total := d.received.Load(), d.total.Load()
	if total < 0 {
		return "Starting…"
	}
	return formatSize(received) + " of " + formatSize(total)
}

// countingWriter adds what's written through it to n.
type countingWriter struct {
	w io.Writer
	n *atomic.Int64
}

func (c countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n.Add(int64(n))
	return n, err
}

// formatSize describes a number of bytes as GNOME does, in powers of 1000: "512 bytes",
// "1.2 kB", "34.5 MB".
func formatSize(n int64) string {
	const unit = 1000
	if n < unit {
		if n == 1 {
			return "1 byte"
		}
		return fmt.Sprintf("%d bytes", n)
	}
	value, prefixes := float64(n)/unit, []string{"kB", "MB", "GB", "TB", "PB"}
	i := 0
	for value >= unit && i < len(prefixes)-1 {
		value /= unit
		i++
	}
	return fmt.Sprintf("%.1f %s", value, prefixes[i])
}

// downloadList is every download of the application, newest first, shown in each window's
// downloads button.
type downloadList struct {
	app   *App
	items []*download
	timer glib.SourceHandle
}

// add puts a download at the top of the list.
func (l *downloadList) add(d *download) {
	l.items = append([]*download{d}, l.items...)
	l.changed()
}

// remove takes a finished download off the list.
func (l *downloadList) remove(d *download) {
	for i, item := range l.items {
		if item == d {
			l.items = append(l.items[:i], l.items[i+1:]...)
			break
		}
	}
	l.changed()
}

// clearFinished takes every download which isn't running off the list.
func (l *downloadList) clearFinished() {
	kept := l.items[:0]
	for _, d := range l.items {
		if d.state == downloadRunning {
			kept = append(kept, d)
		}
	}
	l.items = kept
	l.changed()
}

// top is the newest running download, which the title bar shows, or nil.
func (l *downloadList) top() *download {
	for _, d := range l.items {
		if d.state == downloadRunning {
			return d
		}
	}
	return nil
}

// changed redraws every window's downloads after one is added, removed or finishes, and
// keeps progress redrawing while any runs.
func (l *downloadList) changed() {
	for w := range l.app.windows {
		w.downloadsView.rebuild()
	}
	if l.top() != nil && l.timer == 0 {
		l.timer = glib.TimeoutAdd(uint(downloadTick.Milliseconds()), func() bool {
			for w := range l.app.windows {
				w.downloadsView.refresh()
			}
			if l.top() == nil {
				l.timer = 0
				return false
			}
			return true
		})
	}
}

// downloadsView is a window's downloads button, in the title bar: it shows the top running
// download's progress, and opens the list of downloads.
type downloadsView struct {
	w       *Window
	button  *gtk.MenuButton
	bar     *gtk.ProgressBar
	rows    *gtk.Box
	clear   *gtk.Button
	entries map[*download]*downloadRow
}

// downloadRow is one download in the list.
type downloadRow struct {
	box    *gtk.Box
	bar    *gtk.ProgressBar
	status *gtk.Label
	cancel *gtk.Button
	open   *gtk.Button
	folder *gtk.Button
	remove *gtk.Button
}

// downloadBarWidth is the width of the title bar's progress bar, and downloadBarChars the
// most of a file's name it shows.
const (
	downloadBarWidth = 160
	downloadBarChars = 18
	// downloadListWidth is the width of the list of downloads, and downloadNameChars the
	// width of a name in it.
	downloadListWidth = 380
	downloadNameChars = 22
)

// middleEllipsis shortens a name to at most n characters by taking some out of its middle,
// so its start and its extension stay, as in "a-very-lo…name.tar.gz".
func middleEllipsis(name string, n int) string {
	runes := []rune(name)
	if len(runes) <= n || n < 3 { //nolint:mnd // room for an ellipsis
		return name
	}
	tail := (n - 1) / 2 //nolint:mnd // half of what's left after the ellipsis
	head := n - 1 - tail
	return string(runes[:head]) + "…" + string(runes[len(runes)-tail:])
}

func newDownloadsView(w *Window) *downloadsView {
	v := &downloadsView{w: w, entries: map[*download]*downloadRow{}}
	v.button, _ = gtk.MenuButtonNew()
	v.button.SetTooltipText("Downloads")
	v.button.SetNoShowAll(true)
	icon, _ := gtk.ImageNewFromIconName(firstIcon("folder-download-symbolic", "document-save-symbolic"),
		gtk.ICON_SIZE_BUTTON)
	v.bar, _ = gtk.ProgressBarNew()
	v.bar.SetShowText(true)
	v.bar.SetSizeRequest(downloadBarWidth, -1)
	v.bar.SetVAlign(gtk.ALIGN_CENTER)
	v.bar.SetNoShowAll(true)
	addClass(v.bar, "ttt-download-progress")
	inner, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, rowSpacing)
	inner.PackStart(icon, false, false, 0)
	inner.PackStart(v.bar, false, false, 0)
	icon.Show()
	inner.Show()
	v.button.Add(inner)

	popover, _ := gtk.PopoverNew(v.button)
	content, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, prefsSpacing/2) //nolint:mnd // half spacing
	content.SetMarginStart(prefsSpacing)
	content.SetMarginEnd(prefsSpacing)
	content.SetMarginTop(prefsSpacing)
	content.SetMarginBottom(prefsSpacing)
	heading, _ := gtk.LabelNew("Downloads")
	heading.SetXAlign(0)
	addClass(heading, "ttt-host-name")
	content.PackStart(heading, false, false, 0)
	scroller, _ := gtk.ScrolledWindowNew(nil, nil)
	scroller.SetPolicy(gtk.POLICY_NEVER, gtk.POLICY_AUTOMATIC)
	scroller.SetPropagateNaturalHeight(true)
	scroller.SetMaxContentHeight(400) //nolint:mnd // about six downloads
	content.SetSizeRequest(downloadListWidth, -1)
	v.rows, _ = gtk.BoxNew(gtk.ORIENTATION_VERTICAL, prefsSpacing)
	scroller.Add(v.rows)
	content.PackStart(scroller, true, true, 0)
	v.clear, _ = gtk.ButtonNewWithLabel("Clear Finished")
	v.clear.SetHAlign(gtk.ALIGN_END)
	v.clear.Connect("clicked", func() { w.app.downloadList.clearFinished() })
	content.PackStart(v.clear, false, false, 0)
	content.ShowAll()
	popover.Add(content)
	v.button.SetPopover(popover)
	v.rebuild()
	return v
}

// rebuild makes the list show the application's downloads.
func (v *downloadsView) rebuild() {
	for _, row := range v.entries {
		v.rows.Remove(row.box)
	}
	v.entries = map[*download]*downloadRow{}
	items := v.w.app.downloadList.items
	finished := false
	for _, d := range items {
		row := v.newRow(d)
		v.entries[d] = row
		v.rows.PackStart(row.box, false, false, 0)
		finished = finished || d.state != downloadRunning
	}
	v.clear.SetSensitive(finished)
	v.button.SetVisible(len(items) > 0)
	if len(items) == 0 {
		v.button.SetActive(false)
	}
	v.refresh()
}

// newRow makes the list's row of a download.
func (v *downloadsView) newRow(d *download) *downloadRow {
	r := &downloadRow{}
	r.box, _ = gtk.BoxNew(gtk.ORIENTATION_VERTICAL, rowSpacing)
	top, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, rowSpacing)
	name, _ := gtk.LabelNew(d.name)
	name.SetXAlign(0)
	name.SetEllipsize(pango.ELLIPSIZE_MIDDLE)
	// Ellipsized, a label asks for no width at all: this keeps most names whole.
	name.SetWidthChars(downloadNameChars)
	name.SetTooltipText(d.name + " from " + d.host)
	addClass(name, "ttt-session-name")
	host, _ := gtk.LabelNew(d.host)
	addClass(host, "dim-label")
	top.PackStart(name, true, true, 0)
	top.PackStart(host, false, false, 0)

	button := func(icon, tip string, onClick func()) *gtk.Button {
		b, _ := gtk.ButtonNewFromIconName(icon, gtk.ICON_SIZE_MENU)
		b.SetRelief(gtk.RELIEF_NONE)
		b.SetTooltipText(tip)
		b.SetNoShowAll(true)
		b.Connect("clicked", onClick)
		top.PackEnd(b, false, false, 0)
		return b
	}
	list := v.w.app.downloadList
	r.remove = button("window-close-symbolic", "Remove from the List", func() { list.remove(d) })
	r.cancel = button(firstIcon("process-stop-symbolic", "window-close-symbolic"), "Cancel", func() {
		d.cancelled = true
		d.cancel()
	})
	r.folder = button(firstIcon("folder-open-symbolic", "folder-symbolic"), "Show in Folder", func() {
		uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Dir(d.path))}).String()
		if err := gtkx.ShowURI(&v.w.window.Window, uri); err != nil {
			v.w.showError("Could not open the folder", err.Error())
		}
	})
	// Open is a word, not an icon: themes have no icon for it which a folder's isn't
	// mistaken for.
	r.open, _ = gtk.ButtonNewWithLabel("Open")
	r.open.SetRelief(gtk.RELIEF_NONE)
	r.open.SetTooltipText("Open " + d.name)
	r.open.SetNoShowAll(true)
	r.open.Connect("clicked", func() {
		v.w.openLink((&url.URL{Scheme: "file", Path: filepath.ToSlash(d.path)}).String())
	})
	top.PackEnd(r.open, false, false, 0)

	r.bar, _ = gtk.ProgressBarNew()
	r.status, _ = gtk.LabelNew("")
	r.status.SetXAlign(0)
	r.status.SetEllipsize(pango.ELLIPSIZE_END)
	addClass(r.status, "dim-label")
	r.box.PackStart(top, false, false, 0)
	r.box.PackStart(r.bar, false, false, 0)
	r.box.PackStart(r.status, false, false, 0)
	r.box.ShowAll()
	r.update(d)
	return r
}

// update shows a download's progress and the buttons which apply to where it is.
func (r *downloadRow) update(d *download) {
	running := d.state == downloadRunning
	if f := d.fraction(); f >= 0 {
		r.bar.SetFraction(f)
	} else {
		r.bar.Pulse()
	}
	r.bar.SetVisible(running)
	r.status.SetText(d.progressText())
	r.status.SetTooltipText(d.progressText())
	r.cancel.SetVisible(running)
	r.open.SetVisible(d.state == downloadDone)
	r.folder.SetVisible(d.state == downloadDone)
	r.remove.SetVisible(!running)
}

// refresh redraws the downloads' progress, and the top one's in the title bar.
func (v *downloadsView) refresh() {
	for d, row := range v.entries {
		row.update(d)
	}
	top := v.w.app.downloadList.top()
	if top == nil {
		v.bar.Hide()
		v.button.SetTooltipText("Downloads")
		return
	}
	name := middleEllipsis(top.name, downloadBarChars)
	text := name
	if f := top.fraction(); f >= 0 {
		v.bar.SetFraction(f)
		text = fmt.Sprintf("%s · %.0f%%", name, f*100) //nolint:mnd // a percentage
	} else {
		v.bar.Pulse()
	}
	v.bar.SetText(text)
	v.button.SetTooltipText("Downloading " + top.name + ": " + top.progressText())
	v.bar.Show()
}
