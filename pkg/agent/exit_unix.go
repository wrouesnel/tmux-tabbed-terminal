//go:build unix

package agent

import (
	"os"
	"syscall"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/agent/agentpb"
)

// exitOf says how a program that ran ended.
func exitOf(state *os.ProcessState) *agentpb.Exit {
	if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return &agentpb.Exit{Code: -1, Signal: ws.Signal().String()}
	}
	return &agentpb.Exit{Code: int32(state.ExitCode())} //nolint:gosec // exit statuses are small
}
