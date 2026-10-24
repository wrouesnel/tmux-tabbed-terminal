package tmux

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ServerPath converts a Windows path, such as a directory or a shell, into the form the
// tmux server takes. A Cygwin or MSYS2 tmux takes only POSIX paths, such as /c/Users,
// and takes C:\Users as relative: their cygpath, beside it, converts the path. Other
// paths, and paths for other tmux, are returned unchanged.
func (c *Client) ServerPath(ctx context.Context, path string) string {
	if c.SSH != nil || !windowsPath(path) {
		return path
	}
	tmux, err := exec.LookPath(c.binary())
	if err != nil {
		return path
	}
	cygpath := filepath.Join(filepath.Dir(tmux), "cygpath.exe")
	if _, err := os.Stat(cygpath); err != nil {
		return path
	}
	out, err := Command(ctx, []string{cygpath, "-u", path}).Output()
	if err != nil {
		return path
	}
	if p := strings.TrimRight(string(out), "\r\n"); p != "" {
		return p
	}
	return path
}

// windowsPath reports whether path is an absolute Windows path, such as C:\Users or
// \\server\share.
func windowsPath(path string) bool {
	return filepath.IsAbs(path) && !strings.HasPrefix(path, "/")
}
