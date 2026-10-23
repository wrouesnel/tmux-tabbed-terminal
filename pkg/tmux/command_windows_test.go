//go:build windows

package tmux_test

import (
	"slices"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

// Windows reads a quoted command line back into the same arguments.
func TestQuotedCommandLineRoundTrip(t *testing.T) {
	t.Parallel()
	args := []string{"tmux", "#{session_name}", `C:\dir\`, `say "hi"`, `a\"b`, "", "a b"}
	got, err := windows.DecomposeCommandLine(tmux.QuotedCommandLine(args))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, args) {
		t.Errorf("got %q, want %q", got, args)
	}
}
