//go:build !windows

package tmux

import "os/exec"

// quoteArgs does nothing: only Windows passes a command line rather than arguments.
func quoteArgs(*exec.Cmd) {}
