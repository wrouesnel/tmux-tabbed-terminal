package ui

import (
	"fmt"
	"math"
	"slices"
	"unicode/utf8"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/gotk3/gotk3/pango"
)

// tabMaxChars is the most characters of a session name a tab shows before ellipsizing.
const tabMaxChars = 24

// tabBar is the strip of tabs above the panes: one per session in the session list's
// current view, in the same order, so the sessions it shows can be clicked between
// without the list. It scrolls sideways when the tabs don't fit.
type tabBar struct {
	win      *Window
	root     *gtk.ScrolledWindow
	box      *gtk.Box
	tabs     map[string]*tab
	order    []string
	selected string
}

// tab is one session's tab.
type tab struct {
	key       string
	button    *gtk.Button
	indicator *gtk.Stack
	name      *gtk.Label
	host      *gtk.Label
}

func newTabBar(w *Window) *tabBar {
	tb := &tabBar{win: w, tabs: map[string]*tab{}}
	tb.root, _ = gtk.ScrolledWindowNew(nil, nil)
	// No scrollbar: the wheel scrolls the strip sideways, and the selected tab is kept in
	// view.
	tb.root.SetPolicy(gtk.POLICY_EXTERNAL, gtk.POLICY_NEVER)
	tb.root.SetNoShowAll(true)
	addClass(tb.root, "ttt-tabbar")
	addClass(tb.root, "ttt-nav")

	// GTK scrolls a strip sideways only for a sideways scroll: turn the wheel's up and
	// down into left and right too.
	tb.root.Connect("scroll-event", func(_ interface{}, ev *gdk.Event) bool {
		return tb.onScroll(gdk.EventScrollNewFromEvent(ev))
	})

	tb.box, _ = gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	tb.root.Add(tb.box)
	tb.box.Show()
	return tb
}

func newTab(tb *tabBar, key string) *tab {
	t := &tab{key: key}
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

	t.name, _ = gtk.LabelNew("")
	t.name.SetEllipsize(pango.ELLIPSIZE_END)
	t.name.SetMaxWidthChars(tabMaxChars)
	addClass(t.name, "ttt-session-name")
	t.host, _ = gtk.LabelNew("")
	addClass(t.host, "ttt-tab-host")
	addClass(t.host, "dim-label")

	box, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, rowSpacing)
	box.PackStart(t.indicator, false, false, 0)
	box.PackStart(t.name, false, false, 0)
	box.PackStart(t.host, false, false, 0)
	t.button.Add(box)
	t.button.ShowAll()
	t.indicator.SetVisibleChildName(indicatorNone)

	w := tb.win
	t.button.Connect("clicked", func() { w.ShowSession(key) })
	t.button.Connect("button-press-event", func(_ interface{}, ev *gdk.Event) bool {
		return tb.onButtonPress(t, gdk.EventButtonNewFromEvent(ev))
	})
	w.dragSessions(&t.button.Widget, func() string { return key })
	return t
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
	adj := tb.root.GetHAdjustment()
	adj.SetValue(min(max(adj.GetValue()+delta, adj.GetLower()), adj.GetUpper()-adj.GetPageSize()))
	return true
}

// onButtonPress opens a session's menu on right click, and in a new pane on middle or
// Ctrl click, as in the session list.
func (tb *tabBar) onButtonPress(t *tab, ev *gdk.EventButton) bool {
	if ev.Type() != gdk.EVENT_BUTTON_PRESS {
		return false
	}
	switch {
	case ev.Button() == gdk.BUTTON_MIDDLE,
		ev.Button() == gdk.BUTTON_PRIMARY && eventHasControl(ev):
		tb.win.OpenInSplit(t.key, gtk.ORIENTATION_HORIZONTAL)
		return true
	case ev.Button() == gdk.BUTTON_SECONDARY:
		_, pinned := tb.win.sidebar.pinnedSessions()[t.key]
		popover, err := gtk.PopoverNewFromModel(t.button, sessionMenu(t.key, pinned))
		if err != nil {
			return false
		}
		popover.SetPosition(gtk.POS_BOTTOM)
		popover.Popup()
		return true
	}
	return false
}

// update shows the sessions in the list's current view as tabs. selected is the session
// in the focused pane, and shown those in any pane.
func (tb *tabBar) update(keys []string, shown map[string]bool, selected string) {
	if !slices.Equal(keys, tb.order) {
		tb.rebuild(keys)
	}

	app := tb.win.app
	now := timeNow()
	for i, key := range keys {
		t := tb.tabs[key]
		h, s := app.lookup(key)
		if s == nil {
			continue
		}
		t.name.SetText(s.Name)
		// Names keep their width, up to tabMaxChars, so tabs that don't fit scroll
		// rather than shrink.
		t.name.SetWidthChars(min(utf8.RuneCountInString(s.Name), tabMaxChars))
		t.host.SetVisible(!h.Local())
		t.host.SetText(h.Name)
		tip := sessionTooltip(s)
		if !h.Local() {
			tip = h.Name + "\n" + tip
		}
		if i < sessionShortcuts {
			tip += fmt.Sprintf("\nAlt+%d", i+1)
		}
		t.button.SetTooltipText(tip)

		state := app.tracker.State(key, now)
		switch {
		case state.Active:
			t.indicator.SetVisibleChildName(indicatorBusy)
		case state.Unseen:
			t.indicator.SetVisibleChildName(indicatorUnseen)
		default:
			t.indicator.SetVisibleChildName(indicatorNone)
		}
		setClass(t.button, "ttt-unseen", state.Unseen)
		setClass(t.button, "ttt-shown", shown[key])
		setClass(t.button, "ttt-selected", key == selected)
	}

	if selected != tb.selected {
		tb.selected = selected
		if t, ok := tb.tabs[selected]; ok {
			// After layout, once the tab has its place in the strip.
			glib.IdleAdd(func() { tb.scrollTo(t) })
		}
	}
}

// rebuild replaces the tabs with ones for keys, in order, reusing existing tabs.
func (tb *tabBar) rebuild(keys []string) {
	for _, key := range tb.order {
		tb.box.Remove(tb.tabs[key].button)
	}
	tabs := make(map[string]*tab, len(keys))
	for _, key := range keys {
		t, ok := tb.tabs[key]
		if !ok {
			t = newTab(tb, key)
		}
		tabs[key] = t
		tb.box.PackStart(t.button, false, false, 0)
	}
	tb.tabs = tabs
	tb.order = slices.Clone(keys)
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
	if _, ok := tb.tabs[t.key]; !ok {
		return
	}
	adj := tb.root.GetHAdjustment()
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
