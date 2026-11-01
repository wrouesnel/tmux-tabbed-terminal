package agent

import (
	"context"
	"io"
	"sync"

	"github.com/pkg/errors"
	"google.golang.org/grpc"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/agent/agentpb"
)

// Terminal is a program running on a pseudo-terminal on the agent's host. Reading it
// returns what the program writes, and writing it types into the program. It has the
// methods of a termwidget pty.
type Terminal struct {
	stream grpc.BidiStreamingClient[agentpb.AttachRequest, agentpb.AttachResponse]
	cancel context.CancelFunc
	tty    string

	// sendMu serializes sends: a gRPC stream allows one sender at a time.
	sendMu sync.Mutex

	// pending is output received but not yet read.
	pending []byte
	readErr error
	exited  chan struct{}
	exit    *agentpb.Exit
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
	ctx, cancel := context.WithCancel(ctx)
	stream, err := c.agent.Attach(ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	err = stream.Send(&agentpb.AttachRequest{Event: &agentpb.AttachRequest_Start{Start: &agentpb.AttachStart{
		Command: &agentpb.Command{Argv: argv},
		Size:    terminalSize(opts.Cols, opts.Rows),
		Env:     opts.Env,
	}}})
	if err != nil {
		cancel()
		return nil, err
	}
	resp, err := stream.Recv()
	if err != nil {
		cancel()
		return nil, err
	}
	started := resp.GetStarted()
	if started == nil {
		cancel()
		return nil, errors.New("the agent didn't say the program started")
	}
	return &Terminal{stream: stream, cancel: cancel, tty: started.GetTty(), exited: make(chan struct{})}, nil
}

func terminalSize(cols, rows int) *agentpb.TerminalSize {
	return &agentpb.TerminalSize{Cols: uint32(max(cols, 0)), Rows: uint32(max(rows, 0))} //nolint:gosec // clamped
}

// Read returns what the program wrote. It returns io.EOF once the program has exited and
// everything it wrote has been read.
func (t *Terminal) Read(p []byte) (int, error) {
	for len(t.pending) == 0 {
		if t.readErr != nil {
			return 0, t.readErr
		}
		resp, err := t.stream.Recv()
		if err != nil {
			t.finish(nil, err)
			continue
		}
		switch ev := resp.GetEvent().(type) {
		case *agentpb.AttachResponse_Output:
			t.pending = ev.Output
		case *agentpb.AttachResponse_Exit:
			t.finish(ev.Exit, io.EOF)
		}
	}
	n := copy(p, t.pending)
	t.pending = t.pending[n:]
	return n, nil
}

// finish records the end of the program's output: how it exited, if it did, and the
// error further reads return.
func (t *Terminal) finish(exit *agentpb.Exit, err error) {
	t.readErr = err
	if exit != nil {
		t.exit = exit
	}
	select {
	case <-t.exited:
	default:
		close(t.exited)
	}
}

// Write types into the program.
func (t *Terminal) Write(p []byte) (int, error) {
	if err := t.send(&agentpb.AttachRequest{Event: &agentpb.AttachRequest_Input{Input: append([]byte(nil), p...)}}); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Resize tells the program the terminal's new size in cells.
func (t *Terminal) Resize(cols, rows int) error {
	return t.send(&agentpb.AttachRequest{Event: &agentpb.AttachRequest_Resize{Resize: terminalSize(cols, rows)}})
}

func (t *Terminal) send(req *agentpb.AttachRequest) error {
	t.sendMu.Lock()
	defer t.sendMu.Unlock()
	return t.stream.Send(req)
}

// Name is the terminal device on the agent's host, such as /dev/pts/3.
func (t *Terminal) Name() string { return t.tty }

// Close hangs the program up.
func (t *Terminal) Close() error {
	t.cancel()
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
	return exitError(int(t.exit.GetCode()), t.exit.GetSignal(), nil)
}
