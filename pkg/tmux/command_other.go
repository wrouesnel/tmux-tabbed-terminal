//go:build !windows

package tmux

import (
	"os/exec"
	"syscall"
)

// quoteArgs does nothing: only Windows passes a command line rather than arguments.
func quoteArgs(*exec.Cmd) {}

// withoutTerminal runs cmd in a session of its own, without the controlling terminal the
// application may have been started from, so ssh can't ask the user there.
func withoutTerminal(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}
