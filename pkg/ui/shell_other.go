//go:build !windows

package ui

import (
	"bufio"
	"os"
	"strings"
)

// bundledTmux returns "": tmux comes from the system here.
func bundledTmux() string { return "" }

// powerShell is only needed for a bundled tmux.
func powerShell() string { return "" }

// shellChoices returns the shells offered in Preferences: the login shells listed in
// /etc/shells.
func shellChoices() []string {
	f, err := os.Open("/etc/shells")
	if err != nil {
		return nil
	}
	defer f.Close()
	choices := []string{}
	seen := map[string]bool{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || seen[line] {
			continue
		}
		seen[line] = true
		choices = append(choices, line)
	}
	return choices
}
