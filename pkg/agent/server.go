package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"

	"github.com/wrouesnel/stdiomux"
	"github.com/wrouesnel/stdiomux/yamux"
)

// streamChunk is the most of a program's output, or input, one frame carries.
const streamChunk = 32 << 10

// maxStderr is the most of a program's standard error kept, from its start.
const maxStderr = 64 << 10

// linkConfig is the multiplexer's configuration, at either end.
func linkConfig(keepAlive bool) *yamux.Config {
	c := stdiomux.DefaultConfig()
	// The application checks the agent is alive. The agent ends when its standard
	// input does, and doesn't give up on an application that's slow to answer, such as
	// on a laptop that's asleep.
	c.EnableKeepAlive = keepAlive
	return c
}

// Serve serves the agent over r and w, such as standard input and output, until r ends or
// ctx is done. version is the application version the agent was built with. It first
// writes a line saying it's ready, and its version, for Connect to find.
func Serve(ctx context.Context, r io.Reader, w io.Writer, version string) error {
	if _, err := fmt.Fprintf(w, "%s %s\n", markerStart, version); err != nil {
		return err
	}
	session, err := stdiomux.Server(stdiomux.NewConn(r, w), linkConfig(false))
	if err != nil {
		return err
	}
	s := &server{version: version}
	go func() {
		for {
			stream, err := session.AcceptStream()
			if err != nil {
				return
			}
			go s.serve(stream)
		}
	}()
	select {
	case <-session.CloseChan():
		// The application went away: its end of the pipe closed.
	case <-ctx.Done():
		// As for a signal, when the agent is ending. The application's end may still be
		// open: closing the session would wait for a read from it which never returns.
	}
	return nil
}

type server struct {
	version string
}

// call is a call being served.
type call struct {
	stream *yamux.Stream
	r      *stdiomux.FrameReader
	w      *stdiomux.FrameWriter
	// ctx is cancelled once the application stops sending, or resets the stream.
	ctx     context.Context
	cancel  context.CancelFunc
	replied bool
}

func (s *server) serve(stream *yamux.Stream) {
	c := &call{stream: stream, r: stdiomux.NewFrameReader(stream), w: stdiomux.NewFrameWriter(stream)}
	var req request
	if err := c.r.NextJSON(frameRequest, &req); err != nil {
		stdiomux.Reset(stream)
		return
	}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	defer c.cancel()
	var err error
	switch req.Call {
	case callHello:
		home, _ := os.UserHomeDir()
		err = c.reply(&reply{Version: s.version, OS: runtime.GOOS, Arch: runtime.GOARCH, Home: home})
		c.cancel()
	case callRun:
		err = c.run(&req)
	case callAttach:
		err = c.attach(&req)
	case callWatch:
		err = c.watch(&req)
	default:
		err = &Error{Code: CodeUnimplemented, Message: "the agent can't " + req.Call}
	}
	if err != nil {
		c.fail(err)
	}
	_ = stream.Close()
}

// reply says the call has started, or failed.
func (c *call) reply(r *reply) error {
	c.replied = true
	return c.w.WriteJSON(frameReply, r)
}

// fail says the call failed: in its reply, or after it, in another.
func (c *call) fail(err error) {
	var e *Error
	if !errors.As(err, &e) {
		e = &Error{Code: CodeInternal, Message: err.Error()}
	}
	_ = c.reply(&reply{Error: e.Message, Code: e.Code})
}

// readInput reads what the application sends, handing it to input, until it ends the
// call by closing or resetting the stream, which cancels it. A frame for none of the
// handlers is ignored.
func (c *call) readInput(input func(typ byte, payload []byte)) {
	defer c.cancel()
	for {
		typ, payload, err := c.r.Next()
		if err != nil {
			return
		}
		input(typ, payload)
	}
}

// command makes the program a request names.
func command(ctx context.Context, argv []string) (*exec.Cmd, error) {
	if len(argv) == 0 {
		return nil, &Error{Code: CodeInvalidArgument, Message: "no program to run"}
	}
	return exec.CommandContext(ctx, argv[0], argv[1:]...), nil //nolint:gosec // running programs is the agent's job
}

// run runs a program, with input frames as its standard input, sending its standard
// output as it's written, and then how it exited.
func (c *call) run(req *request) error {
	cmd, err := command(c.ctx, req.Argv)
	if err != nil {
		return err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr := &limitedBuffer{max: maxStderr}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return startError(err)
	}
	if err := c.reply(&reply{}); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	go c.readInput(func(typ byte, payload []byte) {
		switch typ {
		case frameInput:
			// A program that stops reading its input, or has exited, drops the rest.
			if stdin != nil {
				if _, err := stdin.Write(payload); err != nil {
					_ = stdin.Close()
					stdin = nil
				}
			}
		case frameEnd:
			if stdin != nil {
				_ = stdin.Close()
				stdin = nil
			}
		}
	})
	buf := make([]byte, streamChunk)
	var sendErr error
	for {
		n, rerr := stdout.Read(buf)
		if n > 0 && sendErr == nil {
			if sendErr = c.w.Write(frameOutput, buf[:n]); sendErr != nil {
				_ = cmd.Process.Kill()
			}
		}
		if rerr != nil {
			break
		}
	}
	waitErr := cmd.Wait()
	if sendErr != nil {
		return sendErr
	}
	ex, err := runExit(waitErr)
	if err != nil {
		return err
	}
	ex.Stderr = stderr.Bytes()
	return c.w.WriteJSON(frameExit, ex)
}

// runExit turns what running a program returned into how it exited, or the error of a
// program that couldn't run.
func runExit(err error) (*exit, error) {
	if err == nil {
		return &exit{}, nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return nil, startError(err)
	}
	return exitOf(exitErr.ProcessState), nil
}

// startError is the error of a program that couldn't start.
func startError(err error) error {
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return &Error{Code: CodeNotFound, Message: err.Error()}
	}
	return &Error{Code: CodeFailedPrecondition, Message: err.Error()}
}

// limitedBuffer keeps the first max bytes written to it, and drops the rest.
type limitedBuffer struct {
	bytes.Buffer

	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}
