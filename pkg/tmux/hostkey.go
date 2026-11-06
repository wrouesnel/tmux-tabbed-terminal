package tmux

import (
	"context"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/pkg/errors"
)

// HostKeyProblem is why ssh refused a host: its key isn't the one known for it, or no
// key is known for it and ssh couldn't ask whether to trust the one it sent.
type HostKeyProblem struct {
	// Host is the host as the known hosts file names it, such as example.com or
	// [example.com]:2222, when ssh said.
	Host string
	// Changed is set when the host sent a key other than the one known for it.
	Changed bool
	// KeyType and Fingerprint are the key the host sent, such as ED25519 and
	// SHA256:..., when ssh said.
	KeyType, Fingerprint string
	// KnownHosts and Line are where the old key is, when Changed.
	KnownHosts string
	Line       int
}

var (
	changedRe     = regexp.MustCompile(`Host key for (\S+) has changed and you have requested strict checking`)
	unknownRe     = regexp.MustCompile(`No (\S+) host key is known for (\S+) and you have requested strict checking`)
	sentKeyRe     = regexp.MustCompile(`The fingerprint for the (\S+) key sent by the remote host is\s+(\S+?)\.?\s*(?:\n|$)`)
	offendingRe   = regexp.MustCompile(`Offending \S+ key in (.+):(\d+)`)
	removeWithRe  = regexp.MustCompile(`ssh-keygen -f '([^']+)' -R '([^']+)'`)
	serverKeyRe   = regexp.MustCompile(`Server host key: (\S+) (\S+)`)
	addedRe       = regexp.MustCompile(`Permanently added '([^']+)'`)
	exitMarkerRe  = regexp.MustCompile(`(?m)^ttt-exit (\d+)\s*$`)
	sshKeyTypeMap = map[string]string{
		"ssh-ed25519": "ED25519", "ssh-rsa": "RSA", "ssh-dss": "DSA",
		"ecdsa-sha2-nistp256": "ECDSA", "ecdsa-sha2-nistp384": "ECDSA", "ecdsa-sha2-nistp521": "ECDSA",
		"sk-ssh-ed25519@openssh.com": "ED25519-SK", "sk-ecdsa-sha2-nistp256@openssh.com": "ECDSA-SK",
	}
)

// FindHostKeyProblem returns the host key problem ssh reported in an error, if it did.
func FindHostKeyProblem(err error) (*HostKeyProblem, bool) {
	if err == nil {
		return nil, false
	}
	msg := err.Error()
	if m := changedRe.FindStringSubmatch(msg); m != nil {
		p := &HostKeyProblem{Host: m[1], Changed: true}
		if m := sentKeyRe.FindStringSubmatch(msg); m != nil {
			p.KeyType, p.Fingerprint = m[1], m[2]
		}
		if m := offendingRe.FindStringSubmatch(msg); m != nil {
			p.KnownHosts = strings.TrimSpace(m[1])
			p.Line, _ = strconv.Atoi(m[2])
		}
		// Newer ssh says how to remove the old key, naming the host as the file does.
		if m := removeWithRe.FindStringSubmatch(msg); m != nil {
			p.KnownHosts, p.Host = m[1], m[2]
		}
		return p, true
	}
	if m := unknownRe.FindStringSubmatch(msg); m != nil {
		return &HostKeyProblem{Host: m[2], KeyType: m[1]}, true
	}
	// In BatchMode, ssh says only this when it knows no key for the host: a changed key
	// always comes with the warning above.
	if strings.Contains(msg, "Host key verification failed") {
		return &HostKeyProblem{}, true
	}
	return nil, false
}

// ProbeHostKey connects to the destination, from the host this hop's ssh runs on, as
// far as ssh checking its key, and returns the key it sent.
func (s *SSH) ProbeHostKey(ctx context.Context) (keyType, fingerprint string, err error) {
	out, _, err := s.runOnOrigin(ctx, s.probeArgv("yes"))
	if err != nil {
		return "", "", err
	}
	keyType, fingerprint, ok := lastServerKey(out)
	if !ok {
		return "", "", errors.Errorf("ssh %s didn't say what key the host sent: %s", s.Destination, lastLines(out))
	}
	return keyType, fingerprint, nil
}

// TrustHostKey makes the host this hop's ssh runs on trust the key with fingerprint for
// the destination: it removes the old key, if the problem is a changed one, and adds the
// key the host sends, as long as it's the one with fingerprint. Otherwise it adds none.
func (s *SSH) TrustHostKey(ctx context.Context, p *HostKeyProblem, fingerprint string) error {
	// Nothing changes unless the host still sends the key the user trusted.
	if _, sent, err := s.ProbeHostKey(ctx); err != nil {
		return err
	} else if sent != fingerprint {
		return errors.Errorf("%s now sends a different key, %s, from the one trusted, %s, so nothing was changed",
			s.Destination, sent, fingerprint)
	}
	knownHosts, err := s.userKnownHosts(ctx)
	if err != nil {
		return err
	}
	if p.Changed {
		file := p.KnownHosts
		if file == "" {
			file = knownHosts
		}
		argv := []string{s.keygenBinary(), "-R", p.Host, "-f", file}
		if out, code, err := s.runOnOrigin(ctx, argv); err != nil || code != 0 {
			return errors.Errorf("removing the old key for %s: %s", p.Host, lastLines(out)+errText(err))
		}
	}
	out, _, err := s.runOnOrigin(ctx, s.probeArgv("accept-new"))
	if err != nil {
		return err
	}
	added := addedRe.FindStringSubmatch(out)
	_, sent, ok := lastServerKey(out)
	switch {
	case added == nil:
		return errors.Errorf("ssh %s didn't add the host's key: %s", s.Destination, lastLines(out))
	case !ok || sent != fingerprint:
		// Another key than the one the user trusted, since the check: take it out again.
		_, _, _ = s.runOnOrigin(ctx, []string{s.keygenBinary(), "-R", added[1], "-f", knownHosts})
		return errors.Errorf("%s sent a different key, %s, from the one trusted, %s, so it wasn't added",
			s.Destination, sent, fingerprint)
	}
	return nil
}

// userKnownHosts returns the known hosts file ssh adds keys for the destination to, on
// the host this hop's ssh runs on.
func (s *SSH) userKnownHosts(ctx context.Context) (string, error) {
	argv := []string{s.binary(), "-G"}
	if config := s.ConfigFile(); config != "" && s.Via == nil {
		argv = append(argv, "-F", config)
	}
	argv = append(append(argv, s.Options...), "--", s.Destination)
	out, code, err := s.runOnOrigin(ctx, argv)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "userknownhostsfile "); ok && code == 0 {
			if files := splitPaths(rest); len(files) > 0 {
				return files[0], nil
			}
		}
	}
	return "", errors.Errorf("ssh %s didn't say where it keeps known hosts: %s", s.Destination, lastLines(out))
}

// splitPaths splits ssh -G's list of files, which it separates with spaces, though a
// path may have spaces too, as Windows home directories can: a new path starts with /,
// ~ or a drive letter.
func splitPaths(list string) []string {
	var paths []string
	for _, field := range strings.Fields(list) {
		starts := strings.HasPrefix(field, "/") || strings.HasPrefix(field, "~") ||
			len(field) > 2 && field[1] == ':' && (field[2] == '\\' || field[2] == '/')
		if starts || len(paths) == 0 {
			paths = append(paths, field)
			continue
		}
		paths[len(paths)-1] += " " + field
	}
	return paths
}

// probeArgv is an ssh command, as run on the host before this hop, which connects to
// the destination on a connection of its own, with StrictHostKeyChecking set to strict,
// and says in its debugging output which key the host sent. It gets no further than
// authenticating, which doesn't matter.
func (s *SSH) probeArgv(strict string) []string {
	argv := []string{
		s.binary(), "-v", "-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10",
		"-o", "StrictHostKeyChecking=" + strict, "-o", "ControlMaster=no", "-o", "ControlPath=none",
	}
	if config := s.ConfigFile(); config != "" && s.Via == nil {
		argv = append(argv, "-F", config)
	}
	argv = append(argv, s.Options...)
	return append(argv, "--", s.Destination, "true")
}

// keygenBinary is ssh-keygen, beside the ssh this hop runs where that's a path.
func (s *SSH) keygenBinary() string {
	if s.Via == nil && s.Binary != "" && filepath.IsAbs(s.Binary) {
		return filepath.Join(filepath.Dir(s.Binary), "ssh-keygen"+filepath.Ext(s.Binary))
	}
	return "ssh-keygen"
}

// runOnOrigin runs a command on the host this hop's ssh runs on, without a terminal or
// anyone to ask, and returns its output and error output together, and its exit code.
// err is only set if the command couldn't be run.
func (s *SSH) runOnOrigin(ctx context.Context, argv []string) (string, int, error) {
	if s.Via == nil {
		cmd := Command(ctx, argv)
		withoutTerminal(cmd)
		out, err := cmd.CombinedOutput()
		if err != nil && cmd.ProcessState == nil {
			return "", 0, errors.Wrapf(err, "running %s", argv[0])
		}
		return string(out), cmd.ProcessState.ExitCode(), nil
	}
	script := `"$@" </dev/null 2>&1; echo "ttt-exit $?"`
	out, err := s.Via.Output(ctx, append([]string{"sh", "-c", script, "sh"}, argv...)...)
	if err != nil {
		return "", 0, err
	}
	m := exitMarkerRe.FindStringSubmatchIndex(out)
	if m == nil {
		return out, 0, errors.Errorf("running %s on %s: no exit status", argv[0], s.Via.Destination)
	}
	code, _ := strconv.Atoi(out[m[2]:m[3]])
	return out[:m[0]], code, nil
}

// lastServerKey returns the last key ssh's debugging output says a host sent: ProxyJump
// hosts come before the destination.
func lastServerKey(out string) (keyType, fingerprint string, ok bool) {
	all := serverKeyRe.FindAllStringSubmatch(out, -1)
	if len(all) == 0 {
		return "", "", false
	}
	m := all[len(all)-1]
	keyType = m[1]
	if t, ok := sshKeyTypeMap[keyType]; ok {
		keyType = t
	}
	return keyType, m[2], true
}

// lastLines returns the last of ssh's output, without its debugging lines, for an error.
func lastLines(out string) string {
	var kept []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "debug") {
			kept = append(kept, line)
		}
	}
	const most = 4
	if len(kept) > most {
		kept = kept[len(kept)-most:]
	}
	return strings.Join(kept, "\n")
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
