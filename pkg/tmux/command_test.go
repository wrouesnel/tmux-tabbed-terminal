package tmux_test

import (
	"bytes"
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

func TestQuotedCommandLine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"plain", []string{"tmux", "ls"}, `"tmux" "ls"`},
		{"format", []string{"tmux", "ls", "-F", "#{session_name}"}, `"tmux" "ls" "-F" "#{session_name}"`},
		{"glob and space", []string{"a *.go", ""}, `"a *.go" ""`},
		{"quote", []string{`say "hi"`}, `"say \"hi\""`},
		{"backslashes", []string{`C:\dir\`, `a\b`}, `"C:\dir\\" "a\b"`},
		{"backslash before quote", []string{`a\"b`}, `"a\\\"b"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := tmux.QuotedCommandLine(c.args); got != c.want {
				t.Errorf("QuotedCommandLine(%q) = %s, want %s", c.args, got, c.want)
			}
		})
	}
}

// TestCommandCancelWithHeldOutput cancels a command whose output pipe another process
// holds open, as an ssh master holds that of a command run over it: it must stop soon
// after, not when the other process lets go.
func TestCommandCancelWithHeldOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := tmux.Command(ctx, []string{"sh", "-c", "(sleep 10; echo late) & echo early; sleep 10"})
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	start := time.Now()
	cancel()
	_ = cmd.Wait()
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("cancelling took %v", took)
	}
	if out.String() != "early\n" {
		t.Errorf("output: %q", out.String())
	}
}
