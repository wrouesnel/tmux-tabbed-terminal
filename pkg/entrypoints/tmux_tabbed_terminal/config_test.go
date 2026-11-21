package tmux_tabbed_terminal_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chigopher/pathlib"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/entrypoints/tmux_tabbed_terminal"
)

func TestUnmarshalEmptyConfig(t *testing.T) {
	for name, content := range map[string]string{
		"empty":         "",
		"comments only": "# nothing set yet\n",
		"blank lines":   "\n\n",
	} {
		path := filepath.Join(t.TempDir(), "config.yml")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		config := tmux_tabbed_terminal.DefaultConfig()
		if err := tmux_tabbed_terminal.UnmarshalConfig(pathlib.NewPath(path), &config); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if config.Activity.PollInterval != tmux_tabbed_terminal.DefaultConfig().Activity.PollInterval {
			t.Errorf("%s: the defaults changed", name)
		}
	}
}
