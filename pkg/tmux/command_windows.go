//go:build windows

package tmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// quoteArgs gives cmd a command line with every argument quoted. It also hides the
// console window a console program started by a GUI program opens. The program needs a
// console: a Cygwin program without one, as CREATE_NO_WINDOW would leave it, opens a
// visible one of its own.
func quoteArgs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: QuotedCommandLine(cmd.Args), HideWindow: true}
}

// withoutTerminal does nothing: a Windows program has a console, not a controlling
// terminal, and SSH_ASKPASS_REQUIRE=force makes ssh use SSH_ASKPASS regardless.
func withoutTerminal(*exec.Cmd) {}

// controlArgv returns the command line of a local control client. A Cygwin or MSYS2 tmux
// can't be one on pipes: their runtime can't pass the client's input and output to the
// server, which opens the client's terminal by name instead, and a pipe has none, so the
// client ends at once. Their script gives it a pty, which doesn't echo the commands or
// end lines with CRs. Another tmux is run as it is.
func controlArgv(argv []string) []string {
	program := lookPath(argv[0])
	dir := filepath.Dir(program)
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	if (!exists("msys-2.0.dll") && !exists("cygwin1.dll")) || !exists("script.exe") {
		return argv
	}
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		if i == 0 {
			arg = program
		}
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
	}
	// stty is given by its path, since the runtime's bin folder needn't be in PATH, as it
	// isn't for one installed with this program; /usr/bin is that folder for both.
	return []string{
		filepath.Join(dir, "script.exe"), "-qfec",
		"/usr/bin/stty -echo -onlcr; exec " + strings.Join(quoted, " "), "/dev/null",
	}
}
