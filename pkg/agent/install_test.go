package agent_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pkg/errors"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/agent"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/agent/agenttest"
)

const testVersion = "v9.9.9-test"

// localLauncher runs the remote command here, with a home of its own, through a shell
// which prints junk first, as some shell startup files do. It counts the commands run.
func localLauncher(t *testing.T, launches *atomic.Int32) agent.Launcher {
	home := t.TempDir()
	return func(ctx context.Context, remote []string) *exec.Cmd {
		launches.Add(1)
		cmd := exec.CommandContext(ctx, "sh", append([]string{"-c", `echo "Welcome!"; exec "$@"`, "sh"}, remote...)...)
		cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CACHE_HOME=")
		return cmd
	}
}

func TestConnect(t *testing.T) {
	binaries := agenttest.Build(t, testVersion)
	var launches atomic.Int32
	launch := localLauncher(t, &launches)
	ctx := context.Background()

	c, err := agent.Connect(ctx, launch, binaries, testVersion)
	if err != nil {
		t.Fatal(err)
	}
	if n := launches.Load(); n != 1 {
		t.Errorf("installing and connecting took %d commands", n)
	}
	res, err := c.Run(ctx, []string{"echo", "hi"}, nil)
	if err != nil || string(res.Stdout) != "hi\n" {
		t.Errorf("run: %+v, %v", res, err)
	}
	_ = c.Close()

	// Installed: one command.
	launches.Store(0)
	c, err = agent.Connect(ctx, launch, binaries, testVersion)
	if err != nil {
		t.Fatal(err)
	}
	if n := launches.Load(); n != 1 {
		t.Errorf("connecting again took %d commands", n)
	}
	_ = c.Close()

	// The agent must be the application's version.
	if _, err := agent.Connect(ctx, launch, binaries, "v0.0.1"); err == nil {
		t.Error("connected to an agent of another version")
	}
}

func TestConnectUnsupported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the agent runs on Unix hosts")
	}
	// Agents for another platform only.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ttt-agent-plan9-mips.gz"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var launches atomic.Int32
	_, err := agent.Connect(context.Background(), localLauncher(t, &launches), &agent.Binaries{Dir: dir}, testVersion)
	if !errors.Is(err, agent.ErrUnsupported) {
		t.Errorf("err %v", err)
	}
}

// An agent replaced after the application started, as by an upgrade, isn't installed under
// the old one's ID.
func TestConnectChangedAgents(t *testing.T) {
	binaries := agenttest.Build(t, testVersion)
	if _, err := binaries.ID(testVersion); err != nil {
		t.Fatal(err)
	}
	gzs, _ := filepath.Glob(filepath.Join(binaries.Dir, "*.gz"))
	for _, gz := range gzs {
		if err := os.WriteFile(gz, []byte("a newer agent"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var launches atomic.Int32
	_, err := agent.Connect(context.Background(), localLauncher(t, &launches), binaries, testVersion)
	if err == nil || !strings.Contains(err.Error(), "restart") {
		t.Errorf("err %v", err)
	}
}
