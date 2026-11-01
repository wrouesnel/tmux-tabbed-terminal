//go:build !unix

package agent

import (
	"os"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/agent/agentpb"
)

// exitOf says how a program that ran ended.
func exitOf(state *os.ProcessState) *agentpb.Exit {
	return &agentpb.Exit{Code: int32(state.ExitCode())} //nolint:gosec // exit statuses are small
}
