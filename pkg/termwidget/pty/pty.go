// Package pty starts a program on a pseudo-terminal: a ConPTY pseudo console on Windows,
// and a Unix pty on Linux.
package pty

import (
	"io"
	"os/exec"
)

// Pty is a running program's terminal. Reading it returns what the program writes, and
// writing it types into the program. Closing it hangs the program up.
type Pty interface {
	io.ReadWriteCloser
	// Resize tells the program the terminal's new size in cells.
	Resize(cols, rows int) error
	// Name is the path of the terminal device, such as /dev/pts/3, or "" where there's
	// none, as on Windows.
	Name() string
}

// Start runs cmd on a new terminal of cols by rows cells, as Cmd.Start would, setting
// cmd.Process. Its Stdin, Stdout and Stderr must be unset. The caller waits for it with
// cmd.Wait.
func Start(cmd *exec.Cmd, cols, rows int) (Pty, error) {
	return start(cmd, cols, rows)
}
