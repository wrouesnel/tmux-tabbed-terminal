package tmux

import (
	"context"
	"os/exec"

	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/agent"
)

// root returns the hop run from this machine, at the start of the route.
func (s *SSH) root() *SSH {
	for s.Via != nil {
		s = s.Via
	}
	return s
}

// sharesConnections reports whether the route's hop from this machine uses ssh connection
// sharing. Hops run from other hosts always do.
func (s *SSH) sharesConnections() bool {
	root := s.root()
	return s != root || (!root.NoConnectionSharing && root.Agents == nil)
}

// innerArgv returns the command which, run on the route's first host, runs remote on the
// destination: remote itself for the first host, and otherwise the ssh commands of the
// hops after it. chainArgv is the same with the first host's own ssh around it.
func (s *SSH) innerArgv(interactive, forward bool, remote []string) []string {
	if s.Via == nil {
		return remote
	}
	return s.Via.innerArgv(interactive, forward || s.ForwardAgent, s.hopArgv(interactive, forward, remote))
}

// agentClient returns the route's first hop and its agent's client, if it has a running
// agent.
func (s *SSH) agentClient() (*SSH, *agent.Client) {
	root := s.root()
	root.agentMu.Lock()
	defer root.agentMu.Unlock()
	if root.agent == nil {
		return root, nil
	}
	select {
	case <-root.agent.Done():
		root.agent = nil
		return root, nil
	default:
		return root, root.agent
	}
}

// agentFailed drops the agent's client if a call failed because the connection to it did,
// so the next command starts it again.
func (s *SSH) agentFailed(c *agent.Client, err error) {
	if code := status.Code(err); code != codes.Unavailable && code != codes.Internal {
		return
	}
	s.agentMu.Lock()
	defer s.agentMu.Unlock()
	if s.agent == c {
		s.agent = nil
		_ = c.Close()
	}
}

// ensureAgent starts the agent of a hop run from this machine, if it has agents and isn't
// running one. A destination with no agent for its platform is remembered, and its
// commands make connections of their own.
func (s *SSH) ensureAgent(ctx context.Context) error {
	if s.Agents == nil {
		return nil
	}
	s.agentMu.Lock()
	defer s.agentMu.Unlock()
	if s.agentUnsupported {
		return nil
	}
	if s.agent != nil {
		select {
		case <-s.agent.Done():
			s.agent = nil
		default:
			return nil
		}
	}
	if s.prompts() {
		// The user may take longer to answer than the caller would wait for a command.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), promptTimeout)
		defer cancel()
	}
	c, err := agent.Connect(ctx, s.launchAgent, s.Agents, s.AgentVersion)
	if errors.Is(err, agent.ErrUnsupported) {
		s.agentUnsupported = true
		return nil
	}
	if err != nil {
		return errors.Wrapf(err, "ssh %s", s.Destination)
	}
	s.agent = c
	return nil
}

// launchAgent makes the ssh command which runs remote on the destination for the agent:
// its connection carries every command, so it forwards the user's agent, for the ssh of
// hops beyond, as a master does.
func (s *SSH) launchAgent(ctx context.Context, remote []string) *exec.Cmd {
	argv := []string{s.binary(), "-T", "-o", "ConnectTimeout=10", "-o", "ForwardAgent=yes"}
	if !s.prompts() {
		argv = append(argv, "-o", "BatchMode=yes")
	}
	argv = append(argv, s.commonOptions("no")...)
	argv = append(argv, "--", s.Destination, ShellJoin(remote))
	cmd := Command(ctx, argv)
	if s.prompts() {
		cmd.Env = append(cmd.Environ(), s.AskpassEnv...)
		withoutTerminal(cmd)
	}
	return cmd
}
