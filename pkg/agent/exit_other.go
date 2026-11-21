//go:build !unix

package agent

import (
	"os"
)

// exitOf says how a program that ran ended.
func exitOf(state *os.ProcessState) *exit {
	return &exit{Code: state.ExitCode()}
}
