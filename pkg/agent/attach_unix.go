//go:build unix

package agent

import (
	"encoding/binary"
	"errors"
	"os"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// drainTime is how long output must stop for, once an attached program has exited, to be
// taken as all it wrote.
const drainTime = 50 * time.Millisecond

// attach runs a program on a new pseudo-terminal, with input frames typed into it and
// resize frames resizing it, sending what it writes, and then how it exited.
func (c *call) attach(req *request) error {
	cmd, err := command(c.ctx, req.Argv)
	if err != nil {
		return err
	}
	cmd.Env = os.Environ()
	for k, v := range req.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	ptmx, tty, err := openPty()
	if err != nil {
		return err
	}
	defer ptmx.Close()
	resize(ptmx, req.Cols, req.Rows)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	err = cmd.Start()
	ttyName := tty.Name()
	_ = tty.Close()
	if err != nil {
		return startError(err)
	}
	if err := c.reply(&reply{TTY: ttyName}); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}

	// Typing and resizing, until the application ends the call, which hangs the program
	// up, through the context.
	go c.readInput(func(typ byte, payload []byte) {
		switch typ {
		case frameInput:
			_, _ = ptmx.Write(payload)
		case frameResize:
			if len(payload) == resizeSize {
				resize(ptmx, int(binary.BigEndian.Uint32(payload)), int(binary.BigEndian.Uint32(payload[4:])))
			}
		}
	})

	// What it writes, until it exits and its terminal hangs up.
	var sendErr error
	var exited atomic.Bool
	output := make(chan struct{})
	go func() {
		defer close(output)
		buf := make([]byte, streamChunk)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 && sendErr == nil {
				sendErr = c.w.Write(frameOutput, buf[:n])
			}
			if err != nil {
				return
			}
			if exited.Load() {
				_ = ptmx.SetReadDeadline(time.Now().Add(drainTime))
			}
		}
	}()
	waitErr := cmd.Wait()
	// The terminal ends once nothing has it open: the program, and anything it left
	// running in the background, such as a shell's jobs, may still. So what the program
	// wrote is read until the terminal ends or goes quiet, and then closing the master
	// ends the read.
	exited.Store(true)
	_ = ptmx.SetReadDeadline(time.Now().Add(drainTime))
	<-output
	_ = ptmx.Close()
	if sendErr != nil {
		return sendErr
	}
	ex, err := runExit(waitErr)
	if err != nil && !errors.Is(err, os.ErrClosed) {
		return err
	}
	return c.w.WriteJSON(frameExit, ex)
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
func resize(ptmx *os.File, cols, rows int) {
	if cols <= 0 || rows <= 0 || cols > 0xffff || rows > 0xffff {
		return
	}
	conn, err := ptmx.SyscallConn()
	if err != nil {
		return
	}
	ws := &unix.Winsize{Col: uint16(cols), Row: uint16(rows)}                                     //nolint:gosec // terminal sizes are small
	_ = conn.Control(func(fd uintptr) { _ = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, ws) }) //nolint:gosec // descriptors are small
}
