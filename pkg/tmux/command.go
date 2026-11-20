package tmux

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
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
	cmd := exec.CommandContext(ctx, lookPath(argv[0]), argv[1:]...) //nolint:gosec // tmux and ssh are configured by the user
	cmd.Args[0] = argv[0]
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

// lookPathTTL is how long a program found in PATH is taken to stay where it was found.
const lookPathTTL = time.Minute

// lookPathKey is a program looked up in a PATH.
type lookPathKey struct{ name, path string }

// lookPathEntry is where a program was found, and when.
type lookPathEntry struct {
	file string
	at   time.Time
}

//nolint:gochecknoglobals // a cache of lookups, shared by every command
var lookPaths struct {
	sync.Mutex
	m map[lookPathKey]lookPathEntry
}

// lookPath returns where name is in PATH, as exec.Command would find it. Polling runs
// tmux several times a second, and searching PATH each time stats every directory in it,
// so where it's found is remembered for a while. A name with a directory, or one which
// isn't found, is returned as it is, for exec.Command to deal with.
func lookPath(name string) string {
	if strings.ContainsAny(name, `/\`) {
		return name
	}
	key := lookPathKey{name, os.Getenv("PATH")}
	now := time.Now()
	lookPaths.Lock()
	defer lookPaths.Unlock()
	if e, ok := lookPaths.m[key]; ok && now.Sub(e.at) < lookPathTTL {
		return e.file
	}
	file, err := exec.LookPath(name)
	if err != nil {
		return name
	}
	if lookPaths.m == nil {
		lookPaths.m = map[lookPathKey]lookPathEntry{}
	}
	lookPaths.m[key] = lookPathEntry{file, now}
	return file
}
