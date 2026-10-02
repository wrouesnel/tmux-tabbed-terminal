# tmux-tabbed-terminal

![tmux-tabbed-terminal with three sessions side by side](docs/screenshot.png)

A terminal for people who live in tmux. It looks like GNOME Terminal, but instead of tabs
it lists the tmux sessions running for you in a sidebar. Click a session to switch to it,
or split the window to watch several sessions side by side.

* **Session list.** Every session on your tmux server, in tmux's order, with what its
  current window is running. Sessions made or killed outside the app show up within a
  second.
* **Activity.** A session producing output shows a spinner in the list and in its pane
  header. A session that produced output while it wasn't on screen is shown in bold
  with a dot until you look at it. Activity comes from tmux's own `window_activity`
  time, so it works for sessions that aren't displayed.
* **Splits.** Split a pane right or down, then pick a session for the new pane from
  the list. Splits nest, and their dividers can be dragged.
* **GNOME look.** Font and colors come from GNOME Terminal's default profile if it's
  installed, otherwise from the desktop monospace font and the GTK theme.

Each pane runs an ordinary `tmux attach-session` client in a VTE terminal, so your tmux
configuration, key bindings and status line all work as usual. Switching a pane to another
session uses `tmux switch-client`, which is instant. Closing a pane or window detaches; it
never kills a session.

## Install

Packages for Ubuntu 24.04 (noble), amd64 and arm64, are published as an APT repository on
GitHub Pages:

```sh
sudo install -d -m 0755 /etc/apt/keyrings
sudo curl -fsSLo /etc/apt/keyrings/tmux-tabbed-terminal.gpg \
    https://wrouesnel.github.io/tmux-tabbed-terminal/key.gpg
sudo tee /etc/apt/sources.list.d/tmux-tabbed-terminal.sources <<EOF
Types: deb
URIs: https://wrouesnel.github.io/tmux-tabbed-terminal
Suites: noble
Components: main
Signed-By: /etc/apt/keyrings/tmux-tabbed-terminal.gpg
EOF
sudo apt update
sudo apt install tmux-tabbed-terminal
```

The `.deb` files are also attached to each
[GitHub release](https://github.com/wrouesnel/tmux-tabbed-terminal/releases).

## Usage

Start it from the desktop menu ("Tmux Tabbed Terminal") or run `tmux-tabbed-terminal`.
Running it again opens another window in the same instance. `--separate` starts an
independent instance instead.

In the session list, click a session to show it in the focused pane. Middle-click or
Ctrl+click opens it in a new pane to the right. Right-click gives Open in Split Right,
Open in Split Down, Rename and Kill. In a terminal, right-click (Shift+right-click if the
program is using the mouse) gives copy, paste and split.

| Shortcut | Action |
|---|---|
| Ctrl+Shift+T | New session in the focused pane |
| Ctrl+Page Down / Ctrl+Page Up | Next / previous session in the focused pane |
| Alt+1 … Alt+9 | Show the session at that position |
| Ctrl+Shift+E | Split right |
| Ctrl+Shift+O | Split down |
| Ctrl+Tab / Ctrl+Shift+Tab | Focus the next / previous pane |
| Ctrl+Shift+W | Close pane (the session keeps running) |
| Ctrl+Shift+R | Rename the focused pane's session |
| Ctrl+Shift+C / Ctrl+Shift+V | Copy / paste |
| Ctrl+plus / Ctrl+minus / Ctrl+0 | Zoom in / out / reset |
| Ctrl+Shift+S | Show or hide the session list |
| Ctrl+Shift+N | New window |
| Ctrl+Shift+Q | Close window |
| F11 | Full screen |

If a pane's client detaches (`prefix d`), the pane offers to reattach. If its session
ends, the pane closes, or, if it's the only pane, moves on to the most recently active
session.

## Configuration

Configuration is optional. The application reads
`~/.config/tmux-tabbed-terminal/config.yml` if it exists, or the file given with
`--config-file`. [`packaging/config.example.yml`](packaging/config.example.yml) lists every
setting with its default, and `tmux-tabbed-terminal config` prints the configuration in
effect. The commonly changed settings are:

```yaml
tmux:
  socket-name: work        # use a tmux server other than the default, as with tmux -L
appearance:
  font: "Monospace 12"
  palette: [tango]         # or a list of 16 colors
activity:
  timeout: 2s              # how long a session shows as busy after output stops
```

## Building

Building needs Go, the GTK3 and VTE development headers, and tmux for the tests. On Ubuntu:

```sh
sudo apt install libgtk-3-dev libvte-2.91-dev tmux xvfb
go run mage.go binary
./tmux-tabbed-terminal
```

| Target | Result |
|---|---|
| `go run mage.go binary` | Builds into `bin/` and symlinks the binary into the repository root. |
| `go run mage.go test` | Runs the tests. The GUI test runs under `xvfb-run` and is skipped without it. |
| `go run mage.go lint` / `style` | golangci-lint and formatting checks, as CI runs them. |
| `go run mage.go deb linux-amd64` | Builds `release/tmux-tabbed-terminal_<version>_amd64.deb`. Dependencies come from `dpkg-shlibdeps`, so build on the release you're packaging for. |
| `go run mage.go aptRepo` | Builds an APT repository in `.apt-repo/` from the `.deb` files in `release/` and `$APT_POOL_DIR`, signed with the gpg key named by `$APT_SIGNING_KEY`. |

The application uses cgo to link GTK3 and VTE, so each architecture is built on a machine
of that architecture. Cross-compiling needs `CC` and `PKG_CONFIG_LIBDIR` set for the target.

## Releases and the APT repository

Pushing a `v*` tag runs `.github/workflows/release.yml`. It does the following:

1. Runs the integration checks.
2. Builds the archive and `.deb` for amd64 on `ubuntu-24.04` and arm64 on
   `ubuntu-24.04-arm`.
3. Attaches them to a GitHub release.
4. Rebuilds the APT repository from the `.deb` files of every release and deploys it to
   GitHub Pages. Pages holds no state of its own.

The repository can be rebuilt without a release by running the workflow manually.

One-time setup:

* Under Settings → Pages, set the source to **GitHub Actions**.
* Create a signing key without a passphrase and store the private key as the
  `APT_SIGNING_KEY` secret:

  ```sh
  export GNUPGHOME=$(mktemp -d)
  gpg --batch --passphrase '' --quick-gen-key "tmux-tabbed-terminal APT repository" ed25519 sign never
  gpg --armor --export-secret-keys | gh secret set APT_SIGNING_KEY
  ```

  Keep a copy of the key somewhere safe. Users trust it through `key.gpg`, so replacing
  it breaks their `apt update`.

## Implementation

| Path | Purpose |
|---|---|
| `cmd/tmux-tabbed-terminal` | `main`. It only calls the entrypoint. |
| `pkg/entrypoints/tmux_tabbed_terminal` | Command line, logging, configuration loading, and the `run` and `config` commands. |
| `pkg/ui` | The GTK3 interface: application, windows, sidebar, pane split tree, terminal panes, appearance and menus. |
| `pkg/vte` | cgo bindings for the parts of VTE the UI uses. |
| `pkg/tmux` | Runs tmux commands and parses their format output into snapshots of sessions, windows and clients. |
| `pkg/activity` | Decides which sessions are busy or have unseen output, from successive snapshots. |
| `pkg/theme` | Color parsing and GNOME Terminal's built-in palettes. |
| `packaging/` | Desktop entry, icon, AppStream metadata and example configuration for the package. |
| `magefile.go`, `magefile_deb.go` | The build system, plus the Debian package and APT repository targets. |

A background goroutine polls tmux once per `poll-interval` with one command that lists
sessions, windows and clients. It polls sooner when a visible terminal shows output. Each
snapshot is handed to the GTK main loop, which updates the session list, the activity
indicators, and which session each pane shows (a client can change session inside tmux,
for example with `prefix s`).

Each pane's tmux client runs on a pty created by VTE but started with Go's `os/exec`, so
Go owns the process and knows its tty. `switch-client -c <tty>` then moves that client to
another session.
