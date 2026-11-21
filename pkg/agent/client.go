package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	"github.com/pkg/errors"
	"github.com/wrouesnel/stdiomux"
	"github.com/wrouesnel/stdiomux/yamux"
)

// Client talks to an agent over a pair of pipes, such as the standard output and input of
// the ssh running it.
type Client struct {
	session *yamux.Session
}

// NewClient talks to an agent which reads r and writes w. Closing the client closes the
// closers, such as the pipes, which should end the agent.
func NewClient(r io.Reader, w io.Writer, closers ...io.Closer) (*Client, error) {
	session, err := stdiomux.Client(stdiomux.NewConn(r, w, closers...), linkConfig(true))
	if err != nil {
		for _, c := range closers {
			_ = c.Close()
		}
		return nil, errors.Wrap(err, "talking to the agent")
	}
	return &Client{session: session}, nil
}

// Close stops talking to the agent.
func (c *Client) Close() error {
	return c.session.Close()
}

// Done is closed once the connection to the agent has closed, from either end.
func (c *Client) Done() <-chan struct{} { return c.session.CloseChan() }

// clientCall is a call to the agent: one stream.
type clientCall struct {
	stream *yamux.Stream
	r      *stdiomux.FrameReader
	w      *stdiomux.FrameWriter
	ctx    context.Context
	// stop stops ctx resetting the stream.
	stop func() bool
}

// open starts a call, and returns it once the agent has replied that it started.
// Cancelling ctx resets the stream, which cancels the call, until it's closed.
func (c *Client) open(ctx context.Context, req *request) (*clientCall, *reply, error) {
	stream, err := c.session.OpenStream(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, &ConnError{Err: err}
	}
	cc := &clientCall{
		stream: stream, r: stdiomux.NewFrameReader(stream), w: stdiomux.NewFrameWriter(stream), ctx: ctx,
		stop: context.AfterFunc(ctx, func() { _ = stream.Reset() }),
	}
	if err := cc.w.WriteJSON(frameRequest, req); err != nil {
		cc.abort()
		return nil, nil, cc.fail(err)
	}
	var rep reply
	if err := cc.r.NextJSON(frameReply, &rep); err != nil {
		cc.abort()
		return nil, nil, cc.fail(err)
	}
	if rep.Error != "" {
		cc.close()
		return nil, nil, &Error{Code: rep.Code, Message: rep.Error}
	}
	return cc, &rep, nil
}

// close ends the call once it's done.
func (cc *clientCall) close() {
	cc.stop()
	_ = cc.stream.Close()
}

// abort ends the call before it's done, which cancels it on the agent.
func (cc *clientCall) abort() {
	cc.stop()
	_ = cc.stream.Reset()
}

// fail is the error a call failed with: the context's if it was cancelled, the agent's if
// it said why, and otherwise the connection's.
func (cc *clientCall) fail(err error) error {
	var agentErr *Error
	var exitErr *ExitError
	var connErr *ConnError
	switch {
	case cc.ctx.Err() != nil:
		return cc.ctx.Err()
	case errors.As(err, &agentErr), errors.As(err, &exitErr), errors.As(err, &connErr):
		return err
	}
	return &ConnError{Err: err}
}

// replyError is the error a reply after the first says the call failed with.
func replyError(payload []byte) error {
	var rep reply
	if err := json.Unmarshal(payload, &rep); err != nil || rep.Error == "" {
		return &Error{Code: CodeInternal, Message: "the agent's call failed"}
	}
	return &Error{Code: rep.Code, Message: rep.Error}
}

// Hello is the agent's version and platform.
type Hello struct {
	// Version is the version of the application the agent was built with.
	Version string
	// OS and Arch are the platform, as Go names it, such as linux and amd64.
	OS, Arch string
	// Home is the home directory of the user it runs as.
	Home string
}

// Hello returns the agent's version and platform.
func (c *Client) Hello(ctx context.Context, version string) (*Hello, error) {
	cc, rep, err := c.open(ctx, &request{Call: callHello, Version: version})
	if err != nil {
		return nil, err
	}
	cc.close()
	return &Hello{Version: rep.Version, OS: rep.OS, Arch: rep.Arch, Home: rep.Home}, nil
}

// Result is how a program run by the agent went.
type Result struct {
	Stdout, Stderr []byte
	// Code is its exit status, or -1 if a signal killed it.
	Code   int
	Signal string
}

// Err is an error for a program that failed, or nil if it succeeded.
func (r *Result) Err() error {
	return exitError(r.Code, r.Signal, r.Stderr)
}

// exitError is the error of a program that ended as it did, or nil if it succeeded.
func exitError(code int, signal string, stderr []byte) error {
	var err error
	switch {
	case signal != "":
		err = errors.Errorf("killed by %s", signal)
	case code != 0:
		err = errors.Errorf("exit status %d", code)
	default:
		return nil
	}
	return &ExitError{err: err, Stderr: string(stderr)}
}

// ExitError is the error of a program that ran and failed.
type ExitError struct {
	err    error
	Stderr string
}

func (e *ExitError) Error() string { return e.err.Error() }

// Run runs a program on the agent's host, with stdin as its standard input. A program
// that fails is a result, not an error: errors are for programs that can't start.
func (c *Client) Run(ctx context.Context, argv []string, stdin []byte) (*Result, error) {
	var stdout bytes.Buffer
	ex, err := c.run(ctx, bytes.NewReader(stdin), &stdout, argv)
	if err != nil {
		return nil, err
	}
	return &Result{Stdout: stdout.Bytes(), Stderr: ex.Stderr, Code: ex.Code, Signal: ex.Signal}, nil
}

// Stream runs a program on the agent's host, copying its standard output to w as it comes.
// It returns an *ExitError if the program fails.
func (c *Client) Stream(ctx context.Context, w io.Writer, argv []string) error {
	return c.Pipe(ctx, nil, w, argv)
}

// Pipe is Stream, with r as the program's standard input.
func (c *Client) Pipe(ctx context.Context, r io.Reader, w io.Writer, argv []string) error {
	ex, err := c.run(ctx, r, w, argv)
	if err != nil {
		return err
	}
	return exitError(ex.Code, ex.Signal, ex.Stderr)
}

// run runs a program, with r, if it isn't nil, as its standard input, copying its
// standard output to w, and returns how it exited.
func (c *Client) run(ctx context.Context, r io.Reader, w io.Writer, argv []string) (*exit, error) {
	cc, _, err := c.open(ctx, &request{Call: callRun, Argv: argv})
	if err != nil {
		return nil, err
	}
	// read is the error reading r, other than its end. An error sending is the call's,
	// which receiving reports.
	read := make(chan error, 1)
	if r == nil {
		var err error
		if werr := cc.w.Write(frameEnd, nil); werr != nil {
			err = &ConnError{Err: werr}
		}
		read <- err
	} else {
		go func() { read <- sendInput(cc, r) }()
	}
	ex, err := receiveOutput(cc, w)
	if err != nil {
		cc.abort()
	} else {
		cc.close()
	}
	if rerr := <-read; rerr != nil && err == nil {
		var connErr *ConnError
		if !errors.As(rerr, &connErr) {
			return nil, rerr
		}
	}
	return ex, err
}

// sendInput sends what r holds as the program's standard input. If reading r fails, it
// cancels the call, which kills the program, so it doesn't take part of the input for
// the whole.
func sendInput(cc *clientCall, r io.Reader) error {
	buf := make([]byte, streamChunk)
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			if err := cc.w.Write(frameInput, buf[:n]); err != nil {
				return &ConnError{Err: err}
			}
		}
		switch {
		case errors.Is(rerr, io.EOF):
			if err := cc.w.Write(frameEnd, nil); err != nil {
				return &ConnError{Err: err}
			}
			return nil
		case rerr != nil:
			_ = cc.stream.Reset()
			return rerr
		}
	}
}

// receiveOutput copies the output of a program the agent runs to w, and returns how it
// exited.
func receiveOutput(cc *clientCall, w io.Writer) (*exit, error) {
	for {
		typ, payload, err := cc.r.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, &ConnError{Err: errors.New("the agent ended the call without saying how the program exited")}
			}
			return nil, cc.fail(err)
		}
		switch typ {
		case frameOutput:
			if _, err := w.Write(payload); err != nil {
				// Cancelling the call kills the program.
				return nil, err
			}
		case frameExit:
			var ex exit
			if err := json.Unmarshal(payload, &ex); err != nil {
				return nil, &ConnError{Err: err}
			}
			return &ex, nil
		case frameReply:
			return nil, replyError(payload)
		}
	}
}
