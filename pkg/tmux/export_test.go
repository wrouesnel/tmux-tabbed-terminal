package tmux

import (
	"io"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/agent"
)

// ProgramName exposes programName to the tests.
var ProgramName = programName

// MasterArgs exposes masterArgs to the tests.
var SplitPaths = splitPaths

func (s *SSH) MasterArgs() []string { return s.masterArgs() }

// CheckArgs exposes checkArgs to the tests.
func (s *SSH) CheckArgs() []string { return s.checkArgs() }

// NewSizeWriter exposes sizeWriter to the tests.
func NewSizeWriter(w io.Writer, size func(int64)) io.Writer { return &sizeWriter{w: w, size: size} }

// SetAgent gives the SSH an agent client, as if it had started its agent.
func (s *SSH) SetAgent(c *agent.Client) {
	s.agentMu.Lock()
	defer s.agentMu.Unlock()
	s.agent = c
}

// AgentClient exposes agentClient to the tests.
func (s *SSH) AgentClient() *agent.Client {
	_, c := s.agentClient()
	return c
}
