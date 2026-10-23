package tmux_test

import (
	"testing"

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
