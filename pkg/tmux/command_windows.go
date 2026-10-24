//go:build windows

package tmux

import (
	"os/exec"
	"syscall"
)

// quoteArgs gives cmd a command line with every argument quoted. It also hides the
// console window a console program started by a GUI program opens. The program needs a
// console: a Cygwin program without one, as CREATE_NO_WINDOW would leave it, opens a
// visible one of its own.
func quoteArgs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: QuotedCommandLine(cmd.Args), HideWindow: true}
}
