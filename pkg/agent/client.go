package agent

import (
	"context"
	"io"
	"net"
	"sync"

	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/agent/agentpb"
)

// errRedial is the dialer's error once its one connection has been used: an agent which
// has gone away can't be reached again over the same pipes.
var errRedial = errors.New("the agent's connection has closed")

// Client talks to an agent over a pair of pipes, such as the standard output and input of
// the ssh running it.
type Client struct {
	conn  *grpc.ClientConn
	agent agentpb.AgentServiceClient
	pipe  *pipeConn
}

// NewClient talks to an agent which reads r and writes w. Closing the client closes the
// closers, such as the pipes, which should end the agent.
func NewClient(r io.Reader, w io.Writer, closers ...io.Closer) (*Client, error) {
	pipe := newPipeConn(r, w, closers...)
	var once sync.Once
	dial := func(context.Context, string) (net.Conn, error) {
		var conn net.Conn
		once.Do(func() { conn = pipe })
		if conn == nil {
			return nil, errRedial
		}
		return conn, nil
	}
	conn, err := grpc.NewClient("passthrough:///agent",
		grpc.WithContextDialer(dial),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxMessage), grpc.MaxCallSendMsgSize(maxMessage)),
	)
	if err != nil {
		return nil, errors.Wrap(err, "talking to the agent")
	}
	return &Client{conn: conn, agent: agentpb.NewAgentServiceClient(conn), pipe: pipe}, nil
}

// Close stops talking to the agent.
func (c *Client) Close() error {
	err := c.conn.Close()
	if perr := c.pipe.Close(); err == nil {
		err = perr
	}
	return err
}

// Done is closed once the pipes to the agent have closed, from either end.
func (c *Client) Done() <-chan struct{} { return c.pipe.closed }

// Hello returns the agent's version and platform.
func (c *Client) Hello(ctx context.Context, version string) (*agentpb.HelloResponse, error) {
	return c.agent.Hello(ctx, &agentpb.HelloRequest{ClientVersion: version})
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

// Run runs a program on the agent's host, with stdin as its standard input.
func (c *Client) Run(ctx context.Context, argv []string, stdin []byte) (*Result, error) {
	resp, err := c.agent.Run(ctx, &agentpb.RunRequest{Command: &agentpb.Command{Argv: argv}, Stdin: stdin})
	if err != nil {
		return nil, err
	}
	return &Result{
		Stdout: resp.GetStdout(), Stderr: resp.GetStderr(),
		Code: int(resp.GetExit().GetCode()), Signal: resp.GetExit().GetSignal(),
	}, nil
}

// Stream runs a program on the agent's host, copying its standard output to w as it comes.
// It returns an *ExitError if the program fails.
func (c *Client) Stream(ctx context.Context, w io.Writer, argv []string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := c.agent.Stream(ctx, &agentpb.StreamRequest{Command: &agentpb.Command{Argv: argv}})
	if err != nil {
		return err
	}
	for {
		resp, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return errors.New("the agent ended the stream without saying how the program exited")
			}
			return err
		}
		switch ev := resp.GetEvent().(type) {
		case *agentpb.StreamResponse_Stdout:
			if _, err := w.Write(ev.Stdout); err != nil {
				// Cancelling the call kills the program.
				return err
			}
		case *agentpb.StreamResponse_Exit:
			exit := ev.Exit.GetExit()
			return exitError(int(exit.GetCode()), exit.GetSignal(), ev.Exit.GetStderr())
		}
	}
}
