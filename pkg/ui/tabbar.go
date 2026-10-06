package ui

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/gotk3/gotk3/pango"
	"go.uber.org/zap"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

// tabMaxChars is the most characters of a window name a tab shows before ellipsizing.
const tabMaxChars = 24

// tabBar is the strip of tabs above the panes: one per tmux window of the session in the
// focused pane, in tmux's order, like tmux's own status line. Clicking a tab makes it the
// session's current window, right-clicking it gives its menu, and the + at the left makes
// a new window. The tabs scroll sideways when they don't fit.
type tabBar struct {
	win  *Window
	root *gtk.Box
	// scroller holds the tabs, beside the + button.
	scroller *gtk.ScrolledWindow
	box      *gtk.Box
	// session is the key of the session whose windows the tabs are.
	session string
	tabs    map[string]*tab
	order   []string
	// selected is the ID of the session's current window.
	selected string
}

// tab is one window's tab.
type tab struct {
	id        string
	button    *gtk.Button
	indicator *gtk.Stack
	index     *gtk.Label
	name      *gtk.Label
}

// windowKey identifies a window to the activity tracker. Window IDs start with @ and
// session IDs with $, so they don't collide with session keys.
func windowKey(host, windowID string) string {
	return sessionKey(host, windowID)
}

func newTabBar(w *Window) *tabBar {
	tb := &tabBar{win: w, tabs: map[string]*tab{}}
	tb.root, _ = gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	tb.root.SetNoShowAll(true)
	addClass(tb.root, "ttt-tabbar")
	addClass(tb.root, "ttt-nav")

	// The + stays at the left, outside the scrolling strip, so it's always in reach.
	newBtn, _ := gtk.ButtonNewFromIconName("list-add-symbolic", gtk.ICON_SIZE_MENU)
	newBtn.SetRelief(gtk.RELIEF_NONE)
	newBtn.SetCanFocus(false)
	newBtn.SetTooltipText("New Window")
	addClass(newBtn, "ttt-tab-new")
	newBtn.Connect("clicked", func() { tb.newWindow() })
	tb.root.PackStart(newBtn, false, false, 0)
	newBtn.Show()

	tb.scroller, _ = gtk.ScrolledWindowNew(nil, nil)
	// No scrollbar: the wheel scrolls the strip sideways, and the selected tab is kept in
	// view.
	tb.scroller.SetPolicy(gtk.POLICY_EXTERNAL, gtk.POLICY_NEVER)
	// GTK scrolls a strip sideways only for a sideways scroll: turn the wheel's up and
	// down into left and right too.
	tb.scroller.Connect("scroll-event", func(_ interface{}, ev *gdk.Event) bool {
		return tb.onScroll(gdk.EventScrollNewFromEvent(ev))
	})
	tb.root.PackStart(tb.scroller, true, true, 0)
	tb.scroller.Show()

	tb.box, _ = gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	tb.scroller.Add(tb.box)
	tb.box.Show()
	return tb
}

func newTab(tb *tabBar, id string) *tab {
	t := &tab{id: id}
	t.button, _ = gtk.ButtonNew()
	t.button.SetRelief(gtk.RELIEF_NONE)
	// Clicking a tab leaves the keyboard with the terminal.
	t.button.SetCanFocus(false)
	addClass(t.button, "ttt-tab")

	t.indicator, _ = gtk.StackNew()
	t.indicator.SetVAlign(gtk.ALIGN_CENTER)
	none, _ := gtk.LabelNew("")
	busy, _ := gtk.LabelNew(dot)
	addClass(busy, "ttt-busy-dot")
	unseen, _ := gtk.LabelNew(dot)
	addClass(unseen, "ttt-unseen-dot")
	t.indicator.AddNamed(none, indicatorNone)
	t.indicator.AddNamed(busy, indicatorBusy)
	t.indicator.AddNamed(unseen, indicatorUnseen)

	t.index, _ = gtk.LabelNew("")
	addClass(t.index, "ttt-tab-index")
	addClass(t.index, "dim-label")
	t.name, _ = gtk.LabelNew("")
	t.name.SetEllipsize(pango.ELLIPSIZE_END)
	t.name.SetMaxWidthChars(tabMaxChars)
	addClass(t.name, "ttt-session-name")

	box, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, rowSpacing)
	box.PackStart(t.indicator, false, false, 0)
	box.PackStart(t.index, false, false, 0)
	box.PackStart(t.name, false, false, 0)
	t.button.Add(box)
	t.button.ShowAll()
	t.indicator.SetVisibleChildName(indicatorNone)

	t.button.Connect("clicked", func() { tb.selectWindow(id) })
	t.button.Connect("button-press-event", func(_ interface{}, ev *gdk.Event) bool {
		return tb.onButtonPress(t, gdk.EventButtonNewFromEvent(ev))
	})
	return t
}

// onButtonPress opens a window's menu on right click.
func (tb *tabBar) onButtonPress(t *tab, ev *gdk.EventButton) bool {
	if ev.Type() != gdk.EVENT_BUTTON_PRESS || ev.Button() != gdk.BUTTON_SECONDARY {
		return false
	}
	hostName, _ := splitKey(tb.session)
	popover, err := gtk.PopoverNewFromModel(t.button, windowMenu(windowKey(hostName, t.id)))
	if err != nil {
		return false
	}
	popover.SetPosition(gtk.POS_BOTTOM)
	popover.Popup()
	return true
}

// windowMenu is the context menu of a tab, for the window with key.
func windowMenu(key string) *glib.MenuModel {
	menu := glib.MenuNew()
	rename := glib.MenuItemNewWithLabel("Rename…")
	rename.SetActionAndTargetValue("win.window-rename", glib.VariantFromString(key))
	menu.AppendItem(rename)
	return &menu.MenuModel
}

// newWindow makes a new tmux window at the end of the session, in the directory of the
// session's current window. tmux makes it the current window, and the tabs follow at the
// next snapshot, which is asked for straight away.
func (tb *tabBar) newWindow() {
	app := tb.win.app
	h, s := app.lookup(tb.session)
	if s == nil {
		return
	}
	id, dir := s.ID, ""
	if aw := s.ActiveWindow(); aw != nil {
		dir = aw.Path
	}
	runAsync(app, func(ctx context.Context) (string, error) {
		return h.client.NewWindow(ctx, id, dir)
	}, func(_ string, err error) {
		if err != nil {
			app.log.Error("Could not create tmux window", zap.String("host", h.Name), zap.Error(err))
			tb.win.showError("Could not create a tmux window in "+s.Name, err.Error())
		}
		h.requestPoll()
	})
}

// selectWindow makes a window its session's current window. The tabs follow at the
// next snapshot, which is asked for straight away.
func (tb *tabBar) selectWindow(id string) {
	if id == tb.selected {
		return
	}
	app := tb.win.app
	hostName, _ := splitKey(tb.session)
	h := app.host(hostName)
	if h == nil {
		return
	}
	app.tracker.MarkSeen(windowKey(hostName, id))
	runAsync(app, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, h.client.SelectWindow(ctx, id)
	}, func(_ struct{}, err error) {
		if err != nil {
			// Most likely the window closed since the last snapshot.
			app.log.Warn("Could not select tmux window",
				zap.String("host", hostName), zap.String("window", id), zap.Error(err))
		}
		h.requestPoll()
	})
}

// selectAt selects the window of the tab at a position in the bar, counting from 0, as
// Alt+number does. It works with the bar hidden too.
func (tb *tabBar) selectAt(index int) {
	if index < len(tb.order) {
		tb.selectWindow(tb.order[index])
	}
}

// tabScrollStep is how far, in pixels, one notch of the wheel scrolls the strip.
const tabScrollStep = 60

// onScroll scrolls the strip sideways for any scroll.
func (tb *tabBar) onScroll(ev *gdk.EventScroll) bool {
	var delta float64
	switch ev.Direction() {
	case gdk.SCROLL_UP, gdk.SCROLL_LEFT:
		delta = -tabScrollStep
	case gdk.SCROLL_DOWN, gdk.SCROLL_RIGHT:
		delta = tabScrollStep
	case gdk.SCROLL_SMOOTH:
		dx, dy := ev.DeltaX(), ev.DeltaY()
		if dy == 0 || math.Abs(dx) > math.Abs(dy) {
			// Already sideways: GTK scrolls it.
			return false
		}
		delta = dy * tabScrollStep
	}
	adj := tb.scroller.GetHAdjustment()
	adj.SetValue(min(max(adj.GetValue()+delta, adj.GetLower()), adj.GetUpper()-adj.GetPageSize()))
	return true
}

// update shows the windows of session, the key of the session in the focused pane, as
// tabs. With no session there are no tabs.
func (tb *tabBar) update(session string) {
	app := tb.win.app
	h, s := app.lookup(session)
	var windows []tmux.Window
	if s != nil {
		windows = s.Windows
	}
	ids := make([]string, len(windows))
	for i := range windows {
		ids[i] = windows[i].ID
	}
	if session != tb.session || !slices.Equal(ids, tb.order) {
		tb.rebuild(session, ids)
	}

	now := timeNow()
	selected := ""
	for i := range windows {
		win := &windows[i]
		t := tb.tabs[win.ID]
		t.index.SetText(fmt.Sprintf("%d", win.Index))
		t.name.SetText(win.Name)
		// Names keep their width, up to tabMaxChars, so tabs that don't fit scroll
		// rather than shrink.
		t.name.SetWidthChars(min(utf8.RuneCountInString(win.Name), tabMaxChars))
		tip := windowTooltip(win, now)
		if i < tabShortcuts {
			tip += fmt.Sprintf("\nAlt+%d", i+1)
		}
		t.button.SetTooltipText(tip)

		state := app.tracker.State(windowKey(h.Name, win.ID), now)
		switch {
		case state.Active:
			t.indicator.SetVisibleChildName(indicatorBusy)
		case state.Unseen:
			t.indicator.SetVisibleChildName(indicatorUnseen)
		default:
			t.indicator.SetVisibleChildName(indicatorNone)
		}
		setClass(t.button, "ttt-unseen", state.Unseen)
		setClass(t.button, "ttt-selected", win.Active)
		if win.Active {
			selected = win.ID
		}
	}

	if selected != tb.selected {
		tb.selected = selected
		if t, ok := tb.tabs[selected]; ok {
			// After layout, once the tab has its place in the strip.
			glib.IdleAdd(func() { tb.scrollTo(t) })
		}
	}
}

// windowTooltip describes a tmux window for its tab.
func windowTooltip(win *tmux.Window, now time.Time) string {
	lines := []string{fmt.Sprintf("%d: %s", win.Index, win.Name)}
	if win.Title != "" {
		lines = append(lines, win.Title)
	}
	if win.Command != "" {
		line := win.Command
		if win.Path != "" {
			line += " in " + win.Path
		}
		lines = append(lines, line)
	}
	if !win.Activity.IsZero() {
		lines = append(lines, fmt.Sprintf("Last output %s ago", now.Sub(win.Activity).Round(time.Second)))
	}
	return strings.Join(lines, "\n")
}

// rebuild replaces the tabs with ones for the windows ids of session, in order, reusing
// existing tabs of the same session.
func (tb *tabBar) rebuild(session string, ids []string) {
	for _, id := range tb.order {
		tb.box.Remove(tb.tabs[id].button)
	}
	if session != tb.session {
		// Window IDs are only unique within a host's server.
		tb.tabs = map[string]*tab{}
	}
	tabs := make(map[string]*tab, len(ids))
	for _, id := range ids {
		t, ok := tb.tabs[id]
		if !ok {
			t = newTab(tb, id)
		}
		tabs[id] = t
		tb.box.PackStart(t.button, false, false, 0)
	}
	tb.session = session
	tb.tabs = tabs
	tb.order = slices.Clone(ids)
	// A tab rebuilt in place may need bringing back into view.
	tb.selected = ""
	tb.syncVisible()
}

// syncVisible shows the bar if the window wants it and there are tabs to show.
func (tb *tabBar) syncVisible() {
	tb.root.SetVisible(tb.win.app.showTabs && len(tb.order) > 0)
}

// scrollTo scrolls the strip so a tab is in view.
func (tb *tabBar) scrollTo(t *tab) {
	if _, ok := tb.tabs[t.id]; !ok {
		return
	}
	adj := tb.scroller.GetHAdjustment()
	alloc := t.button.GetAllocation()
	left, right := float64(alloc.GetX()), float64(alloc.GetX()+alloc.GetWidth())
	value, page := adj.GetValue(), adj.GetPageSize()
	switch {
	case left < value:
		adj.SetValue(left)
	case right > value+page:
		adj.SetValue(right - page)
	}
}
