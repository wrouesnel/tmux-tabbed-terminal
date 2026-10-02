// Package tmux talks to a tmux server through its command line interface.
//
// Every call runs one short-lived tmux process. State is read with format strings, so it
// doesn't depend on tmux's human-readable output.
package tmux

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
)

// ErrNoSession is returned when a session that was asked for doesn't exist.
var ErrNoSession = errors.New("no such tmux session")

// fieldSep separates fields in format output. It's printable ASCII because tmux replaces
// control characters, such as tabs, with "_" when its locale isn't UTF-8, as is common
// over ssh.
const fieldSep = "^|^"

// Client runs tmux commands against one tmux server.
type Client struct {
	// Binary is the tmux executable. It is looked up in PATH if it isn't absolute.
	Binary string
	// SocketName selects a server by name, as with tmux -L.
	SocketName string
	// SocketPath selects a server by socket path, as with tmux -S. It wins over SocketName.
	SocketPath string
	// SSH, if set, runs tmux on another host. Binary and the socket settings then apply
	// to the remote tmux.
	SSH *SSH
}

// NewClient returns a client for the default tmux server.
func NewClient() *Client {
	return &Client{Binary: "tmux"}
}

// ServerArgs returns the arguments which select this client's server. They go before the
// tmux command.
func (c *Client) ServerArgs() []string {
	switch {
	case c.SocketPath != "":
		return []string{"-S", c.SocketPath}
	case c.SocketName != "":
		return []string{"-L", c.SocketName}
	default:
		return nil
	}
}

// binary returns the tmux executable to run.
func (c *Client) binary() string {
	if c.Binary == "" {
		return "tmux"
	}
	return c.Binary
}

// tmuxArgv returns the tmux command line, as run on the host with the server. -u makes
// tmux use UTF-8 even when the locale doesn't say so, as over ssh without LANG.
func (c *Client) tmuxArgv(args ...string) []string {
	argv := []string{c.binary(), "-u"}
	argv = append(argv, c.ServerArgs()...)
	return append(argv, args...)
}

// Argv returns the full command line to run the given tmux command on this client's
// server: tmux itself, or ssh running it on the remote host.
func (c *Client) Argv(args ...string) []string {
	if c.SSH != nil {
		return c.SSH.argv(false, c.tmuxArgv(args...))
	}
	return c.tmuxArgv(args...)
}

// AttachArgv returns the command line of a tmux client attached to session. For a remote
// server it's an interactive ssh, so ssh can prompt in the terminal if it needs to.
func (c *Client) AttachArgv(session string) []string {
	if c.SSH != nil {
		return c.SSH.argv(true, c.tmuxArgv("attach-session", "-t", session))
	}
	return c.tmuxArgv("attach-session", "-t", session)
}

// Remote reports whether the server is on another host.
func (c *Client) Remote() bool {
	return c.SSH != nil
}

// Environ returns env without the variables which make tmux think it's running inside
// another tmux client. tmux refuses to attach when TMUX is set.
func Environ(env []string) []string {
	result := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") {
			continue
		}
		result = append(result, kv)
	}
	return result
}

// run runs a tmux command and returns its standard output.
func (c *Client) run(ctx context.Context, args ...string) (string, error) {
	if c.SSH != nil {
		if err := c.SSH.ensureMaster(ctx); err != nil {
			return "", err
		}
	}
	argv := c.Argv(args...)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // the binary is configured by the user
	cmd.Env = Environ(os.Environ())
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if c.SSH != nil && !isNoServerError(msg) && !isNoSessionError(msg) {
			// Maybe a connection on the way dropped: check the masters next time.
			c.SSH.resetMasters()
		}
		if isNoSessionError(msg) {
			return "", errors.Wrap(ErrNoSession, msg)
		}
		return "", errors.Wrapf(err, "tmux %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

// isNoServerError reports whether tmux's stderr says no server is running. That means
// there are no sessions, which isn't an error for this program.
func isNoServerError(msg string) bool {
	return strings.Contains(msg, "no server running") ||
		strings.Contains(msg, "error connecting to") ||
		strings.Contains(msg, "server exited unexpectedly")
}

// isNoSessionError reports whether tmux's stderr says a session doesn't exist.
func isNoSessionError(msg string) bool {
	return strings.Contains(msg, "can't find session") || strings.Contains(msg, "session not found")
}

// Window is one tmux window.
type Window struct {
	ID     string
	Index  int
	Name   string
	Active bool
	// Activity is when the window last had output.
	Activity time.Time
	// Command is the command running in the window's active pane.
	Command string
	// Path is the working directory of the window's active pane.
	Path string
	// PaneID is the ID of the window's active pane, such as %3.
	PaneID string
	// AlternateScreen is set when the active pane's program uses the alternate screen,
	// as full-screen programs such as vim and less do.
	AlternateScreen bool
	// MouseReporting is set when the active pane's program has asked for mouse events.
	MouseReporting bool
	// InMode is set when the active pane is in a mode, such as copy mode.
	InMode bool
	// Scroll is the active pane's history and scroll position.
	Scroll ScrollState
	// Title is the title of the window's active pane.
	Title string
}

// Session is one tmux session.
type Session struct {
	ID      string
	Name    string
	Created time.Time
	// Attached is the number of clients attached to the session.
	Attached int
	Windows  []Window
}

// Activity is when any window in the session last had output.
func (s *Session) Activity() time.Time {
	var latest time.Time
	for _, w := range s.Windows {
		if w.Activity.After(latest) {
			latest = w.Activity
		}
	}
	return latest
}

// ActiveWindow returns the session's current window, or nil if it has none.
func (s *Session) ActiveWindow() *Window {
	for i := range s.Windows {
		if s.Windows[i].Active {
			return &s.Windows[i]
		}
	}
	return nil
}

// ClientInfo is one client attached to the server.
type ClientInfo struct {
	TTY       string
	SessionID string
}

// Snapshot is the state of the tmux server at one moment.
type Snapshot struct {
	Sessions []Session
	Clients  []ClientInfo
}

// Session returns the session with the given ID, or nil.
func (s *Snapshot) Session(id string) *Session {
	for i := range s.Sessions {
		if s.Sessions[i].ID == id {
			return &s.Sessions[i]
		}
	}
	return nil
}

// ClientSession returns the ID of the session the client on tty is showing, or "".
func (s *Snapshot) ClientSession(tty string) string {
	for _, c := range s.Clients {
		if c.TTY == tty {
			return c.SessionID
		}
	}
	return ""
}

// Format strings for the snapshot. Each line starts with a record type so one tmux
// invocation can return sessions, windows and clients together.
//
//nolint:gochecknoglobals
var (
	sessionFormat = strings.Join([]string{
		"S", "#{session_id}", "#{session_name}", "#{session_created}", "#{session_attached}",
	}, fieldSep)
	windowFormat = strings.Join([]string{
		"W", "#{session_id}", "#{window_id}", "#{window_index}", "#{window_active}",
		"#{window_activity}", "#{pane_current_command}", "#{window_name}", "#{pane_current_path}",
		"#{pane_id}", "#{alternate_on}", "#{mouse_any_flag}", "#{pane_in_mode}",
		"#{history_size}", "#{pane_height}", "#{scroll_position}", "#{pane_title}",
	}, fieldSep)
	clientFormat = strings.Join([]string{"C", "#{client_tty}", "#{session_id}"}, fieldSep)
)

// Snapshot reads every session, window and client on the server. A server which isn't
// running has no sessions.
func (c *Client) Snapshot(ctx context.Context) (*Snapshot, error) {
	out, err := c.run(ctx,
		"list-sessions", "-F", sessionFormat, ";",
		"list-windows", "-a", "-F", windowFormat, ";",
		"list-clients", "-F", clientFormat)
	if err != nil {
		if isNoServerError(err.Error()) {
			return &Snapshot{}, nil
		}
		return nil, err
	}
	return ParseSnapshot(out), nil
}

// parseUnix parses a decimal unix timestamp, returning the zero time if it's invalid.
func parseUnix(s string) time.Time {
	secs, err := strconv.ParseInt(s, 10, 64)
	if err != nil || secs == 0 {
		return time.Time{}
	}
	return time.Unix(secs, 0)
}

// ParseSnapshot parses the output of the snapshot command. Malformed lines are skipped.
//
//nolint:mnd // field counts are fixed by the format strings
func ParseSnapshot(out string) *Snapshot {
	snap := &Snapshot{}
	index := map[string]int{}
	windows := map[string][]Window{}

	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, fieldSep)
		switch fields[0] {
		case "S":
			if len(fields) < 5 {
				continue
			}
			attached, _ := strconv.Atoi(fields[4])
			index[fields[1]] = len(snap.Sessions)
			snap.Sessions = append(snap.Sessions, Session{
				ID:       fields[1],
				Name:     fields[2],
				Created:  parseUnix(fields[3]),
				Attached: attached,
			})
		case "W":
			if len(fields) < 17 {
				continue
			}
			idx, _ := strconv.Atoi(fields[3])
			windows[fields[1]] = append(windows[fields[1]], Window{
				ID:       fields[2],
				Index:    idx,
				Active:   fields[4] == "1",
				Activity: parseUnix(fields[5]),
				Command:  fields[6],
				Name:     fields[7],
				Path:     fields[8],
				PaneID:   fields[9],
				// tmux prints 1 for set flags.
				AlternateScreen: fields[10] == "1",
				MouseReporting:  fields[11] == "1",
				InMode:          fields[12] == "1",
				Scroll:          parseScroll(fields[13], fields[14], fields[15]),
				// The title may contain the separator: keep the rest of the line.
				Title: strings.Join(fields[16:], fieldSep),
			})
		case "C":
			if len(fields) < 3 {
				continue
			}
			snap.Clients = append(snap.Clients, ClientInfo{TTY: fields[1], SessionID: fields[2]})
		}
	}

	for id, ws := range windows {
		i, ok := index[id]
		if !ok {
			continue
		}
		sort.Slice(ws, func(a, b int) bool { return ws[a].Index < ws[b].Index })
		snap.Sessions[i].Windows = ws
	}
	return snap
}

// NewSession creates a detached session and returns its ID. An empty name lets tmux pick
// one. An empty dir starts it in the server's default directory.
func (c *Client) NewSession(ctx context.Context, name string, dir string) (string, error) {
	args := []string{"new-session", "-d", "-P", "-F", "#{session_id}"}
	if name != "" {
		args = append(args, "-s", name)
	}
	if dir != "" {
		args = append(args, "-c", dir)
	}
	out, err := c.run(ctx, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// KillSession destroys a session.
func (c *Client) KillSession(ctx context.Context, session string) error {
	_, err := c.run(ctx, "kill-session", "-t", session)
	return err
}

// RenameSession renames a session.
func (c *Client) RenameSession(ctx context.Context, session string, name string) error {
	_, err := c.run(ctx, "rename-session", "-t", session, "--", name)
	return err
}

// SwitchClient makes the client on tty show session.
func (c *Client) SwitchClient(ctx context.Context, tty string, session string) error {
	_, err := c.run(ctx, "switch-client", "-c", tty, "-t", session)
	return err
}

// ScrollState is where a pane's view is in its history.
type ScrollState struct {
	// History is the number of lines scrolled off the top of the pane.
	History int
	// Height is the pane's height in lines.
	Height int
	// Position is how many lines up from the bottom the view is: 0 unless in copy mode.
	Position int
}

// parseScroll parses the scroll formats. tmux prints an empty scroll_position outside copy
// mode.
func parseScroll(history, height, position string) ScrollState {
	var st ScrollState
	st.History, _ = strconv.Atoi(history)
	st.Height, _ = strconv.Atoi(height)
	st.Position, _ = strconv.Atoi(position)
	return st
}

// scrollStateFormat prints a ScrollState.
//
//nolint:gochecknoglobals
var scrollStateFormat = strings.Join([]string{"#{history_size}", "#{pane_height}", "#{scroll_position}"}, fieldSep)

// ScrollPane scrolls a pane's history by lines: up for negative, down for positive. It
// returns where the view ended up. Scrolling up enters copy mode, which leaves again on
// scrolling back to the bottom, as tmux's own mouse wheel binding does.
func (c *Client) ScrollPane(ctx context.Context, pane string, lines int) (ScrollState, error) {
	args := []string{}
	switch {
	case lines < 0:
		args = append(args, "copy-mode", "-e", "-t", pane, ";",
			"send-keys", "-X", "-N", strconv.Itoa(-lines), "-t", pane, "scroll-up", ";")
	case lines > 0:
		args = append(args, "send-keys", "-X", "-N", strconv.Itoa(lines), "-t", pane, "scroll-down", ";")
	}
	args = append(args, "display-message", "-p", "-t", pane, scrollStateFormat)
	out, err := c.run(ctx, args...)
	if err != nil {
		return ScrollState{}, err
	}
	fields := strings.Split(strings.TrimSpace(out), fieldSep)
	if len(fields) != 3 { //nolint:mnd // the fields of scrollStateFormat
		return ScrollState{}, errors.Errorf("unexpected scroll state %q", out)
	}
	return parseScroll(fields[0], fields[1], fields[2]), nil
}

// CaptureHistory returns all the history and visible text of a session's current pane,
// with lines tmux wrapped joined again.
func (c *Client) CaptureHistory(ctx context.Context, session string) (string, error) {
	return c.run(ctx, "capture-pane", "-p", "-J", "-S", "-", "-E", "-", "-t", session)
}

// HasSession reports whether a session exists.
func (c *Client) HasSession(ctx context.Context, session string) (bool, error) {
	_, err := c.run(ctx, "has-session", "-t", session)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrNoSession), isNoServerError(err.Error()):
		return false, nil
	default:
		return false, err
	}
}
