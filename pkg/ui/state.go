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
	// Appearance is what was chosen in Preferences.
	Appearance AppearancePrefs `yaml:"appearance,omitempty"`
	// Pinned are the sessions listed at the top of the session list, in order.
	Pinned []PinnedSession `yaml:"pinned,omitempty"`
	// PinsCollapsed hides the pinned sessions under their heading.
	PinsCollapsed bool `yaml:"pins-collapsed,omitempty"`
	// HideUnavailablePins hides pinned sessions which aren't running.
	HideUnavailablePins bool `yaml:"hide-unavailable-pins,omitempty"`
	// HideTabBar hides the tabs above the panes.
	HideTabBar bool `yaml:"hide-tab-bar,omitempty"`
}

// PinnedSession is a session pinned to the top of the list. It's pinned by name rather
// than tmux's session ID, which changes when the tmux server restarts.
type PinnedSession struct {
	Host string `yaml:"host"`
	Name string `yaml:"name"`
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
