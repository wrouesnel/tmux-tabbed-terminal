//go:build !windows

package ui

import (
	"os"
	"strconv"
	"syscall"

	"github.com/pkg/errors"
)

// relaunch replaces this process with the program at path, run as it was.
func relaunch(path string, env []string) error {
	// Libraries' files, such as the D-Bus connection holding the application's name, would
	// otherwise live on, unused, in the new program.
	if fds, err := os.ReadDir("/dev/fd"); err == nil {
		for _, fd := range fds {
			if n, err := strconv.Atoi(fd.Name()); err == nil && n > 2 {
				syscall.CloseOnExec(n)
			}
		}
	}
	return errors.Wrap(syscall.Exec(path, os.Args, env), "starting the new version")
}
