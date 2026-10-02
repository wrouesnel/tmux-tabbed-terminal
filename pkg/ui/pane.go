package ui

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/gotk3/gotk3/pango"
	"go.uber.org/zap"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/gtkx"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/vte"
)

// Stack pages of a pane.
const (
	pageTerminal = "terminal"
	pageEmpty    = "empty"
)

const (
	emptyIconSize = 64
	paneSpacing   = 6
	headerSpacing = 6
	// zoomStep is the font scale change of one zoom step, as in GNOME Terminal.
	zoomStep = 1.2
	minZoom  = 0.25
	maxZoom  = 4.0
)

// Pane is one terminal showing a tmux session through its own tmux client. It is a leaf
// of the window's split tree.
type Pane struct {
	win    *Window
	parent *split

	root        *gtk.Box
	header      *gtk.Box
	headerLabel *gtk.Label
	headerDot   *gtk.Label
	stack       *gtk.Stack
	term        *vte.Terminal
	// dropZone highlights where a dragged session would land.
	dropZone *gtk.Box

	emptyTitle    *gtk.Label
	emptyHint     *gtk.Label
	reattachBtn   *gtk.Button
	newSessionBtn *gtk.Button

	// hostName and sessionID are the session shown, or last shown.
	hostName  string
	sessionID string
	tty       string
	cmd       *exec.Cmd
	running   bool
	// generation increases with every client started, so the exit of a replaced client
	// is ignored.
	generation int
	closed     bool

	// Mouse wheel state: lines waiting to be scrolled, whether a scroll command is
	// running, and the streak of quick wheel events which speeds scrolling up.
	scrollPending float64
	scrolling     bool
	scrollStreak  int
	lastScroll    time.Time
	lastScrollUp  time.Time
}

func (p *Pane) widget() gtk.IWidget { return p.root }
func (p *Pane) getParent() *split   { return p.parent }
func (p *Pane) setParent(s *split)  { p.parent = s }
func (p *Pane) leaves() []*Pane     { return []*Pane{p} }
func (p *Pane) firstLeaf() *Pane    { return p }

// SessionKey returns the key of the session the pane shows, or last showed, or "".
func (p *Pane) SessionKey() string {
	if p.sessionID == "" {
		return ""
	}
	return sessionKey(p.hostName, p.sessionID)
}

// Running reports whether the pane's tmux client is running.
func (p *Pane) Running() bool { return p.running }

func newPane(w *Window) *Pane {
	p := &Pane{win: w}

	p.root, _ = gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	addClass(p.root, "ttt-pane")

	// The header names the session. It is only shown when the window is split.
	p.header, _ = gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, headerSpacing)
	addClass(p.header, "ttt-pane-header")
	p.headerDot, _ = gtk.LabelNew(dot)
	addClass(p.headerDot, "ttt-busy-dot")
	p.headerDot.SetNoShowAll(true)
	p.headerLabel, _ = gtk.LabelNew("")
	p.headerLabel.SetEllipsize(pango.ELLIPSIZE_END)
	p.headerLabel.SetHExpand(true)
	closeBtn, _ := gtk.ButtonNewFromIconName("window-close-symbolic", gtk.ICON_SIZE_MENU)
	closeBtn.SetRelief(gtk.RELIEF_NONE)
	closeBtn.SetFocusOnClick(false)
	closeBtn.SetTooltipText("Close Pane")
	closeBtn.Connect("clicked", func() { w.ClosePane(p) })
	p.header.PackStart(p.headerDot, false, false, 0)
	p.header.PackStart(p.headerLabel, true, true, 0)
	p.header.PackEnd(closeBtn, false, false, 0)
	// The header is hidden until the window splits. ShowAll skips it, so show its
	// children now.
	p.headerLabel.Show()
	closeBtn.Show()
	p.header.SetNoShowAll(true)

	// Clicking anywhere in the header focuses the pane.
	headerEvents, _ := gtk.EventBoxNew()
	headerEvents.Add(p.header)
	headerEvents.Connect("button-press-event", func() bool {
		p.Focus()
		return false
	})

	p.term = w.app.newTerminal()
	p.term.SetHExpand(true)
	p.term.SetVExpand(true)
	p.term.Connect("focus-in-event", func() bool {
		w.setActivePane(p)
		return false
	})
	p.term.Connect("contents-changed", func() { w.app.requestPoll(p.hostName) })
	// After VTE's own handler, so a program using the mouse gets the click unless Shift
	// is held, as in GNOME Terminal.
	p.term.ConnectAfter("button-press-event", func(_ interface{}, ev *gdk.Event) bool {
		return p.onButtonPress(gdk.EventButtonNewFromEvent(ev))
	})

	p.term.Connect("scroll-event", func(_ interface{}, ev *gdk.Event) bool {
		return p.onScroll(gdk.EventScrollNewFromEvent(ev))
	})

	p.stack, _ = gtk.StackNew()
	p.stack.AddNamed(p.term, pageTerminal)
	p.stack.AddNamed(p.buildEmptyPage(), pageEmpty)

	// Sessions dragged from the list drop on the pane, over the terminal.
	overlay, _ := gtk.OverlayNew()
	overlay.Add(p.stack)
	p.dropZone, _ = gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	addClass(p.dropZone, "ttt-drop-zone")
	p.dropZone.SetNoShowAll(true)
	overlay.AddOverlay(p.dropZone)
	overlay.SetOverlayPassThrough(p.dropZone, true)
	overlay.DragDestSet(0, dragTargets(), dropAction)
	overlay.Connect("drag-motion", func(_ interface{}, ctx interface{}, x, y int, time uint) bool {
		if w.app.dragKey == "" {
			gtkx.DragStatus(ctx, 0, time)
			return false
		}
		p.showDropZone(zoneAt(x, y, overlay.GetAllocatedWidth(), overlay.GetAllocatedHeight()))
		gtkx.DragStatus(ctx, dropAction, time)
		return true
	})
	overlay.Connect("drag-leave", func() { p.hideDropZone() })
	overlay.Connect("drag-drop", func(_ interface{}, ctx interface{}, x, y int, time uint) bool {
		key := w.app.dragKey
		zone := zoneAt(x, y, overlay.GetAllocatedWidth(), overlay.GetAllocatedHeight())
		gtkx.DragFinish(ctx, key != "", time)
		w.app.endDrag()
		if key != "" {
			// After the drag finishes, so the layout doesn't change under it.
			glib.IdleAdd(func() { w.dropSession(p, key, zone) })
		}
		return true
	})

	p.root.PackStart(headerEvents, false, false, 0)
	p.root.PackStart(overlay, true, true, 0)
	p.root.ShowAll()
	p.showEmpty("No session", "Choose a session from the list, or start a new one.", false)
	return p
}

// buildEmptyPage builds the page shown when the pane has no tmux client.
func (p *Pane) buildEmptyPage() gtk.IWidget {
	box, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, paneSpacing)
	box.SetHAlign(gtk.ALIGN_CENTER)
	box.SetVAlign(gtk.ALIGN_CENTER)

	icon, _ := gtk.ImageNewFromIconName("utilities-terminal-symbolic", gtk.ICON_SIZE_DIALOG)
	icon.SetPixelSize(emptyIconSize)
	addClass(icon, "dim-label")
	p.emptyTitle, _ = gtk.LabelNew("")
	addClass(p.emptyTitle, "ttt-empty-title")
	p.emptyHint, _ = gtk.LabelNew("")
	addClass(p.emptyHint, "dim-label")
	p.emptyHint.SetLineWrap(true)
	p.emptyHint.SetJustify(gtk.JUSTIFY_CENTER)

	buttons, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, paneSpacing)
	buttons.SetHAlign(gtk.ALIGN_CENTER)
	p.reattachBtn, _ = gtk.ButtonNewWithLabel("Reattach")
	p.reattachBtn.Connect("clicked", func() { p.attach(p.SessionKey()) })
	p.reattachBtn.Connect("focus-in-event", func() bool {
		p.win.setActivePane(p)
		return false
	})
	p.newSessionBtn, _ = gtk.ButtonNewWithLabel("New Session")
	addClass(p.newSessionBtn, "suggested-action")
	p.newSessionBtn.Connect("clicked", func() { p.win.NewSession(p) })
	p.newSessionBtn.Connect("focus-in-event", func() bool {
		p.win.setActivePane(p)
		return false
	})
	buttons.PackStart(p.reattachBtn, false, false, 0)
	buttons.PackStart(p.newSessionBtn, false, false, 0)
	p.reattachBtn.SetNoShowAll(true)

	box.PackStart(icon, false, false, 0)
	box.PackStart(p.emptyTitle, false, false, 0)
	box.PackStart(p.emptyHint, false, false, 0)
	box.PackStart(buttons, false, false, paneSpacing)

	// Clicking the page focuses the pane.
	events, _ := gtk.EventBoxNew()
	addClass(events, "ttt-empty")
	events.Add(box)
	events.Connect("button-press-event", func() bool {
		p.Focus()
		return false
	})
	return events
}

// showEmpty shows the empty page with a message.
func (p *Pane) showEmpty(title, hint string, reattach bool) {
	p.emptyTitle.SetText(title)
	p.emptyHint.SetText(hint)
	p.reattachBtn.SetVisible(reattach)
	p.stack.SetVisibleChildName(pageEmpty)
}

// Focus moves keyboard focus into the pane.
func (p *Pane) Focus() {
	if p.running {
		p.term.GrabFocus()
	} else {
		p.newSessionBtn.GrabFocus()
	}
	p.win.setActivePane(p)
}

// Show makes the pane show a session, by key. A running local client is switched to it
// with switch-client, which keeps the terminal and is quicker than a new client. A remote
// one is replaced: its tty is on the other host. The ssh master connection makes that
// quick too.
func (p *Pane) Show(key string) {
	hostName, id := splitKey(key)
	if p.running && p.SessionKey() == key {
		return
	}
	if p.running && p.tty != "" && hostName == LocalHost && p.hostName == LocalHost {
		h := p.win.app.host(LocalHost)
		tty := p.tty
		generation := p.generation
		runAsync(p.win.app, func(ctx context.Context) (struct{}, error) {
			return struct{}{}, h.client.SwitchClient(ctx, tty, id)
		}, func(_ struct{}, err error) {
			if p.closed || generation != p.generation {
				return
			}
			if err != nil {
				p.win.app.log.Debug("switch-client failed: starting a new client", zap.Error(err))
				p.attach(key)
				return
			}
			p.sessionID = id
			p.win.app.tracker.MarkSeen(key)
			p.win.refresh()
		})
		return
	}
	p.attach(key)
}

// attach starts a tmux client for a session in the pane's terminal, replacing any
// running one.
func (p *Pane) attach(key string) {
	hostName, id := splitKey(key)
	h := p.win.app.host(hostName)
	if id == "" || h == nil || p.closed {
		return
	}
	log := p.win.app.log.With(zap.String("host", hostName), zap.String("session", id))

	argv := h.client.AttachArgv(id)
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // the tmux binary is configured by the user
	cmd.Env = terminalEnv(os.Environ())
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}

	// Starting on a new pty closes the old one, which hangs up the old client.
	p.generation++
	generation := p.generation
	tty, err := p.term.Start(cmd)
	if err != nil {
		log.Error("Could not start tmux client", zap.Error(err))
		p.running = false
		p.showEmpty("Could not start tmux", err.Error(), true)
		return
	}
	log.Debug("Started tmux client", zap.String("tty", tty), zap.Int("pid", cmd.Process.Pid))

	p.cmd, p.tty, p.hostName, p.sessionID, p.running = cmd, tty, hostName, id, true
	p.win.app.tracker.MarkSeen(key)
	p.stack.SetVisibleChildName(pageTerminal)
	if p.win.activePane == p {
		p.term.GrabFocus()
	}

	go func() {
		err := cmd.Wait()
		glib.IdleAdd(func() { p.exited(generation, err) })
	}()
	p.win.refresh()
}

// exited handles the end of a tmux client.
func (p *Pane) exited(generation int, err error) {
	if p.closed || generation != p.generation {
		return
	}
	p.win.app.log.Debug("tmux client exited", zap.String("session", p.sessionID), zap.Error(err))
	p.running, p.tty, p.cmd = false, "", nil
	p.win.paneExited(p)
}

// terminalEnv returns the environment of a tmux client.
func terminalEnv(env []string) []string {
	result := make([]string, 0, len(env)+2) //nolint:mnd // the two variables added below
	for _, kv := range tmux.Environ(env) {
		if strings.HasPrefix(kv, "TERM=") || strings.HasPrefix(kv, "COLORTERM=") {
			continue
		}
		result = append(result, kv)
	}
	return append(result, "TERM=xterm-256color", "COLORTERM=truecolor")
}

// close hangs up the pane's client. The session keeps running.
func (p *Pane) close() {
	p.closed = true
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Signal(syscall.SIGHUP)
	}
}

// syncSession follows a local client which changed session inside tmux. Remote clients'
// ttys are on the other host, so they can't be followed.
func (p *Pane) syncSession(snap *tmux.Snapshot) {
	if !p.running || p.tty == "" || p.hostName != LocalHost {
		return
	}
	if id := snap.ClientSession(p.tty); id != "" && id != p.sessionID {
		p.sessionID = id
	}
}

// updateHeader shows the session name and activity in the pane header.
func (p *Pane) updateHeader(name string, busy bool, showHeader bool) {
	p.headerLabel.SetText(name)
	p.header.SetVisible(showHeader)
	p.headerDot.SetVisible(busy && showHeader)
	setClass(p.root, "ttt-active", p.win.activePane == p)
}

// onButtonPress shows the context menu on a right click.
func (p *Pane) onButtonPress(ev *gdk.EventButton) bool {
	if ev.Type() != gdk.EVENT_BUTTON_PRESS || ev.Button() != gdk.BUTTON_SECONDARY {
		return false
	}
	p.Focus()
	p.win.updateActionState()
	menu := terminalMenu()
	popover, err := gtk.PopoverNewFromModel(p.term, menu)
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

// showDropZone highlights the part of the pane a session would drop into.
func (p *Pane) showDropZone(zone dropZone) {
	w, h := p.stack.GetAllocatedWidth(), p.stack.GetAllocatedHeight()
	halign, valign := gtk.ALIGN_FILL, gtk.ALIGN_FILL
	width, height := -1, -1
	switch zone {
	case zoneLeft:
		halign, width = gtk.ALIGN_START, w/2 //nolint:mnd // half
	case zoneRight:
		halign, width = gtk.ALIGN_END, w/2 //nolint:mnd
	case zoneTop:
		valign, height = gtk.ALIGN_START, h/2 //nolint:mnd
	case zoneBottom:
		valign, height = gtk.ALIGN_END, h/2 //nolint:mnd
	case zoneCenter:
	}
	p.dropZone.SetHAlign(halign)
	p.dropZone.SetVAlign(valign)
	p.dropZone.SetSizeRequest(width, height)
	p.dropZone.Show()
}

// hideDropZone removes the drop highlight.
func (p *Pane) hideDropZone() {
	p.dropZone.Hide()
}

// Mouse wheel scrolling of tmux history.
const (
	// scrollLines is how many lines one wheel step scrolls, before acceleration.
	scrollLines = 3
	// Wheel events closer together than scrollStreakGap speed up scrolling; a pause of
	// scrollStreakReset, or a change of direction, starts again at normal speed.
	scrollStreakGap   = 90 * time.Millisecond
	scrollStreakReset = 300 * time.Millisecond
	// scrollAcceleration is the extra speed of each quick event in a streak, up to
	// scrollMaxSpeed times normal.
	scrollAcceleration = 0.35
	scrollMaxSpeed     = 15.0
	// scrollDownWindow is how long after scrolling up a scroll down can still be in
	// copy mode, when the last poll hasn't seen copy mode yet.
	scrollDownWindow = 2 * time.Second
)

// onScroll scrolls tmux's history with the mouse wheel, faster as the wheel spins faster.
// It scrolls tmux itself, entering copy mode, so it works whether or not tmux's mouse
// option is on. Programs using the mouse or the alternate screen, such as vim and less,
// get the wheel instead, as with tmux's own wheel binding.
func (p *Pane) onScroll(ev *gdk.EventScroll) bool {
	if !p.running || ev.State()&gdk.CONTROL_MASK != 0 {
		return false
	}
	h, s := p.win.app.lookup(p.SessionKey())
	if s == nil {
		return false
	}
	aw := s.ActiveWindow()
	if aw == nil || aw.PaneID == "" || aw.AlternateScreen || aw.MouseReporting {
		return false
	}

	var steps float64
	switch ev.Direction() {
	case gdk.SCROLL_UP:
		steps = -1
	case gdk.SCROLL_DOWN:
		steps = 1
	case gdk.SCROLL_SMOOTH:
		steps = ev.DeltaY()
	case gdk.SCROLL_LEFT, gdk.SCROLL_RIGHT:
		return false
	}
	if steps == 0 {
		return true
	}

	now := time.Now()
	gap := now.Sub(p.lastScroll)
	reversed := p.scrollPending != 0 && (steps < 0) != (p.scrollPending < 0)
	switch {
	case gap > scrollStreakReset || reversed:
		p.scrollStreak = 0
		p.scrollPending = 0
	case gap < scrollStreakGap:
		p.scrollStreak++
	}
	p.lastScroll = now
	if steps < 0 {
		p.lastScrollUp = now
	} else if !aw.InMode && now.Sub(p.lastScrollUp) > scrollDownWindow {
		// Already at the bottom: there's nothing to scroll down to.
		return true
	}

	speed := min(1+float64(p.scrollStreak)*scrollAcceleration, scrollMaxSpeed)
	p.scrollPending += steps * scrollLines * speed
	p.flushScroll(h, aw.PaneID)
	return true
}

// flushScroll sends the pending scroll to tmux, one command at a time, so a fast wheel
// becomes a few large scrolls rather than a queue of small ones.
func (p *Pane) flushScroll(h *Host, pane string) {
	lines := int(p.scrollPending)
	if p.scrolling || lines == 0 {
		return
	}
	p.scrollPending -= float64(lines)
	p.scrolling = true
	runAsync(p.win.app, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, h.client.ScrollPane(ctx, pane, lines)
	}, func(_ struct{}, err error) {
		p.scrolling = false
		if err != nil {
			// Scrolling down outside copy mode fails, which is fine.
			p.win.app.log.Debug("Scroll failed", zap.Error(err))
		}
		p.flushScroll(h, pane)
	})
}

// Zoom changes the font scale by steps, or resets it when steps is 0.
func (p *Pane) Zoom(steps int) {
	scale := 1.0
	if steps != 0 {
		scale = p.term.FontScale()
		for ; steps > 0; steps-- {
			scale *= zoomStep
		}
		for ; steps < 0; steps++ {
			scale /= zoomStep
		}
		scale = min(max(scale, minZoom), maxZoom)
	}
	p.term.SetFontScale(scale)
}
