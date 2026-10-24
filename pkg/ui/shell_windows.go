package ui

import (
	"os"
	"os/exec"
	"path/filepath"
)

// bundledTmux returns the tmux installed with the application, in the MSYS2 tree beside
// it, or "" if there's none.
func bundledTmux() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	tmux := filepath.Join(filepath.Dir(exe), "msys", "usr", "bin", "tmux.exe")
	if _, err := os.Stat(tmux); err != nil {
		return ""
	}
	return tmux
}

// powerShell returns PowerShell 7 if it's installed, and otherwise Windows PowerShell,
// which always is.
func powerShell() string {
	if pwsh, err := exec.LookPath("pwsh.exe"); err == nil {
		return pwsh
	}
	return filepath.Join(systemRoot(), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
}

// shellChoices returns the shells offered in Preferences: PowerShell, cmd, and the bash
// of the tmux in PATH, if it's an MSYS2 or Cygwin one.
func shellChoices() []string {
	choices := []string{}
	if pwsh, err := exec.LookPath("pwsh.exe"); err == nil {
		choices = append(choices, pwsh)
	}
	choices = append(choices,
		filepath.Join(systemRoot(), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"),
		filepath.Join(systemRoot(), "System32", "cmd.exe"))
	if tmux, err := exec.LookPath("tmux.exe"); err == nil {
		bash := filepath.Join(filepath.Dir(tmux), "bash.exe")
		if _, err := os.Stat(bash); err == nil {
			choices = append(choices, bash)
		}
	}
	return choices
}

func systemRoot() string {
	if root := os.Getenv("SystemRoot"); root != "" {
		return root
	}
	return `C:\Windows`
}
