package tmux

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
)

// A control connection is a tmux client in control mode (tmux -C), kept running to send
// the commands polling runs several times a second, instead of starting tmux for each.
// tmux answers each command line with its output between %begin and %end, or %error,
// lines, and between them sends notifications, which are ignored.
//
// A control client must be attached to a session. It's attached with no-output, so it's
// sent none of the panes' output, and ignore-size, so it doesn't change windows' sizes.
// tmux counts it among the session's clients: Snapshot leaves out such clients, which only
// watch. It ends when its
// session does; the next command starts another, on another session.

// errControlClosed is returned for commands a control connection could not answer
// because it ended.
var errControlClosed = errors.New("tmux control client ended")

// controlCommands are the commands sent through the control connection, with
// display-message when targetedPrint says so. They only read, and each names what it
// reads, so none of them depends on which client runs it: inside the control
// connection, a command without a target would act on the control client's own session.
//
//nolint:gochecknoglobals // a fixed set
var controlCommands = map[string]bool{
	"list-sessions": true, "list-windows": true, "list-panes": true,
	"list-clients": true, "list-buffers": true, "capture-pane": true,
}

// controlRetry is how long after a control connection first fails to start commands are
// run without one. It doubles with each failure in a row, up to controlRetryMax: the first
// may only be for a session which has just ended.
const (
	controlRetry    = time.Second
	controlRetryMax = time.Minute
)

// controlStartTimeout limits how long starting a control connection may take.
const controlStartTimeout = 10 * time.Second

// controlCall is a call's commands, waiting for their replies.
type controlCall struct {
	// remaining is how many replies are still to come. The reader appends them to out,
	// or their messages to failed, and signals done when the last has come or the
	// client has ended, setting closed. Until then, only the reader touches them.
	remaining int
	out       []byte
	failed    []string
	closed    bool
	done      chan struct{}
	// deadline is when the call's replies must have come by, or zero for no limit.
	deadline time.Time
}

// controlCalls keeps calls, with their buffers, for reuse, to spare the garbage. It's a
// free list rather than a sync.Pool, which the garbage collector empties: calls made again
// after each collection, and their buffers grown again to a snapshot's size, were most of
// what polling allocated.
//
//nolint:gochecknoglobals // calls are reused, with their buffers, to spare the garbage
var controlCalls callList

// callList is a free list of calls. It keeps a few: about as many as run at once.
type callList struct {
	mu    sync.Mutex
	calls []*controlCall
}

// maxFreeCalls is the most calls kept, and maxKeptOutput the largest output buffer kept
// with one: a long capture's isn't, to be held until the next.
const (
	maxFreeCalls  = 8
	maxKeptOutput = 1 << 20
)

// Get returns a free call, or a new one.
func (l *callList) Get() *controlCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n := len(l.calls); n > 0 {
		call := l.calls[n-1]
		l.calls[n-1] = nil
		l.calls = l.calls[:n-1]
		return call
	}
	return &controlCall{done: make(chan struct{}, 1)}
}

// Put frees a call for reuse.
func (l *callList) Put(call *controlCall) {
	if cap(call.out) > maxKeptOutput {
		call.out = nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.calls) < maxFreeCalls {
		l.calls = append(l.calls, call)
	}
}

// controlConn is a running control client.
type controlConn struct {
	stdin io.WriteCloser
	// stop ends the client's process.
	stop func()
	// name is the client's name, as tmux's client_name gives it.
	name string

	// writeMu is held while a command line is written, from line.
	writeMu sync.Mutex
	line    []byte
	mu      sync.Mutex
	// pending are the calls waiting for replies, in the order their commands were sent.
	pending []*controlCall
	// watchdog ends the client if the earliest deadline of the pending calls passes:
	// it's stuck, as a remote one whose network went away. It's one timer, reset as
	// calls come and go, rather than one made with each call.
	watchdog *time.Timer
	// closed is set once the client has ended, when done is closed.
	closed bool
	done   chan struct{}
}

// startControl starts a control client with the given standard input and output,
// already running as start says, and asks it its name.
func startControl(ctx context.Context, stdin io.WriteCloser, stdout io.Reader, stop func()) (*controlConn, error) {
	cc := &controlConn{stdin: stdin, stop: stop, done: make(chan struct{})}
	cc.watchdog = time.AfterFunc(time.Hour, cc.close)
	cc.watchdog.Stop()
	go cc.read(stdout)
	ctx, cancel := context.WithTimeout(ctx, controlStartTimeout)
	defer cancel()
	out, err := cc.run(ctx, [][]string{{"display-message", "-p", "#{client_name}"}}, controlStartTimeout, nil)
	if err != nil {
		cc.close()
		return nil, err
	}
	cc.name = strings.TrimSpace(out)
	return cc, nil
}

// alive reports whether the client is still running.
func (cc *controlConn) alive() bool {
	select {
	case <-cc.done:
		return false
	default:
		return true
	}
}

// close ends the client. Commands waiting for replies get errControlClosed.
func (cc *controlConn) close() {
	_ = cc.stdin.Close()
	cc.stop()
}

// run sends commands, each a tmux command and its arguments, and returns their output
// joined, as tmux would print it run with them separated by ;. A command which fails
// makes the error, with its message. The replies must come by ctx's deadline or, if it
// has none, within timeout, if that isn't zero, or the client is ended. If use isn't nil,
// it's given the output instead, which is only good until it returns.
func (cc *controlConn) run(ctx context.Context, commands [][]string, timeout time.Duration, use func([]byte)) (string, error) {
	call := controlCalls.Get()
	call.remaining, call.out, call.failed, call.closed = len(commands), call.out[:0], call.failed[:0], false
	call.deadline = time.Time{}
	if deadline, ok := ctx.Deadline(); ok {
		call.deadline = deadline
	} else if timeout > 0 {
		call.deadline = time.Now().Add(timeout)
	}

	// Commands are answered in the order they're sent: the call is queued and its line
	// written in turn, so concurrent calls can't swap replies. The lock the reader needs
	// isn't held while writing, which a stuck client can block.
	cc.writeMu.Lock()
	cc.mu.Lock()
	if cc.closed {
		cc.mu.Unlock()
		cc.writeMu.Unlock()
		controlCalls.Put(call)
		return "", errControlClosed
	}
	cc.pending = append(cc.pending, call)
	cc.armWatchdog()
	cc.mu.Unlock()
	line := cc.line[:0]
	for _, cmd := range commands {
		for j, arg := range cmd {
			if j > 0 {
				line = append(line, ' ')
			}
			line = appendControlArg(line, arg)
		}
		line = append(line, '\n')
	}
	cc.line = line
	var err error
	if call.deadline.IsZero() && ctx.Done() != nil {
		// No deadline for the watchdog: a write the client doesn't take is given up
		// when ctx is cancelled.
		stop := context.AfterFunc(ctx, cc.close)
		_, err = cc.stdin.Write(line)
		stop()
	} else {
		_, err = cc.stdin.Write(line)
	}
	cc.writeMu.Unlock()
	if err != nil {
		// The call is let go as the client ends.
		cc.close()
		return "", errors.Wrap(errControlClosed, err.Error())
	}

	select {
	case <-call.done:
	case <-ctx.Done():
		// The connection may be stuck, as a remote one whose network went away:
		// another is started for the next command. The call is left to the reader.
		cc.close()
		return "", ctx.Err()
	}
	defer controlCalls.Put(call)
	switch {
	case call.closed && !call.deadline.IsZero() && !time.Now().Before(call.deadline):
		// The watchdog ended it: the commands timed out, and would again run the usual
		// way.
		return "", context.DeadlineExceeded
	case call.closed:
		return "", errControlClosed
	case len(call.failed) > 0:
		return "", &controlError{msg: strings.Join(call.failed, "\n")}
	}
	if use != nil {
		// To keep allocations down on long runs: polls read the output where it is,
		// without a copy.
		use(call.out)
		return "", nil
	}
	return string(call.out), nil
}

// controlError is a command's failure, with tmux's message.
type controlError struct{ msg string }

func (e *controlError) Error() string { return e.msg }

// read reads the client's output until it ends, handing each command's reply to its
// call.
func (cc *controlConn) read(stdout io.Reader) {
	defer func() {
		cc.mu.Lock()
		cc.closed = true
		for _, call := range cc.pending {
			call.closed = true
			call.done <- struct{}{}
		}
		cc.pending = nil
		cc.watchdog.Stop()
		cc.mu.Unlock()
		close(cc.done)
		cc.stop()
	}()
	r := bufio.NewReaderSize(stdout, 64*1024) //nolint:mnd // a typical reply's size
	var (
		in    bool
		guard []byte
		// call is the call the block is for, nil for a block not this client's, and
		// start where in its output the block began.
		call  *controlCall
		start int
		long  []byte
	)
	for {
		line, err := readLine(r, &long)
		if err != nil {
			return
		}
		if !in {
			// %begin time number flags: flags is 1 for commands this client sent, and 0
			// for the attach it was started with.
			if rest, ok := bytes.CutPrefix(line, []byte("%begin ")); ok {
				in, guard, call = true, append(guard[:0], rest...), nil
				if bytes.HasSuffix(rest, []byte(" 1")) {
					cc.mu.Lock()
					if len(cc.pending) > 0 {
						call = cc.pending[0]
						start = len(call.out)
					}
					cc.mu.Unlock()
				}
			} else if bytes.Equal(line, []byte("%exit")) || bytes.HasPrefix(line, []byte("%exit ")) {
				return
			}
			continue
		}
		// The block ends with %end or %error and the same time, number and flags, which
		// output can't be mistaken for.
		end, isEnd := bytes.CutPrefix(line, []byte("%end "))
		failure, isError := bytes.CutPrefix(line, []byte("%error "))
		if (isEnd && bytes.Equal(end, guard)) || (isError && bytes.Equal(failure, guard)) {
			in = false
			if call != nil {
				if isError {
					call.failed = append(call.failed, strings.TrimSpace(string(call.out[start:])))
					call.out = call.out[:start]
				}
				cc.replied(call)
			}
			continue
		}
		if call != nil {
			call.out = append(call.out, line...)
			call.out = append(call.out, '\n')
		}
	}
}

// replied counts a reply to the first call waiting, and lets it go if that was its last.
func (cc *controlConn) replied(call *controlCall) {
	call.remaining--
	if call.remaining > 0 {
		return
	}
	cc.mu.Lock()
	// Shifted down, not resliced, so that appending reuses the space.
	n := copy(cc.pending, cc.pending[1:])
	cc.pending[n] = nil
	cc.pending = cc.pending[:n]
	cc.armWatchdog()
	cc.mu.Unlock()
	call.done <- struct{}{}
}

// armWatchdog sets the watchdog for the earliest deadline of the pending calls, or stops
// it if they have none. cc.mu must be held.
func (cc *controlConn) armWatchdog() {
	var earliest time.Time
	for _, call := range cc.pending {
		if !call.deadline.IsZero() && (earliest.IsZero() || call.deadline.Before(earliest)) {
			earliest = call.deadline
		}
	}
	if earliest.IsZero() {
		cc.watchdog.Stop()
		return
	}
	cc.watchdog.Reset(time.Until(earliest))
}

// readLine reads a line of any length, without its newline. The line is good until the
// next read. long is kept for lines longer than r's buffer.
func readLine(r *bufio.Reader, long *[]byte) ([]byte, error) {
	line, err := r.ReadSlice('\n')
	if err == nil {
		return line[:len(line)-1], nil
	}
	if !errors.Is(err, bufio.ErrBufferFull) {
		return nil, err
	}
	*long = append((*long)[:0], line...)
	for errors.Is(err, bufio.ErrBufferFull) {
		line, err = r.ReadSlice('\n')
		*long = append(*long, line...)
	}
	if err != nil {
		return nil, err
	}
	return (*long)[:len(*long)-1], nil
}

// quoteControlArg quotes an argument for tmux's command parser: in single quotes, which
// keep everything as it is, unless it's a plain word. A single quote is ended, given
// escaped and begun again.
func quoteControlArg(arg string) string {
	return string(appendControlArg(nil, arg))
}

// appendControlArg appends arg to dst, quoted as quoteControlArg does.
func appendControlArg(dst []byte, arg string) []byte {
	if arg != "" && !strings.ContainsAny(arg, " \t\n'\"\\;#{}$~") {
		return append(dst, arg...)
	}
	dst = append(dst, '\'')
	for i := range len(arg) {
		if arg[i] == '\'' {
			dst = append(dst, `'\''`...)
		} else {
			dst = append(dst, arg[i])
		}
	}
	return append(dst, '\'')
}

// splitCommands splits a command line of tmux commands separated by ; arguments,
// appending them to commands.
func splitCommands(commands [][]string, args []string) [][]string {
	start := 0
	for i, arg := range args {
		if arg == ";" {
			commands = append(commands, args[start:i])
			start = i + 1
		}
	}
	return append(commands, args[start:])
}

// controllable reports whether every command of args can be sent through the control
// connection.
func controllable(commands [][]string) bool {
	for _, cmd := range commands {
		if len(cmd) == 0 {
			return false
		}
		if cmd[0] == "display-message" {
			if !targetedPrint(cmd[1:]) {
				return false
			}
			continue
		}
		if !controlCommands[cmd[0]] {
			return false
		}
	}
	return true
}

// targetedPrint reports whether display-message arguments print, with -p, about a target
// named with -t. Only then is it sent through the control connection: without a target it
// would print about the control client's own session. ServerVersion's, which has none,
// isn't either, as starting a control connection asks it: it uses a running one itself.
func targetedPrint(args []string) bool {
	var print, target bool
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-p":
			print = true
		case "-t":
			target = i+1 < len(args)
			i++
		default:
			if !strings.HasPrefix(args[i], "-") {
				// The message: the options are over.
				return print && target
			}
			// Other options, as -c or -d, or a combined form: not sent.
			return false
		}
	}
	return print && target
}

// supportsControl reports whether a tmux version, as ServerVersion returns it, can attach
// a control client without output or a say in window sizes, as tmux 3.2 and later can.
func supportsControl(version string) bool {
	var major, minor int
	v := strings.TrimPrefix(strings.TrimSpace(version), "next-")
	if _, err := fmt.Sscanf(v, "%d.%d", &major, &minor); err != nil {
		return false
	}
	return major > 3 || (major == 3 && minor >= 2) //nolint:mnd // tmux 3.2
}

// controlFor returns the client's control connection, starting one if there's none and
// the server can have one, or nil to run commands the usual way.
func (c *Client) controlFor(ctx context.Context) *controlConn {
	c.controlMu.Lock()
	defer c.controlMu.Unlock()
	if c.control != nil {
		if c.control.alive() {
			return c.control
		}
		c.setControl(nil)
	}
	if c.controlOff || time.Now().Before(c.controlRetryAt) {
		return nil
	}
	c.mu.Lock()
	snap := c.lastSnapshot
	c.mu.Unlock()
	session := controlSession(snap)
	if session == "" {
		// No session to attach to: there may be no server either.
		return nil
	}
	version, err := c.ServerVersion(ctx)
	if err != nil {
		return nil
	}
	if !supportsControl(version) {
		c.controlRetryAt = time.Now().Add(versionTTL)
		return nil
	}
	cc, err := c.startControl(ctx, session)
	if err != nil {
		c.controlRetryAt = time.Now().Add(min(controlRetry<<c.controlFailures, controlRetryMax))
		c.controlFailures = min(c.controlFailures+1, 16) //nolint:mnd // far past the maximum
		return nil
	}
	c.setControl(cc)
	c.controlFailures = 0
	return cc
}

// controlSession picks the session to attach a control client to: the one most recently
// active, since attaching counts as using it, as when tmux attach without a target picks
// a session.
func controlSession(snap *Snapshot) string {
	if snap == nil {
		return ""
	}
	best := ""
	var at time.Time
	for _, s := range snap.Sessions {
		if a := s.Activity(); best == "" || a.After(at) {
			best, at = s.ID, a
		}
	}
	return best
}

// controlArgs are the tmux arguments of a control client attached to session.
func controlArgs(session string) []string {
	return []string{"-C", "attach-session", "-t", session, "-f", "no-output,ignore-size"}
}

// startControl starts a control client attached to session: through the agent, if the
// server's route has one, or else as a process, tmux itself or ssh running it.
func (c *Client) startControl(ctx context.Context, session string) (*controlConn, error) {
	if c.SSH != nil {
		if root, ac := c.SSH.agentClient(); ac != nil {
			argv := c.SSH.innerArgv(false, false, c.tmuxArgv(controlArgs(session)...))
			stdinR, stdinW := io.Pipe()
			stdoutR, stdoutW := io.Pipe()
			pipeCtx, cancel := context.WithCancel(context.Background())
			go func() {
				err := ac.Pipe(pipeCtx, stdinR, stdoutW, argv)
				if err != nil {
					root.agentFailed(ac, err)
				}
				_ = stdoutW.CloseWithError(errControlClosed)
			}()
			return startControl(ctx, stdinW, stdoutR, cancel)
		}
		var cancel context.CancelFunc
		var err error
		ctx, cancel, err = c.SSH.connect(ctx)
		defer cancel()
		if err != nil {
			return nil, err
		}
	}
	argv := c.Argv(controlArgs(session)...)
	if c.SSH == nil {
		argv = controlArgv(argv)
	}
	cmd := Command(context.Background(), argv)
	cmd.Env = Environ(os.Environ())
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, errors.Wrap(err, "starting a tmux control client")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.Wrap(err, "starting a tmux control client")
	}
	if err := cmd.Start(); err != nil {
		return nil, errors.Wrap(err, "starting a tmux control client")
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = cmd.Process.Kill()
			go func() { _ = cmd.Wait() }()
		})
	}
	return startControl(ctx, stdin, stdout, stop)
}

// runControl runs args through the control connection, if they can be and there is one.
// It reports whether it did.
func (c *Client) runControl(ctx context.Context, args []string, use func([]byte)) (string, bool, error) {
	// To keep allocations down on long runs, the commands are split into space on the
	// stack: polls have a few.
	var buf [8][]string
	commands := splitCommands(buf[:0], args)
	if !controllable(commands) {
		return "", false, nil
	}
	cc := c.controlFor(ctx)
	if cc == nil {
		return "", false, nil
	}
	out, err := cc.run(ctx, commands, c.Timeout, use)
	// run returns a *controlError itself, never wrapped. It's asserted rather than found
	// with errors.As, whose target would be allocated on every call, to keep allocations
	// down on long runs.
	ce, isControlError := err.(*controlError) //nolint:errorlint // never wrapped
	switch {
	case err == nil:
		return out, true, nil
	case isControlError:
		if isNoSessionError(ce.msg) {
			return "", true, errors.Wrap(ErrNoSession, ce.msg)
		}
		return "", true, errors.Errorf("tmux %s: %s", strings.Join(args, " "), ce.msg)
	case errors.Is(err, errControlClosed):
		// It ended, as when its session did: run the command the usual way.
		return "", false, nil
	default:
		return "", true, err
	}
}

// setControl sets the control connection. controlMu must be held.
func (c *Client) setControl(cc *controlConn) {
	c.control = cc
	c.running.Store(cc)
}

// runningControl returns the client's control connection if one is running, without
// starting one, or waiting for one starting.
func (c *Client) runningControl() *controlConn {
	cc := c.running.Load()
	if cc == nil || !cc.alive() {
		return nil
	}
	return cc
}

// controlName returns the name of the client's control client, if it has one running.
func (c *Client) controlName() string {
	c.controlMu.Lock()
	defer c.controlMu.Unlock()
	if c.control == nil || !c.control.alive() {
		return ""
	}
	return c.control.name
}

// SetControl turns the control connection on or off. Off, the running one is ended, and
// every command starts tmux. It's on to begin with.
func (c *Client) SetControl(on bool) {
	c.controlMu.Lock()
	defer c.controlMu.Unlock()
	c.controlOff = !on
	c.controlRetryAt, c.controlFailures = time.Time{}, 0
	if !on && c.control != nil {
		c.control.close()
		c.setControl(nil)
	}
}

// Close ends the client's control connection, if it has one. The client can still be
// used: it starts another if it needs one.
func (c *Client) Close() {
	c.controlMu.Lock()
	defer c.controlMu.Unlock()
	if c.control != nil {
		c.control.close()
		c.setControl(nil)
	}
}
