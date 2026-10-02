package tmux_tabbed_terminal_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wrouesnel/ctxstdio"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/entrypoints/tmux_tabbed_terminal"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

// guiArgsEnv makes the test binary run the application with these arguments instead of
// the tests. TestMain does it on the main thread, which GTK needs.
const guiArgsEnv = "TTT_TEST_GUI_ARGS"

func TestMain(m *testing.M) {
	if args := os.Getenv(guiArgsEnv); args != "" {
		ctx := ctxstdio.Set(context.Background(), os.Stdout, os.Stderr, os.Stdin)
		os.Exit(tmux_tabbed_terminal.Entrypoint(ctx, strings.Split(args, "\n")))
	}
	os.Exit(m.Run())
}

// waitFor polls cond until it's true or the timeout passes.
func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(100 * time.Millisecond) //nolint:mnd
	}
	return cond()
}

// TestGUIAttachesToSessions starts the application on a virtual X server against a
// private tmux server, and checks that it attaches a client to the session and creates
// one when there are none.
func TestGUIAttachesToSessions(t *testing.T) {
	for _, tool := range []string{"xvfb-run", "tmux"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	ctx := context.Background()
	client := tmux.NewClient()
	client.SocketName = fmt.Sprintf("ttt-gui-test-%d", os.Getpid())
	t.Cleanup(func() {
		argv := client.Argv("kill-server")
		_ = exec.Command(argv[0], argv[1:]...).Run() //nolint:gosec // test helper
	})

	configPath := writeConfig(t, fmt.Sprintf("tmux:\n  socket-name: %s\nactivity:\n  poll-interval: 200ms\n",
		client.SocketName))
	args := strings.Join([]string{"--config-file", configPath, "run", "--separate"}, "\n")

	cmd := exec.Command("xvfb-run", "-a", "-s", "-screen 0 1024x768x24", os.Args[0]) //nolint:gosec // test binary
	cmd.Env = append(os.Environ(), guiArgsEnv+"="+args)
	// Own process group, so the X server and the application are stopped together.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	output := new(strings.Builder)
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		_ = cmd.Wait()
		if t.Failed() {
			t.Logf("application output:\n%s", output.String())
		}
	})

	// With no sessions, the window creates one and attaches to it.
	var snap *tmux.Snapshot
	attached := waitFor(30*time.Second, func() bool {
		var err error
		snap, err = client.Snapshot(ctx)
		return err == nil && len(snap.Sessions) == 1 && len(snap.Clients) == 1
	})
	if !attached {
		t.Fatalf("no session with an attached client: %+v", snap)
	}
	if snap.Clients[0].SessionID != snap.Sessions[0].ID {
		t.Fatalf("client is on %s, want %s", snap.Clients[0].SessionID, snap.Sessions[0].ID)
	}

	// Killing the session moves the window to another one.
	other, err := client.NewSession(ctx, "other", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.KillSession(ctx, snap.Sessions[0].ID); err != nil {
		t.Fatal(err)
	}
	moved := waitFor(10*time.Second, func() bool {
		snap, err = client.Snapshot(ctx)
		return err == nil && len(snap.Clients) == 1 && snap.Clients[0].SessionID == other
	})
	if !moved {
		t.Fatalf("client did not move to the remaining session %s: %+v", other, snap)
	}
}
