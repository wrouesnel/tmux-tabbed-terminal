package tmux

import (
	"context"
	"strings"

	"github.com/pkg/errors"
)

// authorizeScript adds the public key in its first argument to ~/.ssh/authorized_keys,
// unless it's there already, making the directory and file private to the user.
const authorizeScript = `umask 077
mkdir -p ~/.ssh || exit 1
f=~/.ssh/authorized_keys
if [ -f "$f" ] && grep -qxF "$1" "$f"; then exit 0; fi
if [ -s "$f" ] && [ -n "$(tail -c 1 "$f")" ]; then echo >> "$f"; fi
printf '%s\n' "$1" >> "$f"`

// AuthorizeKey lets a public key, in authorized_keys format, log in to the destination,
// by adding it to ~/.ssh/authorized_keys there. It runs over the hop's connection.
func (s *SSH) AuthorizeKey(ctx context.Context, publicKey string) error {
	publicKey = strings.TrimSpace(publicKey)
	if publicKey == "" || strings.ContainsAny(publicKey, "\r\n") {
		return errors.New("not a single public key")
	}
	if _, err := s.Output(ctx, "sh", "-c", authorizeScript, "sh", publicKey); err != nil {
		return errors.Wrap(err, "adding the key to ~/.ssh/authorized_keys")
	}
	return nil
}

// CheckKeyLogin logs in to the destination, from the host this hop's ssh runs on, on a
// connection of its own, without a password or anyone to ask, to check ssh can log in
// with a key alone.
func (s *SSH) CheckKeyLogin(ctx context.Context) error {
	argv := []string{
		s.binary(), "-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10",
		"-o", "PasswordAuthentication=no", "-o", "KbdInteractiveAuthentication=no",
		"-o", "ControlMaster=no", "-o", "ControlPath=none",
	}
	if config := s.ConfigFile(); config != "" && s.Via == nil {
		argv = append(argv, "-F", config)
	}
	argv = append(append(argv, s.Options...), "--", s.Destination, "true")
	out, code, err := s.runOnOrigin(ctx, argv)
	if err != nil {
		return err
	}
	if code != 0 {
		return errors.Errorf("ssh %s couldn't log in with a key: %s", s.Destination, lastLines(out))
	}
	return nil
}
