package tmux_test

import (
	"context"
	"io"
	"runtime"
	"testing"
	"time"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/agent"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

// TestLostAgentIsClosed closes the client of an agent whose pipes closed when it's
// dropped, so a host that keeps losing its agent doesn't keep each lost client's gRPC
// goroutines (issue #15).
func TestLostAgentIsClosed(t *testing.T) {
	s := &tmux.SSH{Destination: "example"}
	const lost = 20
	settle := func() int {
		for range 20 {
			runtime.Gosched()
			time.Sleep(10 * time.Millisecond)
		}
		return runtime.NumGoroutine()
	}
	before := settle()
	for range lost {
		// The agent's end: what it writes, and what it reads, which is thrown away.
		fromAgent, agentOut := io.Pipe()
		agentIn, toAgent := io.Pipe()
		go func() { _, _ = io.Copy(io.Discard, agentIn) }()
		c, err := agent.NewClient(fromAgent, toAgent, fromAgent, toAgent)
		if err != nil {
			t.Fatal(err)
		}
		s.SetAgent(c)
		// A call connects; the agent then goes away, and gRPC closes the pipes.
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		go func() { _, _ = c.Hello(ctx, "test") }()
		time.Sleep(10 * time.Millisecond)
		_ = agentOut.Close()
		<-c.Done()
		cancel()
		if s.AgentClient() != nil {
			t.Fatal("a lost agent's client was kept")
		}
	}
	if after := settle(); after-before >= lost {
		t.Errorf("%d goroutines before losing %d agents, %d after", before, lost, after)
	}
}
