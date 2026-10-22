//go:build !linux && !windows

package pty

import (
	"errors"
	"os/exec"
)

// ErrUnsupported is returned by Start where there's no pty support yet.
var ErrUnsupported = errors.New("pty: not supported on this system")

func start(*exec.Cmd, int, int) (Pty, error) { return nil, ErrUnsupported }
