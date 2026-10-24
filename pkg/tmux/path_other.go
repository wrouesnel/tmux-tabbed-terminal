//go:build !windows

package tmux

import "context"

// ServerPath returns path: tmux takes this system's paths as they are.
func (c *Client) ServerPath(_ context.Context, path string) string { return path }
