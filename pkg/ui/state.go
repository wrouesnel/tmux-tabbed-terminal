package ui

import (
	"os"
	"path/filepath"

	"github.com/pkg/errors"
	"go.yaml.in/yaml/v4"

	"github.com/wrouesnel/tmux-tabbed-terminal/version"
)

// uiState holds choices made in the UI, such as which side the session list is on, so
// they last between runs. Unset fields fall back to the configuration file.
type uiState struct {
	SidebarRight       *bool `yaml:"sidebar-right,omitempty"`
	GroupByApplication *bool `yaml:"group-by-application,omitempty"`
}

// stateFile is where uiState is kept.
func stateFile() string {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, version.Name, "state.yml")
}

// loadState reads the UI state. A missing file is an empty state.
func loadState(path string) (uiState, error) {
	var st uiState
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	return st, errors.Wrapf(yaml.Unmarshal(data, &st), "parsing %s", path)
}

// saveState writes the UI state, replacing the file atomically.
func saveState(path string, st uiState) error {
	data, err := yaml.Marshal(st)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil { //nolint:mnd // owner only
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil { //nolint:mnd // owner only
		return err
	}
	return os.Rename(tmp, path)
}
