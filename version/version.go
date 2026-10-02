// Package version exposes build version information. Version is set at link
// time by the build system via -ldflags "-X".
package version

// Name is the overall name of the application at the Git repository level.
const Name = "tmux-tabbed-terminal"

// Description is an overall description of its function.
const Description = `A GNOME Terminal style terminal which shows your tmux sessions in a sidebar`

//nolint:gochecknoglobals // overridden at link time
var Version = "v0.0.0-dev"
