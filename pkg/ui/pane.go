package ui

import (
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/gotk3/gotk3/pango"
	"go.uber.org/zap"

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

	emptyTitle    *gtk.Label
	emptyHint     *gtk.Label
	reattachBtn   *gtk.Button
	newSessionBtn *gtk.Button

	sessionID string
	tty       string
	cmd       *exec.Cmd
	running   bool
	// generation increases with every client started, so the exit of a replaced client
	// is ignored.
	generation int
	closed     bool
}

func (p *Pane) widget() gtk.IWidget { return p.root }
func (p *Pane) getParent() *split   { return p.parent }
func (p *Pane) setParent(s *split)  { p.parent = s }
func (p *Pane) leaves() []*Pane     { return []*Pane{p} }
func (p *Pane) firstLeaf() *Pane    { return p }

// SessionID returns the session the pane shows, or last showed.
func (p *Pane) SessionID() string { return p.sessionID }

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
	p.term.Connect("contents-changed", func() { w.app.requestPoll() })
	// After VTE's own handler, so a program using the mouse gets the click unless Shift
	// is held, as in GNOME Terminal.
	p.term.ConnectAfter("button-press-event", func(_ interface{}, ev *gdk.Event) bool {
		return p.onButtonPress(gdk.EventButtonNewFromEvent(ev))
	})

	p.stack, _ = gtk.StackNew()
	p.stack.AddNamed(p.term, pageTerminal)
	p.stack.AddNamed(p.buildEmptyPage(), pageEmpty)

	p.root.PackStart(headerEvents, false, false, 0)
	p.root.PackStart(p.stack, true, true, 0)
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
	p.reattachBtn.Connect("clicked", func() { p.attach(p.sessionID) })
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

// Show makes the pane show a session. A running client is switched to it, which keeps
// the terminal and is quicker than starting a new client.
func (p *Pane) Show(sessionID string) {
	if p.running && p.tty != "" {
		if p.sessionID == sessionID {
			return
		}
		ctx, cancel := p.win.app.commandContext()
		defer cancel()
		err := p.win.app.client.SwitchClient(ctx, p.tty, sessionID)
		if err == nil {
			p.sessionID = sessionID
			p.win.app.tracker.MarkSeen(sessionID)
			p.win.refresh()
			return
		}
		p.win.app.log.Debug("switch-client failed: starting a new client", zap.Error(err))
	}
	p.attach(sessionID)
}

// attach starts a tmux client for a session in the pane's terminal, replacing any
// running one.
func (p *Pane) attach(sessionID string) {
	if sessionID == "" || p.closed {
		return
	}
	log := p.win.app.log.With(zap.String("session", sessionID))

	argv := p.win.app.client.AttachArgv(sessionID)
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

	p.cmd, p.tty, p.sessionID, p.running = cmd, tty, sessionID, true
	p.win.app.tracker.MarkSeen(sessionID)
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

// syncSession follows a client which changed session inside tmux.
func (p *Pane) syncSession(snap *tmux.Snapshot) {
	if !p.running || p.tty == "" {
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
