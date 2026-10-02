package tmux_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

// newTestClient returns a client for a private tmux server which is killed when the test
// ends. The test is skipped if tmux isn't installed.
func newTestClient(t *testing.T) *tmux.Client {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	c := tmux.NewClient()
	c.SocketName = fmt.Sprintf("ttt-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	t.Cleanup(func() {
		argv := c.Argv("kill-server")
		_ = exec.Command(argv[0], argv[1:]...).Run() //nolint:gosec // test helper
	})
	return c
}

func TestSnapshotWithoutServer(t *testing.T) {
	c := newTestClient(t)
	snap, err := c.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Sessions) != 0 {
		t.Fatalf("got %d sessions, want 0", len(snap.Sessions))
	}
}

func TestSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newTestClient(t)

	id, err := c.NewSession(ctx, "alpha", t.TempDir())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if id == "" || id[0] != '$' {
		t.Fatalf("session ID %q doesn't look like a tmux session ID", id)
	}
	if _, err := c.NewSession(ctx, "", ""); err != nil {
		t.Fatalf("NewSession without name: %v", err)
	}

	snap, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(snap.Sessions))
	}
	alpha := snap.Session(id)
	if alpha == nil {
		t.Fatalf("session %s missing from snapshot", id)
	}
	if alpha.Name != "alpha" {
		t.Errorf("name: got %q, want alpha", alpha.Name)
	}
	if len(alpha.Windows) != 1 || alpha.ActiveWindow() == nil {
		t.Errorf("windows: got %+v, want one active window", alpha.Windows)
	}
	if alpha.Activity().IsZero() {
		t.Error("session has no activity time")
	}

	if err := c.RenameSession(ctx, id, "beta gamma"); err != nil {
		t.Fatalf("RenameSession: %v", err)
	}
	snap, _ = c.Snapshot(ctx)
	if got := snap.Session(id).Name; got != "beta gamma" {
		t.Errorf("renamed: got %q, want %q", got, "beta gamma")
	}

	if ok, err := c.HasSession(ctx, id); err != nil || !ok {
		t.Fatalf("HasSession before kill: %v, %v", ok, err)
	}
	if err := c.KillSession(ctx, id); err != nil {
		t.Fatalf("KillSession: %v", err)
	}
	if ok, err := c.HasSession(ctx, id); err != nil || ok {
		t.Fatalf("HasSession after kill: %v, %v", ok, err)
	}
}

func TestParseSnapshot(t *testing.T) {
	out := "S\t$1\tmain\t100\t1\n" +
		"S\t$2\tother\t200\t0\n" +
		"W\t$1\t@2\t1\t1\t150\tvim\teditor\t/src\ttitle\twith tab\n" +
		"W\t$1\t@1\t0\t0\t300\tbash\tshell\t/home\t\n" +
		"W\t$9\t@9\t0\t1\t1\tbash\torphan\t/\t\n" +
		"C\t/dev/pts/4\t$1\n" +
		"garbage\n"
	snap := tmux.ParseSnapshot(out)
	if len(snap.Sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(snap.Sessions))
	}
	main := snap.Session("$1")
	if len(main.Windows) != 2 || main.Windows[0].ID != "@1" {
		t.Fatalf("windows not sorted by index: %+v", main.Windows)
	}
	if got := main.ActiveWindow().Command; got != "vim" {
		t.Errorf("active window command: got %q, want vim", got)
	}
	if got := main.ActiveWindow().Path; got != "/src" {
		t.Errorf("path: got %q, want /src", got)
	}
	if got := main.ActiveWindow().Title; got != "title\twith tab" {
		t.Errorf("title: got %q", got)
	}
	if got := main.Activity().Unix(); got != 300 {
		t.Errorf("activity: got %d, want 300", got)
	}
	if got := snap.ClientSession("/dev/pts/4"); got != "$1" {
		t.Errorf("client session: got %q, want $1", got)
	}
	if snap.Session("$2").Activity() != (time.Time{}) {
		t.Error("session without windows should have no activity")
	}
}

func TestEnviron(t *testing.T) {
	got := tmux.Environ([]string{"HOME=/h", "TMUX=/tmp/x,1,0", "TMUX_PANE=%1", "TMUXP=keep"})
	want := []string{"HOME=/h", "TMUXP=keep"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
