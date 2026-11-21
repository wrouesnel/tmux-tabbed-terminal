package tmux_tabbed_terminal

import (
	"errors"
	"io"

	"github.com/chigopher/pathlib"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/ui"
	"go.yaml.in/yaml/v4"
)

// UnmarshalConfig wraps the yaml.Unmarshaling process for entrypoint configuration files.
// This includes ensuring that the necessary providers from other parts of the application
// are loaded.
func UnmarshalConfig(path *pathlib.Path, config *EntrypointConfig) error {
	configFile, err := path.Open()
	if err != nil {
		return err
	}
	defer configFile.Close()

	loader, err := yaml.NewLoader(configFile, YamlOption())
	if err != nil {
		return err
	}
	// A file with nothing in it, or only comments, has no document: it changes nothing.
	if err := loader.Load(config); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// YamlOption returns the correct YAML options for decoding entrypoint configuration.
func YamlOption() yaml.Option {
	return yaml.Options()
}

// EntrypointConfig unmarshals to configure the application. Keys missing from the file
// keep the values from DefaultConfig.
type EntrypointConfig struct {
	ui.Config `yaml:",inline"`
}

// DefaultConfig returns the configuration used when there is no configuration file.
func DefaultConfig() EntrypointConfig {
	return EntrypointConfig{Config: ui.DefaultConfig()}
}
