package pty

import (
	"os/exec"
	"strings"
	"testing"
)

func TestScriptCommand(t *testing.T) {
	got := scriptCommand([]string{`C:\msys64\usr\bin\tmux.exe`, "attach", "-t", "it's #{x}"}, 97, 36)
	want := `stty cols 97 rows 36; printf '\033]7799;tty=%s\007' "$(tty)"; ` +
		`exec 'C:\msys64\usr\bin\tmux.exe' 'attach' '-t' 'it'\''s #{x}'`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// TestScriptCommandShell runs the command's quoting through a real shell.
func TestScriptCommandShell(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	args := []string{"printf", `%s|`, "it's", "#{x} *", `back\slash`, "$HOME"}
	line := scriptCommand(args, 80, 24)
	// Only the exec is run: there's no terminal here for stty and tty.
	line = line[strings.Index(line, "exec "):]
	out, err := exec.Command(sh, "-c", line).Output()
	if err != nil {
		t.Fatal(err)
	}
	if want := `it's|#{x} *|back\slash|$HOME|`; string(out) != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

func TestCutMarker(t *testing.T) {
	for _, tc := range []struct {
		in, name, rest string
		found          bool
	}{
		{"\x1b]7799;tty=/dev/pty6\x07\x1b[?1049h", "/dev/pty6", "\x1b[?1049h", true},
		{"before\x1b]7799;tty=/dev/pty0\x07after", "/dev/pty0", "beforeafter", true},
		{"\x1b]7799;tty=/dev/pt", "", "\x1b]7799;tty=/dev/pt", false},
		{"plain", "", "plain", false},
	} {
		name, rest, found := cutMarker([]byte(tc.in))
		if name != tc.name || string(rest) != tc.rest || found != tc.found {
			t.Errorf("cutMarker(%q) = %q, %q, %v", tc.in, name, rest, found)
		}
	}
}
