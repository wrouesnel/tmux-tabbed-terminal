package ui

import (
	"context"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/pkg/errors"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/sshconfig"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

// notFoundStatus is the exit status the remote read uses for a missing file.
const notFoundStatus = "exit status 3"

// safeGlob matches the Include patterns expanded on a remote host. They're expanded by its
// shell, so anything a shell would treat specially, other than glob characters, is
// refused.
var safeGlob = regexp.MustCompile(`^[A-Za-z0-9._/*?~\[\]@+-]+$`)

// remoteSSHSource reads a remote host's ssh configuration through its ssh connection.
type remoteSSHSource struct {
	ctx  context.Context
	ssh  *tmux.SSH
	home string
}

// loadRemoteSSHConfig reads the hosts of a remote host's ~/.ssh/config, with its
// includes.
func loadRemoteSSHConfig(ctx context.Context, ssh *tmux.SSH) ([]sshconfig.Host, error) {
	home, err := ssh.Output(ctx, "sh", "-c", `printf %s "$HOME"`)
	if err != nil {
		return nil, err
	}
	if home == "" {
		return nil, errors.New("the remote host has no home directory")
	}
	src := &remoteSSHSource{ctx: ctx, ssh: ssh, home: home}
	return sshconfig.LoadFrom(src, path.Join(home, ".ssh", "config"))
}

func (r *remoteSSHSource) Home() string { return r.home }

func (r *remoteSSHSource) ReadFile(p string) ([]byte, error) {
	out, err := r.ssh.Output(r.ctx, "sh", "-c", `[ -f "$1" ] || exit 3; cat -- "$1"`, "sh", p)
	if err != nil {
		if strings.Contains(err.Error(), notFoundStatus) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	return []byte(out), nil
}

func (r *remoteSSHSource) Glob(pattern string) ([]string, error) {
	if !safeGlob.MatchString(pattern) {
		return nil, nil
	}
	// The pattern is left unquoted so the remote shell expands it.
	script := `for f in ` + pattern + `; do [ -e "$f" ] && printf '%s\n' "$f"; done; true`
	out, err := r.ssh.Output(r.ctx, "sh", "-c", script)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if line != "" {
			paths = append(paths, line)
		}
	}
	return paths, nil
}
