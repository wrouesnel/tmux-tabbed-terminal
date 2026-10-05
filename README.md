# tmux-tabbed-terminal

![tmux-tabbed-terminal showing pinned sessions and sessions grouped by application](docs/screenshot.png)

A terminal for people who live in tmux. It looks like GNOME Terminal, but instead of tabs
it lists the tmux sessions running for you in a sidebar. Click a session to switch to it,
or split the window to watch several sessions side by side.

* **Session list.** Every session on your tmux server, in tmux's order, with what its
  current window is running. Sessions made or killed outside the app show up within a
  second.
* **Tab bar.** A strip of tabs above the terminals holds the sessions in the list's
  current view, in the same order, so you can click between them with the list hidden.
  Searching or filtering the list narrows the tabs too. The tab underlined in the accent
  color is the focused pane's session, and fainter underlines mark sessions shown in
  other panes. Tabs show the same activity dots as the list, scroll sideways with the
  wheel when they don't fit, and can be dragged onto panes like list entries. Hide it with
  Show Tab Bar in the menu; the choice is remembered.
* **Other hosts.** "Add Host" under the list connects to another machine over ssh and
  lists its tmux sessions under its own heading, below this machine's ("local"). Remote
  sessions open in panes like local ones.
* **Saving scrollback.** Right-click a session, or a terminal, and choose Save Scrollback
  to save the whole tmux history of its current pane as text, under
  `~/.local/share/tmux-tabbed-terminal/scrollback/<host>/<session>/<date-time>.txt`, or
  Save Scrollback As… to choose where.
* **Pinned sessions.** Right-click a session and choose Pin to list it at the top, in a
  collapsible Pinned section, as well as in its usual place. Pins are kept by host and
  session name, so they survive tmux restarts. A pinned session that isn't running is
  greyed out, and clicking it starts it again under that name. The button on the Pinned
  heading hides the ones that aren't running.
* **Grouping and search.** Sessions are grouped by the program running in their current
  window, so all your `claude` sessions sit together (toggle it under the list). The
  search box at the top filters by session name, program or window name.
* **Activity.** A session producing output shows a gently pulsing dot in the list and in
  its pane header. A session that produced output while it wasn't on screen is shown in bold
  with a dot until you look at it. Activity comes from tmux's own `window_activity`
  time, so it works for sessions that aren't displayed.
* **Splits.** Split a pane right or down, then pick a session for the new pane from
  the list. Or drag a session from the list onto a pane, as in VS Code: drop it near an
  edge to split that side, or in the middle to show it in that pane. Drag it out of the
  window to open it in a window of its own, with the session list hidden (on X11; Wayland
  doesn't tell applications where the pointer is outside their windows). Splits nest, and
  their dividers can be dragged.
* **Scrolling.** The mouse wheel scrolls tmux's history, speeding up the faster the wheel
  spins. A scrollbar beside each terminal shows where you are in tmux's history (not
  the terminal's, which tmux redraws in place) and scrolls it when dragged. Both work
  whether or not tmux's `mouse` option is on. Programs that use the mouse
  or the full screen, such as vim, less and htop, get the wheel as usual.
* **SIXEL images** show when the system's VTE was built with SIXEL support, and tmux
  passes them through (tmux 3.4 or newer, built with it). VTE's SIXEL support is a build
  option which Ubuntu 24.04 and 26.04 and RHEL 8 and 10 leave off, so on those it's
  unavailable.
* **GNOME look.** Font and colors come from GNOME Terminal's default profile if it's
  installed, otherwise from the desktop monospace font and the GTK theme. Preferences
  (Ctrl+comma) picks another GNOME Terminal profile, the GTK theme, one of GNOME
  Terminal's built-in schemes or custom colors, and the font. The profile in use is
  followed live as it's edited in GNOME Terminal, and the session list's colors are
  derived from the terminal's.

Each pane runs an ordinary `tmux attach-session` client in a VTE terminal, so your tmux
configuration, key bindings and status line all work as usual. Switching a pane to another
session uses `tmux switch-client`, which is instant. Closing a pane or window detaches; it
never kills a session.

## Install

Packages are built from source by Launchpad and COPR, for amd64 and arm64.

### Ubuntu 24.04 and 26.04

From the PPA [ppa:w-rouesnel/tmux-tabbed-terminal](https://launchpad.net/~w-rouesnel/+archive/ubuntu/tmux-tabbed-terminal):

```sh
sudo add-apt-repository ppa:w-rouesnel/tmux-tabbed-terminal
sudo apt install tmux-tabbed-terminal
```

### RHEL 8 and 10

From COPR, [wrouesnel/tmux-tabbed-terminal](https://copr.fedorainfracloud.org/coprs/wrouesnel/tmux-tabbed-terminal/),
for RHEL and its rebuilds such as Rocky Linux and AlmaLinux:

```sh
sudo dnf install dnf-plugins-core
sudo dnf copr enable wrouesnel/tmux-tabbed-terminal
sudo dnf install tmux-tabbed-terminal
```

RHEL 8's tmux is 2.7, which works. RHEL 10's tmux is a pre-release snapshot (it reports
`next-3.4`) whose `capture-pane` corrupts its memory and kills the server, so Save
Scrollback refuses to run against it rather than end every session; everything else
works.

### From a release

Each [GitHub release](https://github.com/wrouesnel/tmux-tabbed-terminal/releases) has the
source, as a tarball with the Go modules vendored, and binary archives for amd64 and
arm64, each with a SHA-256 checksum. The binaries link GTK3 and VTE dynamically and are
built on Ubuntu 24.04.

## Usage

Start it from the desktop menu ("Tmux Tabbed Terminal") or run `tmux-tabbed-terminal`.
Running it again opens another window in the same instance. `--separate` starts an
independent instance instead.

In the session list or the tab bar, click a session to show it in the focused pane, or
drag it onto a pane. Middle-click or Ctrl+click opens it in a new pane to the right. Right-click gives Open in Split Right,
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
| Ctrl+Shift+F | Search sessions (Enter opens the first match, Escape returns to the terminal) |
| Ctrl+Shift+N | New window |
| Ctrl+comma | Preferences |
| Ctrl+Shift+Q | Close window |
| F11 | Full screen |

If a pane's client detaches (`prefix d`), the pane offers to reattach. If its session
ends, the pane closes, or, if it's the only pane, moves on to the most recently active
session.

## Configuration

Choices made in the UI, such as which side the session list is on, whether it's grouped,
pinned sessions and Preferences, are remembered in `~/.local/state/tmux-tabbed-terminal/state.yml`.

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

## Remote hosts

"Add Host" takes an ssh destination: `user@host`, or an alias from `~/.ssh/config`, which
is the place for ports, jump hosts and keys. The dialog lists the concrete hosts of
`~/.ssh/config` and the files it includes (wildcard patterns are left out); typing
filters the list, a click fills in the host and a double click adds it. The host needs tmux installed, and key or
agent authentication: the session list is polled in the background with ssh's
`BatchMode`, so it can't prompt for a password. If ssh has never seen the host's key,
connect to it once by hand to accept it.

All commands to a host share one ssh master connection (`ControlMaster`), started on first
use and kept for 10 minutes after the last. Its socket is under
`$XDG_RUNTIME_DIR/tmux-tabbed-terminal/`. Hosts added in the UI are saved to
`~/.config/tmux-tabbed-terminal/hosts.yml`, and the configuration file's `hosts:` key can
list more:

```yaml
hosts:
  - destination: buildbox
  - destination: admin@db.example.com
    ssh-options: ["-p", "2222"]
    socket-name: work      # a tmux server other than the default, as with tmux -L
```

Add Host's Origin picks where the host is reached from: this computer, or any listed host,
typed or picked from the dropdown. A host reached through an origin runs its ssh *on the
origin*, with the origin's `~/.ssh/config` and keys, and the dialog lists that host's
configured hosts. Hosts can be chained to any depth (`db via bastion via vpn-gw`); each
hop keeps its own ssh master connection on the host it runs from (in `~/.ssh/ttt-*`
there). If the origin has no key of its own for the host, tick "Forward my ssh agent
through the origin"; it's off by default because anyone with root on the origin can use
a forwarded agent. Removing a host also removes the hosts reached through it, after
listing them. A tunneled host in the configuration file names its origin with `via:`:

```yaml
hosts:
  - destination: bastion
  - destination: db        # an alias in bastion's ~/.ssh/config
    via: bastion
    forward-agent: true
```

With more than one host, New Session asks which host to start it on. The "+" beside a
host starts one there directly, and its ⋯ menu also removes the host. Removing a host leaves
panes already attached to its sessions running.

## Building

Building needs Go 1.24.1 or newer, the GTK3 and VTE development headers, and tmux for the
tests. On Ubuntu:

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
| `go run mage.go source` | Builds the vendored source tarball and its checksum in `release/source/`. |
| `go run mage.go srpm` | Builds the source RPM for COPR in `release/srpm/`. `rpmbuild --rebuild` it on RHEL, after `dnf builddep`, to build the package. |
| `go run mage.go debSource noble` | Builds the source package for an Ubuntu suite (`noble`, `resolute`) in `release/ppa/<suite>/`; `DEB_SIGNING_KEY` signs it. `dpkg-buildpackage -b` it to build the package. |

The application uses cgo to link GTK3 and VTE, so each architecture is built on a machine
of that architecture. Cross-compiling needs `CC` and `PKG_CONFIG_LIBDIR` set for the target.

## Releases and packages

Pushing a `v*` tag runs `.github/workflows/release.yml`. It does the following:

1. Runs the integration checks, which build every package as its repository will.
2. Builds the binary archives for amd64 on `ubuntu-24.04` and arm64 on `ubuntu-24.04-arm`,
   and the vendored source tarball (`go run mage.go source`), and makes a GitHub release
   of them with their checksums. Releases carry only code: no packages.
3. Builds signed source packages for Ubuntu 24.04 (noble) and 26.04 (resolute) with
   `go run mage.go debSource <suite>` and uploads them to the PPA, where Launchpad builds
   them.
4. Builds the source RPM (`go run mage.go srpm`) and submits it to COPR, which builds,
   signs and publishes the RHEL 8 and 10 packages.

Both build services build without network access, so the source packages carry the Go
modules vendored, and use each release's packaged Go: Ubuntu 24.04's `golang-1.24-go`
(from noble-updates), and the default Go of Ubuntu 26.04 and RHEL 8 and 10, which are
newer. That's why `go.mod` targets Go 1.24.1 and dependencies are held to versions that
build with it. Older GTK libraries, as on RHEL 8, are
detected by `packaging/gotk3-tags.sh` and selected in gotk3 with build tags.

CI checks every package builds that way on each push and pull request: the source RPM is
rebuilt in Rocky Linux 8 and AlmaLinux 10 containers with `dnf builddep`, and both PPA
packages in Ubuntu containers from their vendored source.

One-time setup:

* PPA uploads are signed with the shared Launchpad key
  `2A12 8435 A6FE 8BD7 51AA  5787 2095 9AB8 0709 6ADB` ("Will Rouesnel (GPG key for
  launchpad signing)"), which lives in the maintainer's personal keyring with its
  passphrase in the login keyring. COPR signs the RPMs with its own key, so it needs none.
* Give the release workflow the key. This is the one step where the private key leaves
  the keyring, so it's one you run deliberately: the workflow signs the PPA uploads on
  GitHub's runners, which can't reach your keyring. GitHub keeps secrets encrypted and
  only hands them to workflow runs of this repository.

  ```sh
  FPR=2A128435A6FE8BD751AA578720959AB807096ADB
  secret-tool lookup service gpg-passphrase fingerprint "$FPR" |
      gpg --batch --pinentry-mode loopback --passphrase-fd 0 --armor --export-secret-keys "$FPR" |
      gh secret set PACKAGE_SIGNING_KEY
  secret-tool lookup service gpg-passphrase fingerprint "$FPR" | gh secret set PACKAGE_SIGNING_KEY_PASSPHRASE
  gh variable set PACKAGE_SIGNING_KEY_FINGERPRINT --body "$FPR"
  ```

  Then run the **Package signing check** workflow (Actions → Package signing check → Run
  workflow). It imports the key as the release does, signs both source packages and checks
  the signatures, without uploading anything.
* On Launchpad: create the PPA `tmux-tabbed-terminal`
  (https://launchpad.net/~w-rouesnel/+activate-ppa); the shared key is registered, and must
  be active, on the account (https://launchpad.net/~w-rouesnel/+editpgpkeys). Then, in the
  PPA's settings, enable arm64 under Change details. The workflow uploads to
  `ppa:w-rouesnel/tmux-tabbed-terminal`, or to the PPA named by the `PPA` repository
  variable.
* On COPR: create the project `tmux-tabbed-terminal`
  (https://copr.fedorainfracloud.org/coprs/add/) with the chroots `epel-8-x86_64`,
  `epel-8-aarch64`, `epel-10-x86_64` and `epel-10-aarch64`. Then give the workflow an API
  token, from https://copr.fedorainfracloud.org/api/, and name the project:

  ```sh
  gh secret set COPR_CONFIG < ~/.config/copr
  gh variable set COPR_PROJECT --body wrouesnel/tmux-tabbed-terminal
  ```

  COPR signs the packages it builds with its own key for the project.

## License

MIT: see [LICENSE](LICENSE). The binary links
[github.com/yuseferi/zax](https://github.com/yuseferi/zax), which is GPL-3.0, through
`go.logutil`, so binary packages are distributed under the GPL-3.0 as well.

## Implementation

| Path | Purpose |
|---|---|
| `cmd/tmux-tabbed-terminal` | `main`. It only calls the entrypoint. |
| `pkg/entrypoints/tmux_tabbed_terminal` | Command line, logging, configuration loading, and the `run` and `config` commands. |
| `pkg/ui` | The GTK3 interface: application, windows, sidebar, pane split tree, terminal panes, appearance and menus. |
| `pkg/vte` | cgo bindings for the parts of VTE the UI uses. |
| `pkg/tmux` | Runs tmux commands, locally or over ssh, and parses their format output into snapshots of sessions, windows and clients. |
| `pkg/gtkx` | cgo bindings for the few GTK functions gotk3 lacks (drag and drop). |
| `pkg/sshconfig` | Lists the concrete hosts of an OpenSSH client configuration, following `Include`. |
| `pkg/sessionlist` | Groups sessions by application, orders and filters the session list. |
| `pkg/activity` | Decides which sessions are busy or have unseen output, from successive snapshots. |
| `pkg/theme` | Color parsing and GNOME Terminal's built-in palettes. |
| `packaging/` | Desktop entry, icon, AppStream metadata, example configuration, the RPM spec, the Debian packaging and the gotk3 build tag detection. |
| `magefile.go`, `magefile_pkg.go` | The build system, plus the source tarball, source RPM and Debian source package targets. |

Each host has a background goroutine which polls its tmux server once per `poll-interval` with one command that lists
sessions, windows and clients. It polls sooner when a visible terminal shows output. Each
snapshot is handed to the GTK main loop, which updates the session list, the activity
indicators, and which session each pane shows (a client can change session inside tmux,
for example with `prefix s`).

Each pane's tmux client runs on a pty created by VTE but started with Go's `os/exec`, so
Go owns the process and knows its tty. `switch-client -c <tty>` then moves that client to
another session.
