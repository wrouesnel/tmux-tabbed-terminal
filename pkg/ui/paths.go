package ui

import (
	"os"
	"path/filepath"
	"runtime"
)

// userDir returns a per-user base directory: the XDG variable env if it's set, otherwise
// %LOCALAPPDATA% on Windows, and the XDG default, home joined with homeRel, elsewhere.
func userDir(env string, homeRel ...string) string {
	if dir := os.Getenv(env); dir != "" {
		return dir
	}
	if runtime.GOOS == "windows" {
		// On Windows the user cache directory is %LOCALAPPDATA%.
		if dir, err := os.UserCacheDir(); err == nil {
			return dir
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(append([]string{home}, homeRel...)...)
}
