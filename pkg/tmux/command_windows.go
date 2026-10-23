//go:build windows

package tmux

import (
	"os/exec"
	"syscall"
)

// quoteArgs gives cmd a command line with every argument quoted.
func quoteArgs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: QuotedCommandLine(cmd.Args)}
}
