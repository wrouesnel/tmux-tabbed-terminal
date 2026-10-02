package tmux_tabbed_terminal

import (
	"context"

	"github.com/wrouesnel/ctxstdio"
	"go.yaml.in/yaml/v4"
)

// ConfigCmd prints the configuration in effect: the defaults with the configuration file
// applied.
type ConfigCmd struct{}

// Run prints the configuration as YAML.
func (c *ConfigCmd) Run(ctx context.Context, config *EntrypointConfig) error {
	data, err := yaml.Marshal(config)
	if err != nil {
		return err
	}
	_, err = ctxstdio.StdOut(ctx).Write(data)
	return err
}
