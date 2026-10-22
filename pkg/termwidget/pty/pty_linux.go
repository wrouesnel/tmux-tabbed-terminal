package pty

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

type unixPty struct {
	// file isn't embedded, so that its WriteTo can't bypass Read.
	file *os.File
	name string
}

func (p *unixPty) Write(b []byte) (int, error) { return p.file.Write(b) }
func (p *unixPty) Close() error                { return p.file.Close() }

func (p *unixPty) Name() string { return p.name }

func (p *unixPty) Resize(cols, rows int) error {
	//nolint:gosec // terminal sizes fit
	ws := &unix.Winsize{Col: uint16(cols), Row: uint16(rows)}
	return unix.IoctlSetWinsize(int(p.file.Fd()), unix.TIOCSWINSZ, ws)
}

// Read returns io.EOF, rather than EIO, once the program has exited.
func (p *unixPty) Read(b []byte) (int, error) {
	n, err := p.file.Read(b)
	if errors.Is(err, syscall.EIO) {
		return n, io.EOF
	}
	return n, err
}

func start(cmd *exec.Cmd, cols, rows int) (Pty, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("opening /dev/ptmx: %w", err)
	}
	fail := func(err error) (Pty, error) {
		master.Close()
		return nil, err
	}
	fd := int(master.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		return fail(fmt.Errorf("unlocking the pty: %w", err))
	}
	n, err := unix.IoctlGetUint32(fd, unix.TIOCGPTN)
	if err != nil {
		return fail(fmt.Errorf("TIOCGPTN: %w", err))
	}
	p := &unixPty{file: master, name: fmt.Sprintf("/dev/pts/%d", n)}
	if err := p.Resize(cols, rows); err != nil {
		return fail(fmt.Errorf("setting the pty size: %w", err))
	}
	slave, err := os.OpenFile(p.name, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return fail(fmt.Errorf("opening %s: %w", p.name, err))
	}
	defer slave.Close()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		return fail(fmt.Errorf("starting %s: %w", cmd.Path, err))
	}
	return p, nil
}
