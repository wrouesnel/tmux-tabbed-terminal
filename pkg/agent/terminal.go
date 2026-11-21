package agent

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"sync"

	"github.com/pkg/errors"
)

// Terminal is a program running on a pseudo-terminal on the agent's host. Reading it
// returns what the program writes, and writing it types into the program. It has the
// methods of a termwidget pty.
type Terminal struct {
	call *clientCall
	tty  string

	// pending is output received but not yet read.
	pending []byte
	readErr error
	exited  chan struct{}
	exit    *exit

	resizeMu  sync.Mutex
	resizeBuf [resizeSize]byte
}

// TerminalOptions are how to start a program on a terminal.
type TerminalOptions struct {
	Cols, Rows int
	// Env is environment variables to set, beyond the agent's own, such as TERM.
	Env map[string]string
}

// Attach runs a program on a new pseudo-terminal on the agent's host. Cancelling ctx, or
// closing the terminal, hangs the program up.
func (c *Client) Attach(ctx context.Context, argv []string, opts TerminalOptions) (*Terminal, error) {
	cc, rep, err := c.open(ctx, &request{
		Call: callAttach, Argv: argv, Cols: max(opts.Cols, 0), Rows: max(opts.Rows, 0), Env: opts.Env,
	})
	if err != nil {
		return nil, err
	}
	return &Terminal{call: cc, tty: rep.TTY, exited: make(chan struct{})}, nil
}

// Read returns what the program wrote. It returns io.EOF once the program has exited and
// everything it wrote has been read.
func (t *Terminal) Read(p []byte) (int, error) {
	for len(t.pending) == 0 {
		if t.readErr != nil {
			return 0, t.readErr
		}
		typ, payload, err := t.call.r.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = errors.New("the agent ended the terminal without saying how the program exited")
			}
			t.finish(nil, t.call.fail(err))
			continue
		}
		switch typ {
		case frameOutput:
			t.pending = payload
		case frameExit:
			var ex exit
			if err := json.Unmarshal(payload, &ex); err != nil {
				t.finish(nil, &ConnError{Err: err})
				continue
			}
			t.finish(&ex, io.EOF)
		case frameReply:
			t.finish(nil, replyError(payload))
		}
	}
	n := copy(p, t.pending)
	t.pending = t.pending[n:]
	return n, nil
}

// finish records the end of the program's output: how it exited, if it did, and the
// error further reads return.
func (t *Terminal) finish(ex *exit, err error) {
	t.readErr = err
	if ex != nil {
		t.exit = ex
	}
	select {
	case <-t.exited:
	default:
		close(t.exited)
		t.call.close()
	}
}

// Write types into the program.
func (t *Terminal) Write(p []byte) (int, error) {
	if err := t.call.w.Write(frameInput, p); err != nil {
		return 0, t.call.fail(err)
	}
	return len(p), nil
}

// Resize tells the program the terminal's new size in cells.
func (t *Terminal) Resize(cols, rows int) error {
	t.resizeMu.Lock()
	defer t.resizeMu.Unlock()
	binary.BigEndian.PutUint32(t.resizeBuf[:], uint32(max(cols, 0)))  //nolint:gosec // clamped
	binary.BigEndian.PutUint32(t.resizeBuf[4:], uint32(max(rows, 0))) //nolint:gosec // clamped
	if err := t.call.w.Write(frameResize, t.resizeBuf[:]); err != nil {
		return t.call.fail(err)
	}
	return nil
}

// Name is the terminal device on the agent's host, such as /dev/pts/3.
func (t *Terminal) Name() string { return t.tty }

// Close hangs the program up.
func (t *Terminal) Close() error {
	t.call.abort()
	return nil
}

// Wait waits until the program's output has all been read, and returns how it exited: nil
// if it succeeded, an *ExitError if it failed, or the error which ended the call, such as
// the connection to the agent closing.
func (t *Terminal) Wait() error {
	<-t.exited
	if t.exit == nil {
		if errors.Is(t.readErr, io.EOF) {
			return errors.New("the agent ended the terminal without saying how the program exited")
		}
		return t.readErr
	}
	return exitError(t.exit.Code, t.exit.Signal, nil)
}
