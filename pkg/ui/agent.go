package ui

import (
	"os"
	"path/filepath"
	"runtime"

	"go.uber.org/zap"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/agent"
)

// agentDirEnv names a directory of agents to use instead of the installed ones, as in
// development, where `go run mage.go agents` builds them into bin/agent.
const agentDirEnv = "TTT_AGENT_DIR"

// noConnectionSharing reports whether this machine's ssh can't share connections: Windows'
// OpenSSH doesn't support ControlMaster, and MSYS2's can't pass a session through one.
func noConnectionSharing() bool {
	return runtime.GOOS == "windows"
}

// useAgent reports whether remote hosts are reached through the agent.
func (a *App) useAgent() bool {
	switch a.cfg.SSH.Agent {
	case agentAlways:
		return true
	case agentNever:
		return false
	default:
		return noConnectionSharing()
	}
}

// agents returns the agents to install on remote hosts, or nil if there are none: from
// TTT_AGENT_DIR, or installed with the application: in lib/tmux-tabbed-terminal/agent, as
// packages install them, or in agent beside it, as in the release archives.
func (a *App) agents() *agent.Binaries {
	a.agentsOnce.Do(func() {
		dirs := []string{os.Getenv(agentDirEnv)}
		if exe, err := os.Executable(); err == nil {
			if resolved, err := filepath.EvalSymlinks(exe); err == nil {
				exe = resolved
			}
			dirs = append(dirs, filepath.Join(filepath.Dir(exe), "..", "lib", "tmux-tabbed-terminal", "agent"),
				filepath.Join(filepath.Dir(exe), "agent"))
		}
		for _, dir := range dirs {
			if dir == "" {
				continue
			}
			if found, _ := filepath.Glob(filepath.Join(dir, "ttt-agent-*.gz")); len(found) > 0 {
				a.agentBinaries = &agent.Binaries{Dir: dir}
				a.log.Debug("Agents for remote hosts", zap.String("dir", dir))
				return
			}
		}
		a.log.Warn("No agents for remote hosts were found, so each command to one connects on its own",
			zap.Strings("looked in", dirs))
	})
	return a.agentBinaries
}
