package tmux

import (
	"context"
	"io"
	"time"

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

// Controllable reports whether args would be sent through a control connection.
func Controllable(args []string) bool { return controllable(splitCommands(nil, args)) }

// ParseSnapshotAfter exposes parseSnapshot, which shares unchanged windows with prev.
func ParseSnapshotAfter(out string, prev *Snapshot) *Snapshot {
	return parseSnapshot([]byte(out), prev)
}

// ParseSnapshotBytes exposes parseSnapshot.
var ParseSnapshotBytes = parseSnapshot

// QuoteControlArg exposes quoteControlArg to the tests.
var QuoteControlArg = quoteControlArg

// ControlRun runs commands through a control connection, as Client.run does with its
// Timeout.
type ControlRun func(ctx context.Context, timeout time.Duration, args []string) (string, error)

// StartControlOn starts a control connection reading tmux's side from stdout, for testing
// how replies are read.
func StartControlOn(ctx context.Context, stdin io.WriteCloser, stdout io.Reader) (ControlRun, error) {
	// Stopping the fake ends its output, as when a process is killed.
	stop := func() {
		if c, ok := stdout.(io.Closer); ok {
			_ = c.Close()
		}
	}
	cc, err := startControl(ctx, stdin, stdout, stop)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, timeout time.Duration, args []string) (string, error) {
		return cc.run(ctx, splitCommands(nil, args), timeout, nil)
	}, nil
}
