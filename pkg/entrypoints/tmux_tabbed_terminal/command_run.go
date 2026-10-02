package tmux_tabbed_terminal

import (
	"context"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/ui"
)

// RunCmd opens the terminal window. It is the default command.
type RunCmd struct {
	Separate bool `help:"Run a separate instance instead of opening a window in a running one"`
}

// Run is invoked by kong with the values bound in Entrypoint.
func (r *RunCmd) Run(ctx context.Context, config *EntrypointConfig) error {
	return ui.Run(ctx, config.Config, ui.Options{Separate: r.Separate})
}
