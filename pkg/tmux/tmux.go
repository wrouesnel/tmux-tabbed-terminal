// Package tmux talks to a tmux server through its command line interface.
//
// Every call runs one short-lived tmux process. State is read with format strings, so it
// doesn't depend on tmux's human-readable output.
package tmux

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/agent"
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
	// Timeout bounds each command run with a context which has no deadline of its own,
	// if it isn't zero. A context without one costs nothing per command, where one with
	// a deadline is made for each: pollers, which run commands several times a second,
	// pass one and leave the limit to Timeout.
	Timeout time.Duration

	mu           sync.Mutex
	defaultShell string
	// version is the server's version as last asked, at versionAt.
	version   string
	versionAt time.Time
	// lastOutput is the snapshot command's last output, and lastSnapshot what it parsed
	// to. Snapshots are only read, so the same one can be returned again.
	lastOutput   []byte
	lastSnapshot *Snapshot

	// control is the control connection, if one is running, and controlRetryAt when to
	// try starting one again after one couldn't be.
	controlMu      sync.Mutex
	control        *controlConn
	controlRetryAt time.Time
	// controlFailures counts control connections which failed to start in a row.
	controlFailures uint
	// controlOff turns control connections off.
	controlOff bool
}

// versionTTL is how long the server's version is taken as known. It changes only when
// the server is restarted, as with another tmux, so asking for it before each capture is
// a process spent for nothing.
const versionTTL = time.Minute

// SetDefaultShell sets the server's default-shell option each time a session or window
// is made, so that they and the panes split from them run shell. Empty leaves the option
// alone. It's safe to call while other calls are running.
func (c *Client) SetDefaultShell(shell string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.defaultShell = shell
}

// withDefaultShell puts setting the default shell, if one is set, before a command that
// makes a shell. tmux runs the two in order, starting the server first if it must.
func (c *Client) withDefaultShell(ctx context.Context, args []string) []string {
	c.mu.Lock()
	shell := c.defaultShell
	c.mu.Unlock()
	if shell == "" {
		return args
	}
	return append([]string{"set-option", "-g", "default-shell", c.ServerPath(ctx, shell), ";"}, args...)
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
// features are terminal features, such as hyperlinks, which tmux should use for the
// client's terminal though its TERM doesn't say so. tmux before 3.2 has no -T for them:
// pass none there.
func (c *Client) AttachArgv(session string, features ...string) []string {
	args := attachArgs(session, features)
	if c.SSH != nil {
		return c.SSH.argv(true, c.tmuxArgv(args...))
	}
	return c.tmuxArgv(args...)
}

// attachArgs are the tmux arguments of a client attached to session, with features.
func attachArgs(session string, features []string) []string {
	args := []string{"attach-session", "-t", session}
	if len(features) > 0 {
		args = append([]string{"-T", strings.Join(features, ",")}, args...)
	}
	return args
}

// AttachThroughAgent starts a tmux client attached to session on a terminal on the remote
// server's route's first host, through its agent: the client itself there, or the ssh of
// the hops after it. It returns nil, and no error, if the route has no agent, when the
// caller runs AttachArgv's command instead. env is the terminal's environment, such as
// TERM.
func (c *Client) AttachThroughAgent(ctx context.Context, session string, features []string,
	env map[string]string) (*agent.Terminal, error) {
	if c.SSH == nil {
		return nil, nil
	}
	ctx, cancel, err := c.SSH.connect(ctx)
	defer cancel()
	if err != nil {
		return nil, err
	}
	_, ac := c.SSH.agentClient()
	if ac == nil {
		return nil, nil
	}
	argv := c.SSH.innerArgv(true, false, c.tmuxArgv(attachArgs(session, features)...))
	// The terminal outlives ctx, which only limits how long it has to start.
	return ac.Attach(context.WithoutCancel(ctx), argv, agent.TerminalOptions{Cols: 80, Rows: 24, Env: env}) //nolint:mnd // until the widget says
}

// SupportsHyperlinks reports whether a tmux version, as ServerVersion returns it, passes
// OSC 8 hyperlinks through to terminals with the hyperlinks feature, as tmux 3.4 and later
// do. Unreleased and unrecognised versions don't count.
func SupportsHyperlinks(version string) bool {
	var major, minor int
	if _, err := fmt.Sscanf(strings.TrimSpace(version), "%d.%d", &major, &minor); err != nil {
		return false
	}
	return major > 3 || (major == 3 && minor >= 4) //nolint:mnd // tmux 3.4
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
	return c.runUsing(ctx, nil, args...)
}

// runUsing is run, giving the output to use, if it isn't nil, instead of returning it.
// The output is only good until use returns. To keep allocations down on long runs,
// polls read it where it is, without a copy.
func (c *Client) runUsing(ctx context.Context, use func([]byte), args ...string) (string, error) {
	if out, ran, err := c.runControl(ctx, args, use); ran {
		return out, err
	}
	if _, ok := ctx.Deadline(); !ok && c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	if c.SSH != nil {
		var cancel context.CancelFunc
		var err error
		ctx, cancel, err = c.SSH.connect(ctx)
		defer cancel()
		if err != nil {
			return "", err
		}
	}
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	var err error
	if ran, agentErr := c.runThroughAgent(ctx, stdout, stderr, args); ran {
		err = agentErr
	} else {
		cmd := Command(ctx, c.Argv(args...))
		cmd.Env = Environ(os.Environ())
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		err = cmd.Run()
	}
	if err != nil {
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
	if use != nil {
		use(stdout.Bytes())
		return "", nil
	}
	return stdout.String(), nil
}

// runThroughAgent runs a tmux command through the agent of a remote server's route, if it
// has one running, writing its output to stdout and stderr. It reports whether it did.
func (c *Client) runThroughAgent(ctx context.Context, stdout, stderr io.Writer, args []string) (bool, error) {
	if c.SSH == nil {
		return false, nil
	}
	root, ac := c.SSH.agentClient()
	if ac == nil {
		return false, nil
	}
	res, err := ac.Run(ctx, c.SSH.innerArgv(false, false, c.tmuxArgv(args...)), nil)
	if err != nil {
		root.agentFailed(ac, err)
		return true, errors.Wrap(err, "running tmux through the agent")
	}
	_, _ = stdout.Write(res.Stdout)
	_, _ = stderr.Write(res.Stderr)
	return true, res.Err()
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
	// Panes is how many panes the window has, and Width how many columns it is wide.
	Panes int
	Width int
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
	// Windows are the session's windows, in index order. They may be shared with the
	// last snapshot's session, if they haven't changed: like the rest of a snapshot,
	// they're only read.
	Windows []Window

	// line is the session's line in the snapshot's output, and windowLines the text of
	// its windows, for the next snapshot to tell whether they changed. windowsMerged is
	// set if they weren't listed together, so can't be told by it.
	line          string
	windowLines   string
	windowsMerged bool
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
	// Sessions may be shared with the last snapshot, if they haven't changed.
	Sessions []*Session
	Clients  []ClientInfo
	// Buffer is the newest paste buffer, or nil if there are none.
	Buffer *Buffer
}

// Buffer is a tmux paste buffer, as copy mode makes.
type Buffer struct {
	Name string
	// Created is when the buffer was made. tmux before 3.1 doesn't say: it's zero there.
	Created time.Time
	// Size is the buffer's length in bytes.
	Size int
}

// NewerThan reports whether b was made after old: later, or in the same second under
// another name. Without creation times, as before tmux 3.1, any other name is newer.
func (b *Buffer) NewerThan(old *Buffer) bool {
	switch {
	case old == nil:
		return true
	case b.Created.After(old.Created):
		return true
	case b.Created.Before(old.Created):
		return false
	}
	return b.Name != old.Name
}

// Session returns the session with the given ID, or nil. The snapshot may be nil.
func (s *Snapshot) Session(id string) *Session {
	if s == nil {
		return nil
	}
	for _, sess := range s.Sessions {
		if sess.ID == id {
			return sess
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
		"#{history_size}", "#{pane_height}", "#{scroll_position}", "#{window_panes}", "#{window_width}",
		"#{pane_title}",
	}, fieldSep)
	clientFormat = strings.Join([]string{"C", "#{client_tty}", "#{session_id}", "#{client_flags}"}, fieldSep)
	bufferFormat = strings.Join([]string{"B", "#{buffer_name}", "#{buffer_created}", "#{buffer_size}"}, fieldSep)
)

// Snapshot reads every session, window and client on the server. A server which isn't
// running has no sessions. The snapshot may be the one returned last time, when nothing
// changed, so it must not be modified.
func (c *Client) Snapshot(ctx context.Context) (*Snapshot, error) {
	var snap *Snapshot
	// Most polls find nothing new: then the last snapshot is the same, and parsing it
	// again would only make garbage. Otherwise what's the same is shared with it.
	use := func(out []byte) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.lastSnapshot == nil || !bytes.Equal(out, c.lastOutput) {
			c.lastSnapshot = parseSnapshot(out, c.lastSnapshot)
			c.lastOutput = append(c.lastOutput[:0], out...)
		}
		snap = c.lastSnapshot
	}
	_, err := c.runUsing(ctx, use,
		"list-sessions", "-F", sessionFormat, ";",
		"list-windows", "-a", "-F", windowFormat, ";",
		"list-clients", "-F", clientFormat, ";",
		// Newest first.
		"list-buffers", "-F", bufferFormat)
	if err != nil {
		if isNoServerError(err.Error()) {
			return &Snapshot{}, nil
		}
		return nil, err
	}
	return snap, nil
}

// parseUnix parses a decimal unix timestamp, returning the zero time if it's invalid.
func parseUnix(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	secs, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return unixTime(secs)
}

// unixTime returns the time of a unix timestamp, or the zero time for 0, which tmux
// prints for times it doesn't know.
func unixTime(secs int64) time.Time {
	if secs == 0 {
		return time.Time{}
	}
	return time.Unix(secs, 0)
}

// programName shortens a Windows program's path, such as C:\\Windows\\System32\\cmd.exe,
// to its name, cmd. A Cygwin or MSYS2 tmux gives such paths, with their backslashes
// escaped, as the command of a pane running a Windows program, and names its window
// after it. Anything else is returned unchanged.
func programName(s string) string {
	if len(s) < 3 || s[1] != ':' || s[2] != '\\' || !strings.EqualFold(filepath.Ext(s), ".exe") {
		return s
	}
	name := s[strings.LastIndexByte(s, '\\')+1:]
	return name[:len(name)-len(".exe")]
}

// ParseSnapshot parses the output of the snapshot command. Malformed lines are skipped.
func ParseSnapshot(out string) *Snapshot {
	return parseSnapshot([]byte(out), nil)
}

// snapshotLine is a line of the snapshot command's output, as offsets into it.
type snapshotLine struct{ start, end int }

// windowGroup is a run of window lines of one session in the snapshot's output.
type windowGroup struct {
	session    snapshotLine
	start, end int
	count      int
}

// Line prefixes of the snapshot command's output.
//
//nolint:gochecknoglobals // constants, as bytes
var (
	sessionPrefix = []byte("S" + fieldSep)
	windowPrefix  = []byte("W" + fieldSep)
	clientPrefix  = []byte("C" + fieldSep)
	bufferPrefix  = []byte("B" + fieldSep)
	separator     = []byte(fieldSep)
	newline       = []byte("\n")
)

// parseSnapshot parses the output of the snapshot command, sharing with prev, the last
// snapshot, if it isn't nil, whatever is as it was.
//
// It's written to keep allocations down on long runs with many sessions. Busy sessions
// change the output on almost every poll, as their history grows, so it's parsed
// several times a second. It's read from the output's bytes, which aren't kept: only
// the lines which changed are made strings. A session whose line and window lines are
// as they were is the last snapshot's own, and one whose window lines are shares their
// windows: snapshots are only read. So what a poll allocates grows with the sessions
// which changed, not with every session on the server.
func parseSnapshot(out []byte, prev *Snapshot) *Snapshot {
	snap := &Snapshot{}
	// Where the lines are, gathered first: room for the usual numbers is kept on the
	// stack.
	var (
		sessionBuf [stackSessions]snapshotLine
		groupBuf   [stackSessions]windowGroup
		monitorBuf [8][]byte
		fieldBuf   [windowFields][]byte
	)
	sessions, groups, monitors := sessionBuf[:0], groupBuf[:0], monitorBuf[:0]
	clients := 0
	var buffer []byte
	for offset := 0; offset < len(out); {
		line, _, _ := bytes.Cut(out[offset:], newline)
		at := snapshotLine{offset, offset + len(line)}
		offset += len(line) + 1
		switch {
		case bytes.HasPrefix(line, sessionPrefix):
			sessions = append(sessions, at)
		case bytes.HasPrefix(line, windowPrefix):
			fields := splitFieldBytes(line, fieldBuf[:0])
			if len(fields) < windowFields {
				continue
			}
			id := snapshotLine{at.start + len(windowPrefix), at.start + len(windowPrefix) + len(fields[1])}
			if n := len(groups); n > 0 && groups[n-1].end+1 == at.start &&
				bytes.Equal(out[groups[n-1].session.start:groups[n-1].session.end], fields[1]) {
				groups[n-1].end = at.end
				groups[n-1].count++
			} else {
				groups = append(groups, windowGroup{session: id, start: at.start, end: at.end, count: 1})
			}
		case bytes.HasPrefix(line, clientPrefix):
			fields := splitFieldBytes(line, fieldBuf[:0:4])
			if len(fields) < 3 { //nolint:mnd // C, tty, session
				continue
			}
			if len(fields) > 3 && monitorFlagBytes(fields[3]) {
				monitors = append(monitors, fields[2])
				continue
			}
			clients++
		case bytes.HasPrefix(line, bufferPrefix):
			// Buffers are listed newest first: keep the first.
			if buffer == nil {
				buffer = line
			}
		}
	}

	if len(sessions) > 0 {
		snap.Sessions = make([]*Session, 0, len(sessions))
	}
	var groupBuf2 [stackSessions]int
	groupFor := assignGroups(groupBuf2[:0], out, sessions, groups)
	for i, at := range sessions {
		line := out[at.start:at.end]
		fields := splitFieldBytes(line, fieldBuf[:0])
		if len(fields) < 5 { //nolint:mnd // S, id, name, created, attached
			continue
		}
		attached := atoiBytes(fields[4])
		for _, m := range monitors {
			if attached > 0 && bytes.Equal(m, fields[1]) {
				attached--
			}
		}
		var windows []byte
		group := groupFor[i]
		if group >= 0 {
			windows = out[groups[group].start:groups[group].end]
		}
		was := prev.sessionAt(i, fields[1])
		if was != nil && was.line == string(line) && was.Attached == attached && group != -2 && !was.windowsMerged &&
			was.windowLines == string(windows) {
			// Nothing about it changed.
			snap.Sessions = append(snap.Sessions, was)
			continue
		}
		var s *Session
		if was != nil && was.line == string(line) {
			// Only its windows or clients changed: its line's strings are kept.
			s = &Session{line: was.line, ID: was.ID, Name: was.Name, Created: was.Created}
		} else {
			s = parseSession(string(line))
		}
		s.Attached = attached
		switch {
		case group == -2:
			mergeWindows(s, out, groups, fields[1])
		case was != nil && !was.windowsMerged && was.windowLines == string(windows):
			s.Windows, s.windowLines = was.Windows, was.windowLines
		case group >= 0:
			s.windowLines = string(windows)
			s.Windows = parseWindows(make([]Window, 0, groups[group].count), s.windowLines)
		}
		snap.Sessions = append(snap.Sessions, s)
	}

	if clients > 0 {
		snap.Clients = make([]ClientInfo, 0, clients)
		for offset := 0; offset < len(out); {
			line, _, _ := bytes.Cut(out[offset:], newline)
			offset += len(line) + 1
			if !bytes.HasPrefix(line, clientPrefix) {
				continue
			}
			fields := splitFieldBytes(line, fieldBuf[:0:4])
			if len(fields) < 3 || (len(fields) > 3 && monitorFlagBytes(fields[3])) { //nolint:mnd // as above
				continue
			}
			snap.Clients = append(snap.Clients, prev.client(fields[1], fields[2]))
		}
	}

	if buffer != nil {
		snap.Buffer = prev.buffer(buffer)
	}
	return snap
}

// stackSessions is how many sessions' lines a snapshot is parsed with room for on the
// stack. More are read all the same.
const stackSessions = 128

// assignGroups appends to dst, for each session, the index of the group of its windows,
// -1 if it has none, or -2 if it has more than one, which isn't how tmux lists them.
//
// tmux lists windows in the order it lists sessions, each session's together, so the
// groups are matched to the sessions in order. Only if that doesn't account for every
// group is each session looked for in all of them: matching every pair on every poll
// would grow with the square of the sessions.
func assignGroups(dst []int, out []byte, sessions []snapshotLine, groups []windowGroup) []int {
	is := func(g windowGroup, id []byte) bool { return bytes.Equal(out[g.session.start:g.session.end], id) }
	start := len(dst)
	next := 0
	for _, at := range sessions {
		id := sessionID(out[at.start:at.end])
		if next < len(groups) && is(groups[next], id) {
			dst = append(dst, next)
			next++
		} else {
			dst = append(dst, -1)
		}
	}
	if next == len(groups) {
		// Every group went to a session in turn: as IDs are unique, none has two.
		return dst
	}
	for i, at := range sessions {
		id := sessionID(out[at.start:at.end])
		dst[start+i] = -1
		for j, g := range groups {
			if !is(g, id) {
				continue
			}
			if dst[start+i] != -1 {
				dst[start+i] = -2
				break
			}
			dst[start+i] = j
		}
	}
	return dst
}

// sessionID returns the ID field of a session's line.
func sessionID(line []byte) []byte {
	_, rest, _ := bytes.Cut(line, separator)
	id, _, _ := bytes.Cut(rest, separator)
	return id
}

// parseSession parses a session's line.
//
//nolint:mnd // field counts are fixed by the format strings
func parseSession(line string) *Session {
	var buf [5]string
	fields := splitFields(line, buf[:0])
	s := &Session{line: line, ID: fields[1], Name: fields[2], Created: parseUnix(fields[3])}
	return s
}

// parseWindows appends the windows of window lines to windows, in index order.
func parseWindows(windows []Window, lines string) []Window {
	var buf [windowFields]string
	for rest := lines; rest != ""; {
		var line string
		line, rest, _ = strings.Cut(rest, "\n")
		if fields := splitFields(line, buf[:0]); len(fields) == windowFields {
			windows = append(windows, parseWindow(fields))
		}
	}
	if !slices.IsSortedFunc(windows, compareWindowIndex) {
		slices.SortStableFunc(windows, compareWindowIndex)
	}
	return windows
}

// mergeWindows gives s the windows of every group of its, for when they weren't listed
// together, as tmux lists them. They can't be compared as one text, so aren't shared.
func mergeWindows(s *Session, out []byte, groups []windowGroup, id []byte) {
	s.windowsMerged = true
	for _, g := range groups {
		if bytes.Equal(out[g.session.start:g.session.end], id) {
			s.Windows = parseWindows(s.Windows, string(out[g.start:g.end]))
		}
	}
}

// sessionAt returns the snapshot's session with an ID, looking first at index i, where it
// usually is, or nil. The snapshot may be nil.
func (s *Snapshot) sessionAt(i int, id []byte) *Session {
	if s == nil {
		return nil
	}
	if i < len(s.Sessions) && s.Sessions[i].ID == string(id) {
		return s.Sessions[i]
	}
	for _, sess := range s.Sessions {
		if sess.ID == string(id) {
			return sess
		}
	}
	return nil
}

// client returns a client with a tty and session, using the snapshot's strings if it
// has the same client. The snapshot may be nil.
func (s *Snapshot) client(tty, session []byte) ClientInfo {
	if s != nil {
		for _, c := range s.Clients {
			if c.TTY == string(tty) && c.SessionID == string(session) {
				return c
			}
		}
	}
	return ClientInfo{TTY: string(tty), SessionID: string(session)}
}

// buffer returns the buffer of a buffer line, the snapshot's own if it's the same. The
// snapshot may be nil.
//
//nolint:mnd // field counts are fixed by the format strings
func (s *Snapshot) buffer(line []byte) *Buffer {
	var buf [4][]byte
	fields := splitFieldBytes(line, buf[:0])
	if len(fields) < 4 {
		return nil
	}
	created, size := atoiBytes(fields[2]), atoiBytes(fields[3])
	if s != nil && s.Buffer != nil && s.Buffer.Name == string(fields[1]) && s.Buffer.Size == size &&
		s.Buffer.Created.Equal(unixTime(int64(created))) {
		return s.Buffer
	}
	return &Buffer{Name: string(fields[1]), Created: unixTime(int64(created)), Size: size}
}

// compareWindowIndex orders windows by index.
func compareWindowIndex(a, b Window) int { return cmp.Compare(a.Index, b.Index) }

// parseWindow parses the fields of a window's line.
func parseWindow(fields []string) Window {
	return Window{
		ID:       fields[2],
		Index:    atoi(fields[3]),
		Active:   fields[4] == "1",
		Activity: parseUnix(fields[5]),
		Command:  programName(fields[6]),
		Name:     programName(fields[7]),
		Path:     fields[8],
		PaneID:   fields[9],
		// tmux prints 1 for set flags.
		AlternateScreen: fields[10] == "1",
		MouseReporting:  fields[11] == "1",
		InMode:          fields[12] == "1",
		Scroll:          parseScroll(fields[13], fields[14], fields[15]),
		Panes:           atoi(fields[16]),
		Width:           atoi(fields[17]),
		// The title may contain the separator: splitFields left it in the last field.
		Title: fields[18],
	}
}

// monitorFlagBytes reports whether a client's flags, as tmux's client_flags gives them, are
// a control client's which takes no output: one which only watches the server, as this
// program's own control clients do, or those of another copy of it. It isn't showing a
// session to anyone, so it isn't counted among the session's clients.
func monitorFlagBytes(flags []byte) bool {
	return bytes.Contains(flags, []byte("control-mode")) && bytes.Contains(flags, []byte("no-output"))
}

// windowFields is how many fields a window's line has. The last, its title, may itself
// contain the separator.
const windowFields = 19

// splitFields splits a line of the snapshot at the separator into dst, which it returns.
// Once dst is full, the rest of the line, separators and all, is its last field.
func splitFields(line string, dst []string) []string {
	for len(dst) < cap(dst)-1 {
		field, rest, found := strings.Cut(line, fieldSep)
		if !found {
			break
		}
		dst = append(dst, field)
		line = rest
	}
	return append(dst, line)
}

// splitFieldBytes is splitFields, for a line still in the output's bytes.
func splitFieldBytes(line []byte, dst [][]byte) [][]byte {
	for len(dst) < cap(dst)-1 {
		field, rest, found := bytes.Cut(line, separator)
		if !found {
			break
		}
		dst = append(dst, field)
		line = rest
	}
	return append(dst, line)
}

// atoiBytes parses a decimal number, returning 0 for anything else: tmux prints nothing
// for unset numbers.
func atoiBytes(b []byte) int {
	n := 0
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0') //nolint:mnd // decimal
	}
	return n
}

// NewSession creates a detached session and returns its ID. An empty name lets tmux pick
// one. An empty dir starts it in the server's default directory.
func (c *Client) NewSession(ctx context.Context, name string, dir string) (string, error) {
	args := []string{"new-session", "-d", "-P", "-F", "#{session_id}"}
	if name != "" {
		args = append(args, "-s", name)
	}
	if dir != "" {
		args = append(args, "-c", c.ServerPath(ctx, dir))
	}
	out, err := c.run(ctx, c.withDefaultShell(ctx, args)...)
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
	if err != nil && strings.Contains(err.Error(), "no current client") {
		// tmux before 3.0, as on RHEL 8, renames the session and then fails to redraw a
		// client's status line when none is attached.
		return nil
	}
	return err
}

// SwitchClient makes the client on tty show session.
func (c *Client) SwitchClient(ctx context.Context, tty string, session string) error {
	_, err := c.run(ctx, "switch-client", "-c", tty, "-t", session)
	return err
}

// ShowBuffer returns the contents of a paste buffer.
func (c *Client) ShowBuffer(ctx context.Context, name string) (string, error) {
	return c.run(ctx, "show-buffer", "-b", name)
}

// NewWindow creates a window at the end of a session, makes it the session's current
// window and returns its ID. An empty dir starts it in the session's directory.
func (c *Client) NewWindow(ctx context.Context, session string, dir string) (string, error) {
	// session: is the next free index in the session.
	args := []string{"new-window", "-t", session + ":", "-P", "-F", "#{window_id}"}
	if dir != "" {
		args = append(args, "-c", c.ServerPath(ctx, dir))
	}
	out, err := c.run(ctx, c.withDefaultShell(ctx, args)...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// RenameWindow renames a window, given by its ID. tmux stops renaming it automatically
// after the program running in it.
func (c *Client) RenameWindow(ctx context.Context, window string, name string) error {
	_, err := c.run(ctx, "rename-window", "-t", window, "--", name)
	if err != nil && strings.Contains(err.Error(), "no current client") {
		// As for RenameSession: tmux before 3.0 renames it, then fails to redraw a
		// client's status line when none is attached.
		return nil
	}
	return err
}

// SelectWindow makes a window, given by its ID such as @3, its session's current
// window. Every client showing the session follows it.
func (c *Client) SelectWindow(ctx context.Context, window string) error {
	_, err := c.run(ctx, "select-window", "-t", window)
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

// atoi parses a decimal number, returning 0 if it's invalid.
func atoi(s string) int {
	if s == "" {
		// tmux prints nothing for unset numbers, such as scroll_position outside copy
		// mode: don't make an error to throw away.
		return 0
	}
	n, _ := strconv.Atoi(s)
	return n
}

// parseScroll parses the scroll formats. tmux prints an empty scroll_position outside copy
// mode.
func parseScroll(history, height, position string) ScrollState {
	var st ScrollState
	st.History, st.Height, st.Position = atoi(history), atoi(height), atoi(position)
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

// ErrCaptureCrashes is returned instead of capturing a pane on a tmux server whose
// capture-pane corrupts its heap and aborts, taking every session with it.
var ErrCaptureCrashes = errors.New("this tmux crashes when capturing a pane, which would end every " +
	"session on the server: upgrade tmux to save scrollback")

// CaptureCrashes reports whether a tmux version, as ServerVersion returns it, crashes in
// capture-pane. RHEL 10 ships a pre-release git snapshot of tmux, which reports itself as
// next-3.4, that does.
func CaptureCrashes(version string) bool {
	return strings.HasPrefix(strings.TrimSpace(version), "next-3.4")
}

// ServerVersion returns the version of the tmux server, as #{version} reports it. It's
// asked again at most once a minute.
func (c *Client) ServerVersion(ctx context.Context) (string, error) {
	c.mu.Lock()
	version, at := c.version, c.versionAt
	c.mu.Unlock()
	if version != "" && time.Since(at) < versionTTL {
		return version, nil
	}
	out, err := c.run(ctx, "display-message", "-p", "#{version}")
	version = strings.TrimSpace(out)
	if err == nil && version != "" {
		c.mu.Lock()
		c.version, c.versionAt = version, time.Now()
		c.mu.Unlock()
	}
	return version, err
}

// CaptureHistory returns all the history and visible text of a session's current pane,
// with lines tmux wrapped joined again and trailing blanks removed. tmux before 3.0 pads
// joined lines with spaces to the pane width. On a tmux whose capture-pane crashes, it
// returns ErrCaptureCrashes without trying.
func (c *Client) CaptureHistory(ctx context.Context, session string) (string, error) {
	version, err := c.ServerVersion(ctx)
	if err != nil {
		return "", err
	}
	if CaptureCrashes(version) {
		return "", errors.Wrap(ErrCaptureCrashes, "tmux "+version)
	}
	out, err := c.run(ctx, "capture-pane", "-p", "-J", "-S", "-", "-E", "-", "-t", session)
	if err != nil {
		return "", err
	}
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.Join(lines, "\n"), nil
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
