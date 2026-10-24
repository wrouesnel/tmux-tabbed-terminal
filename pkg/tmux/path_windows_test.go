package tmux

import "testing"

func TestWindowsPath(t *testing.T) {
	for path, want := range map[string]bool{
		`C:\Users\me`:     true,
		`C:/Users/me`:     true,
		`\\server\share`:  true,
		`/c/Users/me`:     false,
		`relative\path`:   false,
		`/usr/bin/bash`:   false,
		`C:relative\path`: false,
	} {
		if got := windowsPath(path); got != want {
			t.Errorf("windowsPath(%q) = %v", path, got)
		}
	}
}
