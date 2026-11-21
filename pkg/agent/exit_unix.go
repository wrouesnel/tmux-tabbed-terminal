//go:build unix

package agent

import (
	"os"
	"syscall"
)

// exitOf says how a program that ran ended.
func exitOf(state *os.ProcessState) *exit {
	if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return &exit{Code: -1, Signal: ws.Signal().String()}
	}
	return &exit{Code: state.ExitCode()}
}
