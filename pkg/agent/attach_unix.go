//go:build unix

package agent

import (
	"bytes"
	"errors"
	"os"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/agent/agentpb"
)

func (s *server) Attach(stream grpc.BidiStreamingServer[agentpb.AttachRequest, agentpb.AttachResponse]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	start := first.GetStart()
	if start == nil {
		return status.Error(codes.InvalidArgument, "the first request must start the program")
	}
	cmd, err := command(stream.Context(), start.GetCommand())
	if err != nil {
		return err
	}
	cmd.Env = os.Environ()
	for k, v := range start.GetEnv() {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	ptmx, tty, err := openPty()
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	defer ptmx.Close()
	resize(ptmx, start.GetSize())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	err = cmd.Start()
	ttyName := tty.Name()
	_ = tty.Close()
	if err != nil {
		return startError(err)
	}
	if err := stream.Send(&agentpb.AttachResponse{Event: &agentpb.AttachResponse_Started{
		Started: &agentpb.AttachStarted{Tty: ttyName},
	}}); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}

	// Typing and resizing, until the application stops sending. Its end of the call
	// finishing doesn't end the program: cancelling the call does, through the context.
	go func() {
		for {
			req, err := stream.Recv()
			if err != nil {
				return
			}
			switch ev := req.GetEvent().(type) {
			case *agentpb.AttachRequest_Input:
				_, _ = ptmx.Write(ev.Input)
			case *agentpb.AttachRequest_Resize:
				resize(ptmx, ev.Resize)
			}
		}
	}()

	// What it writes, until it exits and its terminal hangs up.
	var sendErr error
	output := make(chan struct{})
	go func() {
		defer close(output)
		buf := make([]byte, streamChunk)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 && sendErr == nil {
				sendErr = stream.Send(&agentpb.AttachResponse{Event: &agentpb.AttachResponse_Output{
					Output: bytes.Clone(buf[:n]),
				}})
			}
			if err != nil {
				return
			}
		}
	}()
	waitErr := cmd.Wait()
	// The terminal ends once nothing has it open: the program, and anything it left
	// running in the background, such as a shell's jobs, may still. Closing the master
	// ends the read.
	_ = ptmx.Close()
	<-output
	if sendErr != nil {
		return sendErr
	}
	exit, err := runExit(waitErr)
	if err != nil && !errors.Is(err, os.ErrClosed) {
		return err
	}
	return stream.Send(&agentpb.AttachResponse{Event: &agentpb.AttachResponse_Exit{Exit: exit}})
}

// openPty opens a new pseudo-terminal: its master, and the terminal the program runs on.
// pty.Open leaves the master in blocking mode, as File.Fd does, where closing it wouldn't
// end a read waiting on it, so the master is reopened, non-blocking, on a copy of its
// descriptor, which Go's poller then manages.
func openPty() (*os.File, *os.File, error) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		return nil, nil, err
	}
	fd, err := unix.Dup(int(ptmx.Fd())) //nolint:gosec // descriptors are small
	_ = ptmx.Close()
	if err == nil {
		unix.CloseOnExec(fd)
		err = unix.SetNonblock(fd, true)
	}
	if err != nil {
		_ = tty.Close()
		return nil, nil, err
	}
	return os.NewFile(uintptr(fd), "/dev/ptmx"), tty, nil
}

// resize sets a terminal's size, if one is given.
func resize(ptmx *os.File, size *agentpb.TerminalSize) {
	if size.GetCols() == 0 || size.GetRows() == 0 {
		return
	}
	conn, err := ptmx.SyscallConn()
	if err != nil {
		return
	}
	ws := &unix.Winsize{Col: uint16(size.GetCols()), Row: uint16(size.GetRows())}                 //nolint:gosec // terminal sizes are small
	_ = conn.Control(func(fd uintptr) { _ = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, ws) }) //nolint:gosec // descriptors are small
}
