package tmux

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// waitDelay is how long a command's output is still read once it has been killed, as
// when its context ends, or after it exits. A command run over an ssh master hands its
// output pipe to the master, which keeps writing to it, and holding it open, after the
// command itself is gone: without a limit, cancelling a download would wait for the
// whole file.
const waitDelay = 2 * time.Second

// Command is exec.CommandContext for tmux and ssh. On Windows, it quotes every argument
// on the command line the program is given. A Cygwin or MSYS2 program, such as their
// tmux, expands braces and globs in the arguments it's started with by a Windows program,
// and so would make tmux's #{format} strings into #format, but leaves quoted ones alone.
func Command(ctx context.Context, argv []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // tmux and ssh are configured by the user
	cmd.WaitDelay = waitDelay
	quoteArgs(cmd)
	return cmd
}

// QuotedCommandLine joins args into a Windows command line, quoting each by the
// Microsoft C runtime's rules, which Cygwin's follow too, whether it needs it or not.
func QuotedCommandLine(args []string) string {
	var b strings.Builder
	for i, arg := range args {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteByte('"')
		slashes := 0
		for j := range len(arg) {
			switch arg[j] {
			case '\\':
				slashes++
			case '"':
				// Backslashes before a quote escape each other, and one more escapes it.
				b.WriteString(strings.Repeat(`\`, slashes+1))
				slashes = 0
			default:
				slashes = 0
			}
			b.WriteByte(arg[j])
		}
		// Backslashes before the closing quote escape each other too.
		b.WriteString(strings.Repeat(`\`, slashes))
		b.WriteByte('"')
	}
	return b.String()
}
