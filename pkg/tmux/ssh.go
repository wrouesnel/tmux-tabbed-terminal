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
//
// A host can be reached through another (Via): its ssh then runs on that host, so its
// destination is resolved with that host's ssh configuration and keys. Hops nest to any
// depth. Each hop's master lives on the host its ssh runs on.
type SSH struct {
	// Destination is what ssh connects to, such as user@host or a ~/.ssh/config alias.
	Destination string
	// Binary is the ssh executable, on the host this hop runs from. Empty means ssh.
	Binary string
	// ControlDir holds the master connection sockets of a hop run from this machine.
	// Empty means DefaultControlDir(). Hops run from another host keep theirs in its
	// ~/.ssh.
	ControlDir string
	// Options are extra ssh arguments, such as ["-p", "2222"].
	Options []string
	// Via is the host this one is reached through, or nil to connect from this machine.
	Via *SSH
	// ForwardAgent forwards this machine's ssh agent to Via's host for this hop, so the
	// ssh run there can use the user's keys. Every session on the way forwards it.
	ForwardAgent bool

	// masterMu stops two commands starting a master at once.
	masterMu sync.Mutex
	// remoteMasterUp is set once a hop run from another host has a master. Checking it
	// takes a round trip, so it's only checked again after a command fails.
	remoteMasterUp bool
}

// controlPersist is how long a master connection stays up after its last use.
const controlPersist = "10m"

// remoteControlPath is where a hop run from another host keeps its master socket. ssh
// expands ~ and %C (a hash of the connection) itself.
const remoteControlPath = "~/.ssh/ttt-%C"

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

// controlPath returns the master connection socket of this hop.
func (s *SSH) controlPath() string {
	if s.Via != nil {
		return remoteControlPath
	}
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

// masterArgs are the arguments which start this hop's master. A session sharing a
// master only gets the agent forwarded if the master allows it, so every master does: it
// opens no session of its own, and only sessions asking for the agent (ForwardAgent on
// the hop beyond) get it.
func (s *SSH) masterArgs() []string {
	args := append([]string{"-f", "-N", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-o", "ForwardAgent=yes"},
		s.commonOptions("yes")...)
	return append(args, "--", s.Destination)
}

// checkArgs are the arguments which check whether this hop's master is running.
func (s *SSH) checkArgs() []string {
	args := append([]string{"-O", "check"}, s.commonOptions("no")...)
	return append(args, "--", s.Destination)
}

// ensureMaster starts the master connections of this hop and every hop before it, where
// they aren't running.
func (s *SSH) ensureMaster(ctx context.Context) error {
	if s.Via != nil {
		if err := s.Via.ensureMaster(ctx); err != nil {
			return err
		}
	}
	s.masterMu.Lock()
	defer s.masterMu.Unlock()
	if s.Via != nil {
		return s.ensureRemoteMaster(ctx)
	}

	if exec.CommandContext(ctx, s.binary(), s.checkArgs()...).Run() == nil { //nolint:gosec // configured ssh
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

	cmd := exec.CommandContext(ctx, s.binary(), s.masterArgs()...) //nolint:gosec // configured ssh
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		msg, _ := os.ReadFile(stderr.Name())
		return errors.Wrapf(err, "ssh %s: %s", s.Destination, strings.TrimSpace(string(msg)))
	}
	return nil
}

// ensureRemoteMaster starts the master of a hop run from another host, on that host. As
// locally, the master's stderr goes to a file, so the command starting it can finish.
func (s *SSH) ensureRemoteMaster(ctx context.Context) error {
	if s.remoteMasterUp {
		return nil
	}
	ssh := ShellQuote(s.binary())
	script := ssh + " " + ShellJoin(s.checkArgs()) + " 2>/dev/null && exit 0\n" +
		"f=$(mktemp) || exit 1\n" +
		ssh + " " + ShellJoin(s.masterArgs()) + " </dev/null >/dev/null 2>\"$f\"\n" +
		"rc=$?; cat \"$f\" >&2; rm -f \"$f\"; exit $rc\n"
	if _, err := s.Via.outputForwarding(ctx, s.ForwardAgent, "sh", "-c", script); err != nil {
		return errors.Wrapf(err, "ssh %s from %s", s.Destination, s.Via.Destination)
	}
	s.remoteMasterUp = true
	return nil
}

// resetMasters forgets that the masters of hops run from other hosts are up, so the next
// command checks them again.
func (s *SSH) resetMasters() {
	for hop := s; hop != nil; hop = hop.Via {
		hop.masterMu.Lock()
		hop.remoteMasterUp = false
		hop.masterMu.Unlock()
	}
}

// hopArgv returns the ssh command, as run on the host before this hop, which runs remote
// on the destination. interactive allocates a terminal and allows prompts; forward
// forwards the agent into the destination for a hop beyond it.
func (s *SSH) hopArgv(interactive, forward bool, remote []string) []string {
	argv := []string{s.binary()}
	if interactive {
		argv = append(argv, "-t")
	} else {
		argv = append(argv, "-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10")
	}
	if forward {
		argv = append(argv, "-o", "ForwardAgent=yes")
	}
	argv = append(argv, s.commonOptions("no")...)
	// ssh joins the remote command into one string for the remote shell, so it's quoted
	// here and passed as a single argument.
	return append(argv, "--", s.Destination, ShellJoin(remote))
}

// argv returns the command line, run on this machine, which runs remote on the
// destination through every hop.
func (s *SSH) argv(interactive bool, remote []string) []string {
	return s.chainArgv(interactive, false, remote)
}

// chainArgv is argv, forwarding the agent into the destination if forward is set.
func (s *SSH) chainArgv(interactive, forward bool, remote []string) []string {
	argv := s.hopArgv(interactive, forward, remote)
	if s.Via == nil {
		return argv
	}
	return s.Via.chainArgv(interactive, forward || s.ForwardAgent, argv)
}

// output runs a command on the destination and returns its standard output.
func (s *SSH) output(ctx context.Context, remote ...string) (string, error) {
	return s.outputForwarding(ctx, false, remote...)
}

// outputForwarding is output, forwarding the agent into the destination if forward is set.
func (s *SSH) outputForwarding(ctx context.Context, forward bool, remote ...string) (string, error) {
	argv := s.chainArgv(false, forward, remote)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // configured ssh
	stdout, stderr := new(strings.Builder), new(strings.Builder)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		return "", errors.Wrapf(err, "%s", strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// Output runs a command on the destination, starting master connections as needed, and
// returns its standard output.
func (s *SSH) Output(ctx context.Context, remote ...string) (string, error) {
	if err := s.ensureMaster(ctx); err != nil {
		return "", err
	}
	out, err := s.output(ctx, remote...)
	if err != nil {
		s.resetMasters()
	}
	return out, err
}

// Name describes the route to the destination, such as "db via bastion".
func (s *SSH) Name() string {
	if s.Via == nil {
		return s.Destination
	}
	return s.Destination + " via " + s.Via.Name()
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
