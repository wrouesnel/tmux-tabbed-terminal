package ui

import (
	"os"
	"os/exec"

	"github.com/pkg/errors"
)

// relaunch starts the program at path, run as this one was, for it to carry on once this
// process ends.
func relaunch(path string, env []string) error {
	cmd := exec.Command(path, os.Args[1:]...)
	cmd.Env = env
	return errors.Wrap(cmd.Start(), "starting the new version")
}
