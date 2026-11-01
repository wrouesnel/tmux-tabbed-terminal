package agent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/agent/agentpb"
)

// maxMessage is the largest message either side sends: a Run's whole output, such as a
// file being previewed, is one message.
const maxMessage = 64 << 20

// streamChunk is the most of a program's output one response carries.
const streamChunk = 32 << 10

// maxStderr is the most of a program's standard error kept, from its start.
const maxStderr = 64 << 10

// Serve serves the agent over r and w, such as standard input and output, until r ends or
// ctx is done. version is the application version the agent was built with.
func Serve(ctx context.Context, r io.Reader, w io.Writer, version string) error {
	conn := newPipeConn(r, w)
	l := newOneConnListener(conn)
	s := grpc.NewServer(grpc.MaxRecvMsgSize(maxMessage), grpc.MaxSendMsgSize(maxMessage))
	agentpb.RegisterAgentServiceServer(s, &server{version: version})
	errs := make(chan error, 1)
	go func() { errs <- s.Serve(l) }()
	select {
	case <-conn.closed:
		// The application went away: its end of the pipe closed.
	case <-ctx.Done():
	case err := <-errs:
		return err
	}
	s.Stop()
	_ = l.Close()
	return nil
}

type server struct {
	agentpb.UnimplementedAgentServiceServer

	version string
}

func (s *server) Hello(context.Context, *agentpb.HelloRequest) (*agentpb.HelloResponse, error) {
	home, _ := os.UserHomeDir()
	return &agentpb.HelloResponse{Version: s.version, Os: runtime.GOOS, Arch: runtime.GOARCH, Home: home}, nil
}

// command makes the program a request names.
func command(ctx context.Context, c *agentpb.Command) (*exec.Cmd, error) {
	argv := c.GetArgv()
	if len(argv) == 0 {
		return nil, status.Error(codes.InvalidArgument, "no program to run")
	}
	return exec.CommandContext(ctx, argv[0], argv[1:]...), nil //nolint:gosec // running programs is the agent's job
}

func (s *server) Run(ctx context.Context, req *agentpb.RunRequest) (*agentpb.RunResponse, error) {
	cmd, err := command(ctx, req.GetCommand())
	if err != nil {
		return nil, err
	}
	var stdout bytes.Buffer
	stderr := &limitedBuffer{max: maxStderr}
	cmd.Stdin = bytes.NewReader(req.GetStdin())
	cmd.Stdout, cmd.Stderr = &stdout, stderr
	exit, err := runExit(cmd.Run())
	if err != nil {
		return nil, err
	}
	return &agentpb.RunResponse{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), Exit: exit}, nil
}

func (s *server) Stream(req *agentpb.StreamRequest, stream grpc.ServerStreamingServer[agentpb.StreamResponse]) error {
	cmd, err := command(stream.Context(), req.GetCommand())
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	stderr := &limitedBuffer{max: maxStderr}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return startError(err)
	}
	buf := make([]byte, streamChunk)
	for {
		n, rerr := stdout.Read(buf)
		if n > 0 {
			chunk := bytes.Clone(buf[:n])
			if err := stream.Send(&agentpb.StreamResponse{Event: &agentpb.StreamResponse_Stdout{Stdout: chunk}}); err != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				return err
			}
		}
		if rerr != nil {
			break
		}
	}
	exit, err := runExit(cmd.Wait())
	if err != nil {
		return err
	}
	return stream.Send(&agentpb.StreamResponse{Event: &agentpb.StreamResponse_Exit{
		Exit: &agentpb.StreamExit{Exit: exit, Stderr: stderr.Bytes()},
	}})
}

// runExit turns what running a program returned into how it exited, or the error of a
// program that couldn't run.
func runExit(err error) (*agentpb.Exit, error) {
	if err == nil {
		return &agentpb.Exit{}, nil
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
		return status.Error(codes.NotFound, err.Error())
	}
	return status.Error(codes.FailedPrecondition, err.Error())
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
