package tmux

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pkg/errors"
)

// SSH runs tmux on another host through ssh.
//
// Every command shares one master connection per host (ssh ControlMaster), so polling a
// remote server doesn't open a new connection each time. The master is started on its own
// with ensureMaster, and commands never become the master: a backgrounded master keeps
// the stdio it started with, which would hold a command's output pipe, or a terminal's
// pty, open. Background commands run with BatchMode, so they fail rather than wait for a
// password; the host needs key or agent authentication. Interactive attaches don't, so ssh
// can ask the user in the terminal if there's no master.
type SSH struct {
	// Destination is what ssh connects to, such as user@host or a ~/.ssh/config alias.
	Destination string
	// Binary is the ssh executable. Empty means ssh.
	Binary string
	// ControlDir holds the master connection sockets. Empty means DefaultControlDir().
	ControlDir string
	// Options are extra ssh arguments, such as ["-p", "2222"].
	Options []string

	// masterMu stops two commands starting a master at once.
	masterMu sync.Mutex
}

// controlPersist is how long a master connection stays up after its last use.
const controlPersist = "10m"

// DefaultControlDir returns a private directory for ssh master connection sockets.
func DefaultControlDir() string {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "tmux-tabbed-terminal")
}

func (s *SSH) binary() string {
	if s.Binary == "" {
		return "ssh"
	}
	return s.Binary
}

// controlPath returns the master connection socket of this destination and options.
func (s *SSH) controlPath() string {
	dir := s.ControlDir
	if dir == "" {
		dir = DefaultControlDir()
	}
	sum := sha256.Sum256([]byte(s.Destination + "\x00" + strings.Join(s.Options, "\x00")))
	return filepath.Join(dir, "ssh-"+hex.EncodeToString(sum[:8]))
}

// commonOptions are the ssh options of every command, before the destination.
func (s *SSH) commonOptions(master string) []string {
	opts := []string{
		"-o", "ControlMaster=" + master,
		"-o", "ControlPath=" + s.controlPath(),
		"-o", "ControlPersist=" + controlPersist,
		"-o", "ServerAliveInterval=15",
	}
	return append(opts, s.Options...)
}

// ensureMaster starts the master connection if it isn't running.
func (s *SSH) ensureMaster(ctx context.Context) error {
	s.masterMu.Lock()
	defer s.masterMu.Unlock()

	check := append([]string{"-O", "check"}, s.commonOptions("no")...)
	check = append(check, "--", s.Destination)
	if exec.CommandContext(ctx, s.binary(), check...).Run() == nil { //nolint:gosec // configured ssh
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(s.controlPath()), 0o700); err != nil { //nolint:mnd // owner only
		return errors.Wrap(err, "creating the ssh control directory")
	}
	// The master forks into the background once connected and keeps its stderr, so it
	// gets a file rather than a pipe a caller would wait on.
	stderr, err := os.CreateTemp("", "ttt-ssh-*.log")
	if err != nil {
		return err
	}
	defer os.Remove(stderr.Name())
	defer stderr.Close()

	args := append([]string{"-f", "-N", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10"},
		s.commonOptions("yes")...)
	args = append(args, "--", s.Destination)
	cmd := exec.CommandContext(ctx, s.binary(), args...) //nolint:gosec // configured ssh
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		msg, _ := os.ReadFile(stderr.Name())
		return errors.Wrapf(err, "ssh %s: %s", s.Destination, strings.TrimSpace(string(msg)))
	}
	return nil
}

// argv returns the ssh command line running remote on the destination. interactive
// allocates a terminal and allows prompts.
func (s *SSH) argv(interactive bool, remote []string) []string {
	argv := []string{s.binary()}
	if interactive {
		argv = append(argv, "-t")
	} else {
		argv = append(argv, "-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10")
	}
	argv = append(argv, s.commonOptions("no")...)
	// ssh joins the remote command into one string for the remote shell, so it's quoted
	// here and passed as a single argument.
	return append(argv, "--", s.Destination, ShellJoin(remote))
}

// ShellJoin quotes arguments for a POSIX shell and joins them with spaces.
func ShellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = ShellQuote(a)
	}
	return strings.Join(quoted, " ")
}

// ShellQuote quotes one argument for a POSIX shell. Arguments made only of characters
// the shell treats literally are left alone.
func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.IndexFunc(s, func(r rune) bool { return !shellLiteral(r) }) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellLiteral reports whether a POSIX shell treats r literally outside quotes.
func shellLiteral(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:@,+%", r)
}
