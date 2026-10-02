package tmux_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	// Scrolling up enters copy mode; scrolling back down to the bottom leaves it.

	argv := c.Argv("send-keys", "-t", id, "seq 1 200", "Enter")
	if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil { //nolint:gosec // test
		t.Fatalf("send-keys: %v: %s", err, out)
	}
	// Wait for the shell to print enough to scroll.
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if snap, _ = c.Snapshot(ctx); snap.Session(id).ActiveWindow().Scroll.History > 20 {
			break
		}
	}
	pane := snap.Session(id).ActiveWindow().PaneID
	st, err := c.ScrollPane(ctx, pane, -10)
	if err != nil {
		t.Fatalf("ScrollPane up: %v", err)
	}
	if st.Position != 10 || st.History == 0 || st.Height == 0 {
		t.Fatalf("scroll state after scrolling up: %+v", st)
	}
	snap, _ = c.Snapshot(ctx)
	if !snap.Session(id).ActiveWindow().InMode {
		t.Fatal("scrolling up didn't enter copy mode")
	}
	if st, err = c.ScrollPane(ctx, pane, 20); err != nil || st.Position != 0 {
		t.Fatalf("ScrollPane down: %+v, %v", st, err)
	}
	snap, _ = c.Snapshot(ctx)
	if snap.Session(id).ActiveWindow().InMode {
		t.Fatal("scrolling to the bottom didn't leave copy mode")
	}

	history, err := c.CaptureHistory(ctx, id)
	switch {
	case errors.Is(err, tmux.ErrCaptureCrashes):
		t.Logf("not capturing on this tmux: %v", err)
	case err != nil:
		t.Fatalf("CaptureHistory: %v", err)
	case !strings.Contains(history, "\n1\n2\n3\n") || !strings.Contains(history, "\n200\n"):
		t.Errorf("history doesn't hold the output: %q", history)
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
	out := strings.ReplaceAll("S\t$1\tmain\t100\t1\n"+
		"S\t$2\tother\t200\t0\n"+
		"W\t$1\t@2\t1\t1\t150\tvim\teditor\t/src\t%5\t1\t0\t1\t500\t40\t12\ttitle\twith sep\n"+
		"W\t$1\t@1\t0\t0\t300\tbash\tshell\t/home\t%4\t0\t0\t0\t0\t40\t\t\n"+
		"W\t$9\t@9\t0\t1\t1\tbash\torphan\t/\t%9\t0\t0\t0\t0\t40\t\t\n"+
		"C\t/dev/pts/4\t$1\n"+
		"garbage\n", "\t", "^|^")
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
	if got := main.ActiveWindow().Scroll; got != (tmux.ScrollState{History: 500, Height: 40, Position: 12}) {
		t.Errorf("scroll: got %+v", got)
	}
	if aw := main.ActiveWindow(); aw.PaneID != "%5" || !aw.AlternateScreen || aw.MouseReporting || !aw.InMode {
		t.Errorf("pane fields: got %+v", aw)
	}
	if got := main.ActiveWindow().Title; got != "title^|^with sep" {
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

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"":              "''",
		"list-sessions": "list-sessions",
		"$1":            "'$1'",
		";":             "';'",
		"#{session_id}": "'#{session_id}'",
		"it's":          `'it'\''s'`,
		"a\tb":          "'a\tb'",
	}
	for in, want := range cases {
		if got := tmux.ShellQuote(in); got != want {
			t.Errorf("%q: got %s, want %s", in, got, want)
		}
	}
}

// TestRemoteThroughSSH runs a session lifecycle through the ssh code path, with a fake ssh
// that runs the remote command through a local shell. That checks the remote command
// survives the remote shell's parsing.
func TestRemoteThroughSSH(t *testing.T) {
	ctx := context.Background()
	local := newTestClient(t)
	fake, err := filepath.Abs("testdata/fake-ssh")
	if err != nil {
		t.Fatal(err)
	}
	remote := &tmux.Client{
		Binary:     "tmux",
		SocketName: local.SocketName,
		SSH:        &tmux.SSH{Destination: "test-host", Binary: fake, ControlDir: t.TempDir()},
	}
	if !remote.Remote() {
		t.Fatal("client with SSH is not remote")
	}

	snap, err := remote.Snapshot(ctx)
	if err != nil || len(snap.Sessions) != 0 {
		t.Fatalf("Snapshot before start: %+v, %v", snap, err)
	}
	id, err := remote.NewSession(ctx, "it's remote", "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	snap, err = remote.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if s := snap.Session(id); s == nil || s.Name != "it's remote" || len(s.Windows) != 1 {
		t.Fatalf("session through ssh: %+v", snap)
	}
	if err := remote.KillSession(ctx, id); err != nil {
		t.Fatalf("KillSession: %v", err)
	}

	argv := remote.AttachArgv("$7")
	if argv[0] != fake || argv[1] != "-t" || argv[len(argv)-1] != "tmux -u -L "+local.SocketName+" attach-session -t '$7'" {
		t.Fatalf("attach argv: %q", argv)
	}
}

// TestRemoteThroughHops runs tmux through three nested ssh hops, each a fake ssh which
// runs its command through a shell, as each real hop's sshd would. The quoting has to
// survive a shell per hop.
func TestRemoteThroughHops(t *testing.T) {
	ctx := context.Background()
	local := newTestClient(t)
	fake, err := filepath.Abs("testdata/fake-ssh")
	if err != nil {
		t.Fatal(err)
	}
	first := &tmux.SSH{Destination: "bastion", Binary: fake, ControlDir: t.TempDir()}
	second := &tmux.SSH{Destination: "inner", Binary: fake, Via: first}
	third := &tmux.SSH{Destination: "db", Binary: fake, Via: second}
	if got := third.Name(); got != "db via inner via bastion" {
		t.Fatalf("Name: got %q", got)
	}
	remote := &tmux.Client{Binary: "tmux", SocketName: local.SocketName, SSH: third}

	id, err := remote.NewSession(ctx, "it's 3 hops; deep", "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	snap, err := remote.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if s := snap.Session(id); s == nil || s.Name != "it's 3 hops; deep" {
		t.Fatalf("session through hops: %+v", snap)
	}
	out, err := third.Output(ctx, "echo", "a b", "$HOME")
	if err != nil || out != "a b $HOME\n" {
		t.Fatalf("Output: %q, %v", out, err)
	}

	argv := remote.AttachArgv(id)
	if argv[0] != fake || argv[1] != "-t" || !strings.Contains(argv[len(argv)-1], "-t") {
		t.Fatalf("attach through hops should be interactive at every hop: %q", argv)
	}
}
