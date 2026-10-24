package pty

import (
	"bytes"
	"fmt"
	"strings"
)

// A Cygwin or MSYS2 program, such as their tmux, is run on a Cygwin pty of its own rather
// than on a pseudo console: their script runs it, over plain pipes. On a pseudo console,
// conhost answers the program's terminal queries itself, and the Cygwin runtime hands the
// answers back to the program mangled, as typing. On a Cygwin pty the queries reach the
// emulator, and the program has a terminal device with a name, as on Unix.

// ttyMarker starts the OSC sequence the shell script runs writes first, naming its pty.
// It's taken out of the output, so the emulator never sees it.
const ttyMarker = "\x1b]7799;tty="

// scriptCommand returns the shell command script runs: it sets the pty's size, names the
// pty, and then becomes argv. The size is set before the name is written, so that resizes
// made after the name is known come later.
func scriptCommand(argv []string, cols, rows int) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = shellQuote(arg)
	}
	return fmt.Sprintf(`stty cols %d rows %d; printf '\033]7799;tty=%%s\007' "$(tty)"; exec %s`,
		cols, rows, strings.Join(quoted, " "))
}

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// cutMarker takes the pty's name out of output. found reports whether a whole marker was
// in it; rest is output with the marker removed.
func cutMarker(output []byte) (name string, rest []byte, found bool) {
	start := bytes.Index(output, []byte(ttyMarker))
	if start < 0 {
		return "", output, false
	}
	end := bytes.IndexByte(output[start:], '\a')
	if end < 0 {
		return "", output, false
	}
	end += start
	name = string(output[start+len(ttyMarker) : end])
	rest = append(append([]byte{}, output[:start]...), output[end+1:]...)
	return name, rest, true
}
