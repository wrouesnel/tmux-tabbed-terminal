package ui

import (
	"time"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

// Config configures the user interface. It is loaded from the configuration file.
type Config struct {
	Tmux       TmuxConfig       `yaml:"tmux"`
	Appearance AppearanceConfig `yaml:"appearance"`
	Activity   ActivityConfig   `yaml:"activity"`
	Sidebar    SidebarConfig    `yaml:"sidebar"`
	TabBar     TabBarConfig     `yaml:"tab-bar"`
	Clipboard  ClipboardConfig  `yaml:"clipboard"`
	Keyboard   KeyboardConfig   `yaml:"keyboard"`
	Links      LinksConfig      `yaml:"links"`
	Behaviour  BehaviourConfig  `yaml:"behaviour"`
	// Hosts are remote hosts whose sessions are listed, besides those added in the UI.
	Hosts []HostConfig `yaml:"hosts"`
}

// TmuxConfig selects the tmux server.
type TmuxConfig struct {
	// Binary is the tmux executable.
	Binary string `yaml:"binary"`
	// SocketName selects a server by name, as with tmux -L.
	SocketName string `yaml:"socket-name"`
	// SocketPath selects a server by socket path, as with tmux -S.
	SocketPath string `yaml:"socket-path"`
}

// Client returns a tmux client for the configured server.
func (c TmuxConfig) Client() *tmux.Client {
	client := tmux.NewClient()
	if c.Binary != "" {
		client.Binary = c.Binary
	}
	client.SocketName = c.SocketName
	client.SocketPath = c.SocketPath
	return client
}

// AppearanceConfig sets how terminals look. Settings left empty come from the default
// GNOME Terminal profile when UseGnomeTerminalProfile is set, and otherwise from the
// desktop's monospace font and the GTK theme.
type AppearanceConfig struct {
	// UseGnomeTerminalProfile reads font and colors from GNOME Terminal's default profile.
	UseGnomeTerminalProfile bool `yaml:"use-gnome-terminal-profile"`
	// Font is a Pango font description such as "Monospace 11".
	Font string `yaml:"font"`
	// Foreground and Background are colors such as "#d0cfcc".
	Foreground string `yaml:"foreground"`
	Background string `yaml:"background"`
	// Palette is either one built-in palette name (gnome, tango, linux, xterm, solarized)
	// or a list of 16 colors.
	Palette []string `yaml:"palette"`
	// BoldIsBright shows bold text in bright colors.
	BoldIsBright *bool `yaml:"bold-is-bright"`
	// ScrollbackLines is the terminal's own scrollback. tmux keeps the real history, so
	// this is only what scrolls off when tmux isn't drawing the alternate screen.
	ScrollbackLines int `yaml:"scrollback-lines"`
	// SidebarWidth is the initial width of the session list in pixels.
	SidebarWidth int `yaml:"sidebar-width"`
}

// ActivityConfig sets how session activity is detected.
type ActivityConfig struct {
	// PollInterval is how often tmux is asked for session state.
	PollInterval time.Duration `yaml:"poll-interval"`
	// Timeout is how long a session shows as busy after its last output.
	Timeout time.Duration `yaml:"timeout"`
}

// SidebarConfig sets how the session list is arranged.
type SidebarConfig struct {
	// GroupByApplication groups sessions by the program running in their current window.
	GroupByApplication bool `yaml:"group-by-application"`
	// GroupHold is how long a session must run a new program before it changes group.
	GroupHold time.Duration `yaml:"group-hold"`
	// Position is the side of the window the list is on: left or right. The button under
	// the list changes it, and that choice is remembered.
	Position string `yaml:"position"`
}

// TabBarConfig sets how the tab bar of tmux windows is arranged.
type TabBarConfig struct {
	// Position is where the bar is, above or below the panes: top or bottom. Preferences
	// changes it, and that choice is remembered.
	Position string `yaml:"position"`
}

// ClipboardConfig sets how the desktop clipboard follows tmux.
type ClipboardConfig struct {
	// FromTmux puts text copied in tmux, on any host, on the desktop clipboard: each new
	// paste buffer, as copy mode makes, and as programs make with OSC 52 when tmux's
	// set-clipboard option is on.
	FromTmux bool `yaml:"from-tmux"`
}

// LinksConfig sets what links in terminals do.
type LinksConfig struct {
	// ImagePreviews shows the image a link names, on this machine or a remote host, when
	// the pointer rests on the link.
	ImagePreviews bool `yaml:"image-previews"`
}

// KeyboardConfig sets the keyboard shortcuts.
type KeyboardConfig struct {
	// Shortcuts replace the default shortcuts of actions. Keys are action names: those
	// with default shortcuts by their short name, such as paste or split-right, and any
	// action by its full name, such as win.save-scrollback. Values are GTK accelerators,
	// such as "<Control><Shift>v" or "<Shift>Insert". An empty list leaves the action
	// without a shortcut.
	Shortcuts map[string][]string `yaml:"shortcuts"`
}

// BehaviourConfig sets what the application does on its own.
type BehaviourConfig struct {
	// CreateSessionOnStart creates a session when a window opens and there are none.
	CreateSessionOnStart bool `yaml:"create-session-on-start"`
	// NewSessionDirectory is where new sessions start when the current one's directory
	// isn't known. Empty means the home directory.
	NewSessionDirectory string `yaml:"new-session-directory"`
	// ConfirmKill asks before killing a session.
	ConfirmKill bool `yaml:"confirm-kill"`
}

// Defaults for Config.
const (
	defaultPollInterval    = time.Second
	defaultActivityTimeout = 2 * time.Second
	defaultSidebarWidth    = 220
	defaultScrollback      = 1000
	defaultGroupHold       = 3 * time.Second
	// minPollInterval stops a bad config from spinning tmux processes continuously.
	minPollInterval = 100 * time.Millisecond
)

// DefaultConfig returns the default configuration.
func DefaultConfig() Config {
	return Config{
		Tmux: TmuxConfig{Binary: "tmux"},
		Appearance: AppearanceConfig{
			UseGnomeTerminalProfile: true,
			ScrollbackLines:         defaultScrollback,
			SidebarWidth:            defaultSidebarWidth,
		},
		Activity: ActivityConfig{
			PollInterval: defaultPollInterval,
			Timeout:      defaultActivityTimeout,
		},
		Sidebar: SidebarConfig{
			GroupByApplication: true,
			GroupHold:          defaultGroupHold,
			Position:           "left",
		},
		TabBar: TabBarConfig{
			Position: "top",
		},
		Clipboard: ClipboardConfig{
			FromTmux: true,
		},
		Links: LinksConfig{
			ImagePreviews: true,
		},
		Behaviour: BehaviourConfig{
			CreateSessionOnStart: true,
			ConfirmKill:          true,
		},
	}
}
