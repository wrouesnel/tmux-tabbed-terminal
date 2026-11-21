package tmux

import (
	"context"
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

// ControlName exposes controlName to the tests.
func (c *Client) ControlName() string { return c.controlName() }

// Run exposes run to the tests.
func (c *Client) Run(ctx context.Context, args ...string) (string, error) { return c.run(ctx, args...) }

// QuoteControlArg exposes quoteControlArg to the tests.
var QuoteControlArg = quoteControlArg

// StartControlOn starts a control connection reading tmux's side from stdout, for testing
// how replies are read.
func StartControlOn(ctx context.Context, stdin io.WriteCloser, stdout io.Reader) (func([]string) (string, error), error) {
	cc, err := startControl(ctx, stdin, stdout, func() {})
	if err != nil {
		return nil, err
	}
	return func(args []string) (string, error) { return cc.run(ctx, splitCommands(args)) }, nil
}
