package tmux_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

// controlTestClient is a test client with sessions, whose version supports control mode,
// and which has polled once, so it has a session to attach a control client to.
func controlTestClient(t *testing.T, sessions ...string) *tmux.Client {
	t.Helper()
	c := newTestClient(t)
	ctx := context.Background()
	for _, name := range sessions {
		if _, err := c.NewSession(ctx, name, t.TempDir()); err != nil {
			t.Fatalf("NewSession: %v", err)
		}
	}
	version, err := c.ServerVersion(ctx)
	if err != nil {
		t.Fatalf("ServerVersion: %v", err)
	}
	var major, minor int
	if _, err := fmt.Sscanf(version, "%d.%d", &major, &minor); err != nil || major < 3 || (major == 3 && minor < 2) {
		t.Skipf("tmux %s has no control clients without output", version)
	}
	if _, err := c.Snapshot(ctx); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	return c
}

func TestControlSnapshot(t *testing.T) {
	c := controlTestClient(t, "one", "two")
	ctx := context.Background()
	snap, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if c.ControlName() == "" {
		t.Fatal("no control client after polling")
	}
	// The control client is this program's own: it isn't counted.
	if len(snap.Clients) != 0 {
		t.Errorf("clients: got %+v, want none", snap.Clients)
	}
	for _, s := range snap.Sessions {
		if s.Attached != 0 {
			t.Errorf("session %s: %d attached, want 0", s.Name, s.Attached)
		}
	}
	if len(snap.Sessions) != 2 {
		t.Errorf("got %d sessions, want 2", len(snap.Sessions))
	}
}

func TestControlSurvivesItsSession(t *testing.T) {
	c := controlTestClient(t, "one", "two")
	ctx := context.Background()
	if _, err := c.Snapshot(ctx); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	first := c.ControlName()
	if first == "" {
		t.Fatal("no control client")
	}
	// Kill the control client's session: it ends with it.
	out, err := c.Run(ctx, "list-clients", "-F", "#{client_name} #{session_id}")
	if err != nil {
		t.Fatalf("list-clients: %v", err)
	}
	var session string
	for _, line := range strings.Split(out, "\n") {
		if name, id, ok := strings.Cut(line, " "); ok && name == first {
			session = id
		}
	}
	if session == "" {
		t.Fatalf("control client %s not in %q", first, out)
	}
	if err := c.KillSession(ctx, session); err != nil {
		t.Fatalf("KillSession: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		snap, err := c.Snapshot(ctx)
		if err != nil {
			t.Fatalf("Snapshot after the kill: %v", err)
		}
		if len(snap.Sessions) == 1 && c.ControlName() != "" && c.ControlName() != first && len(snap.Clients) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after the kill: %d sessions, control client %q (was %q), clients %+v",
				len(snap.Sessions), c.ControlName(), first, snap.Clients)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestControlArgumentsAndErrors(t *testing.T) {
	c := controlTestClient(t, "one")
	ctx := context.Background()
	const format = `it's "#{session_name}" ; \ $HOME ~ {x}`
	out, err := c.Run(ctx, "list-sessions", "-F", format, ";", "list-sessions", "-F", "#{session_name}")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if c.ControlName() == "" {
		t.Fatal("not run through the control client")
	}
	if want := "it's \"one\" ; \\ $HOME ~ {x}\none\n"; out != want {
		t.Errorf("got %q, want %q", out, want)
	}
	if _, err := c.Run(ctx, "list-windows", "-t", "nosuch"); !errors.Is(err, tmux.ErrNoSession) {
		t.Errorf("missing session: got %v, want ErrNoSession", err)
	}
}

func TestQuoteControlArg(t *testing.T) {
	for in, want := range map[string]string{
		"list-sessions": "list-sessions",
		"":              "''",
		"#{a}":          "'#{a}'",
		"it's":          `'it'\''s'`,
	} {
		if got := tmux.QuoteControlArg(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

// TestControlReplies feeds a control connection what tmux might send: notifications
// between replies, the reply to the attach, and output that looks like the end of a
// reply but isn't this one's.
func TestControlReplies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	// The fake tmux: answers each command line in turn.
	answers := []string{
		"%begin 1 10 1\nclient-7\n%end 1 10 1\n",
		"%window-add @3\n%begin 2 11 1\nline one\n%end 2 9 1\n%error 1 11 1\nline two\n%end 2 11 1\n%sessions-changed\n",
		"%begin 3 12 1\ncan't find session: x\n%error 3 12 1\n",
	}
	go func() {
		_, _ = io.WriteString(stdoutW, "%begin 0 5 0\n%end 0 5 0\n%session-changed $1 one\n")
		lines := bufio.NewScanner(stdinR)
		for _, a := range answers {
			if !lines.Scan() {
				return
			}
			_, _ = io.WriteString(stdoutW, a)
		}
		_, _ = io.WriteString(stdoutW, "%exit\n")
		_ = stdoutW.Close()
		_, _ = io.Copy(io.Discard, stdinR)
	}()
	run, err := tmux.StartControlOn(ctx, stdinW, stdoutR)
	if err != nil {
		t.Fatalf("starting: %v", err)
	}
	out, err := run([]string{"list-sessions"})
	if want := "line one\n%end 2 9 1\n%error 1 11 1\nline two\n"; err != nil || out != want {
		t.Errorf("reply: got %q, %v; want %q", out, err, want)
	}
	if _, err := run([]string{"list-windows", "-t", "x"}); err == nil || !strings.Contains(err.Error(), "can't find session") {
		t.Errorf("error reply: got %v", err)
	}
	if _, err := run([]string{"list-sessions"}); err == nil {
		t.Error("a command after the client ended succeeded")
	}
}
