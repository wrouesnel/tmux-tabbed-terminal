package tmux_test

import (
	"context"
	"testing"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/agent/agenttest"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

// pollRemote polls a remote client as the application does, and checks that once a
// control client is running, polls start no more ssh.
func pollRemote(t *testing.T, remote *tmux.Client, runs func() int) {
	t.Helper()
	ctx := context.Background()
	id, err := remote.NewSession(ctx, "it's remote", "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	for range 2 {
		if _, err := remote.Snapshot(ctx); err != nil {
			t.Fatalf("Snapshot: %v", err)
		}
	}
	if remote.ControlName() == "" {
		t.Fatal("no control client")
	}
	before := runs()
	for range 10 {
		snap, err := remote.Snapshot(ctx)
		if err != nil {
			t.Fatalf("Snapshot: %v", err)
		}
		if s := snap.Session(id); s == nil || s.Name != "it's remote" || s.Attached != 0 || len(snap.Clients) != 0 {
			t.Fatalf("snapshot through the control client: %+v", snap)
		}
	}
	if n := runs() - before; n != 0 {
		t.Errorf("polling ran ssh %d times", n)
	}
}

func TestControlThroughSSH(t *testing.T) {
	local := controlTestClient(t, "local")
	fake, runs := countingSSH(t)
	ssh := &tmux.SSH{Destination: "test-host", Binary: fake, NoConnectionSharing: true}
	remote := &tmux.Client{Binary: "tmux", SocketName: local.SocketName, SSH: ssh}
	t.Cleanup(remote.Close)
	pollRemote(t, remote, runs)
}

func TestControlThroughAgent(t *testing.T) {
	const version = "v9.9.9-agent-test"
	binaries := agenttest.Build(t, version)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	local := controlTestClient(t, "local")
	fake, runs := countingSSH(t)
	ssh := &tmux.SSH{
		Destination: "test-host", Binary: fake, NoConnectionSharing: true,
		Agents: binaries, AgentVersion: version,
	}
	remote := &tmux.Client{Binary: "tmux", SocketName: local.SocketName, SSH: ssh}
	t.Cleanup(remote.Close)
	pollRemote(t, remote, runs)
}
