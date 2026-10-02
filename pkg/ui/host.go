package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gotk3/gotk3/glib"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"go.yaml.in/yaml/v4"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
	"github.com/wrouesnel/tmux-tabbed-terminal/version"
)

// LocalHost is the name of the tmux server on this machine.
const LocalHost = "local"

// keySep separates the host and session ID in a session key. Neither contains it.
const keySep = "\x1f"

// sessionKey identifies a session across hosts: tmux session IDs are only unique on
// their own server.
func sessionKey(host, id string) string {
	return host + keySep + id
}

// splitKey returns the host and session ID of a key.
func splitKey(key string) (string, string) {
	host, id, _ := strings.Cut(key, keySep)
	return host, id
}

// HostConfig is a remote host whose tmux sessions are listed.
type HostConfig struct {
	// Destination is what ssh connects to: user@host, or a ~/.ssh/config alias.
	Destination string `yaml:"destination"`
	// SSHOptions are extra ssh arguments, such as ["-p", "2222"].
	SSHOptions []string `yaml:"ssh-options,omitempty"`
	// SocketName selects a tmux server on the host other than the default, as with -L.
	SocketName string `yaml:"socket-name,omitempty"`
	// Via is the listed host this one is reached through, by name: ssh runs there, with
	// that host's ssh configuration and keys. Empty means this machine.
	Via string `yaml:"via,omitempty"`
	// ForwardAgent forwards this machine's ssh agent through the origin, for when the
	// origin has no key of its own for the host.
	ForwardAgent bool `yaml:"forward-agent,omitempty"`
}

// Host is one tmux server, on this machine or reached over ssh, and its latest state.
type Host struct {
	Name   string
	Config HostConfig
	client *tmux.Client

	snapshot *tmux.Snapshot
	// err is why the last poll failed, or nil.
	err error
	// polled is set once a poll has finished, successfully or not.
	polled bool

	kick   chan struct{}
	cancel context.CancelFunc
}

func newHost(name string, cfg HostConfig, client *tmux.Client) *Host {
	return &Host{
		Name:     name,
		Config:   cfg,
		client:   client,
		snapshot: &tmux.Snapshot{},
		kick:     make(chan struct{}, 1),
	}
}

// Local reports whether the host is this machine.
func (h *Host) Local() bool { return h.Name == LocalHost }

// requestPoll asks the host's poller for a snapshot soon.
func (h *Host) requestPoll() {
	select {
	case h.kick <- struct{}{}:
	default:
	}
}

// errNoOrigin is returned for a host whose origin host isn't listed.
var errNoOrigin = errors.New("origin host is not listed")

// newRemoteHost makes a remote host from its configuration. Its origin, if any, must
// already be listed: the host's ssh runs there.
func (a *App) newRemoteHost(cfg HostConfig) (*Host, error) {
	ssh := &tmux.SSH{Destination: cfg.Destination, Options: cfg.SSHOptions, ForwardAgent: cfg.ForwardAgent}
	if cfg.Via != "" && cfg.Via != LocalHost {
		via := a.host(cfg.Via)
		if via == nil {
			return nil, errors.Wrap(errNoOrigin, cfg.Via)
		}
		if !via.Local() {
			ssh.Via = via.client.SSH
		}
	}
	client := tmux.NewClient()
	client.SocketName = cfg.SocketName
	client.SSH = ssh
	return newHost(ssh.Name(), cfg, client), nil
}

// dependents returns the hosts reached through a host, directly or through others, in
// list order.
func (a *App) dependents(name string) []*Host {
	through := map[string]bool{name: true}
	result := []*Host{}
	// Hosts are listed after their origins, so one pass in order finds every depth.
	for _, h := range a.hosts {
		if h.Config.Via != "" && through[h.Config.Via] {
			through[h.Name] = true
			result = append(result, h)
		}
	}
	return result
}

// hostsFile is where hosts added in the UI are saved.
func hostsFile() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, version.Name, "hosts.yml")
}

// hostsDocument is the format of the hosts file.
type hostsDocument struct {
	Hosts []HostConfig `yaml:"hosts"`
}

// loadHosts reads the saved hosts. A missing file means none.
func loadHosts(path string) ([]HostConfig, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var doc hostsDocument
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, errors.Wrapf(err, "parsing %s", path)
	}
	return doc.Hosts, nil
}

// saveHosts writes the hosts file, replacing it atomically.
func saveHosts(path string, hosts []HostConfig) error {
	data, err := yaml.Marshal(hostsDocument{Hosts: hosts})
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

// runAsync runs fn on another goroutine with a command timeout and hands its result to
// then on the main loop. Remote commands go over the network, so nothing waits for them
// on the UI thread.
func runAsync[T any](a *App, fn func(ctx context.Context) (T, error), then func(T, error)) {
	go func() {
		ctx, cancel := a.commandContext()
		defer cancel()
		result, err := fn(ctx)
		glib.IdleAdd(func() { then(result, err) })
	}()
}

// pollHost reads a host's state at the poll interval, and when asked to, and hands it to
// the main loop. It stops when ctx is cancelled.
func (a *App) pollHost(ctx context.Context, h *Host) {
	ticker := time.NewTicker(a.cfg.Activity.PollInterval)
	defer ticker.Stop()
	var last time.Time
	var lastErr string

	for {
		cmdCtx, cancel := context.WithTimeout(ctx, commandTimeout)
		snap, err := h.client.Snapshot(cmdCtx)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			// Log a persistent error once, not on every poll.
			if err.Error() != lastErr {
				a.log.Warn("Could not read tmux sessions", zap.String("host", h.Name), zap.Error(err))
			}
			lastErr = err.Error()
		} else {
			lastErr = ""
		}
		glib.IdleAdd(func() { a.applySnapshot(h, snap, err) })

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-h.kick:
			if wait := minKickInterval - time.Since(last); wait > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(wait):
				}
			}
		}
		last = time.Now()
	}
}
