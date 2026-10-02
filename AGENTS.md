Use README.md files to understand program intent and structure.

Binary outputs must build via `go run mage.go binary`.

Build without CGO, with one exception: this application links GTK3 and VTE through
cgo (gotk3 and `pkg/vte`), so `CGO_ENABLED=1` and binaries are dynamically linked. Keep
cgo confined to `pkg/vte`; use gotk3 for everything else in GTK.

GTK may only be used from the main thread. `pkg/ui` locks it in `init`. Work on other
goroutines (tmux polling, waiting for child processes) hands results back with
`glib.IdleAdd` and never touches widgets directly.

Use the [afero package](https://github.com/spf13/afero) to abstract filesystem
access, ideally via the [pathlib package](https://github.com/chigopher/pathlib)
except in application startup entrypoints where no configuration has been loaded
yet.

If a web application is being developed then implement all endpoints using OpenAPI 3
and use `oapi-codegen` to build Echo v5 (`go get github.com/labstack/echo/v5`) based
servers.

Use the [zap logger](https://go.uber.org/zap) for logging and favor using the
[logutil package](https://github.com/wrouesnel/go.logutil). Any function taking
a `context.Context` should use `logutil.FromCtx` to get a context-aware logger.

Implement all binary applications as exportable packages under `pkg/entrypoints/<binary name>`
and keep code under `cmd/` to an absolute minimum. Replace `-` with `_` in the binary name to
get the package name, e.g. `cmd/foo-bar` is implemented by `pkg/entrypoints/foo_bar`. Each new
binary also needs an entry in `.gitignore` for the symlink `go run mage.go binary` creates.

Test entrypoints by calling `Entrypoint(ctx, args)` directly, as in
`pkg/entrypoints/tmux_tabbed_terminal/entrypoint_test.go`.

## Build commands

Run all build commands from the repository root. `go run mage.go -l` lists every target.

* `go run mage.go binary` - build for the current platform into `bin/` and symlink it into
  the repository root.
* `go run mage.go test` - run the tests. `go run mage.go coverage` merges coverage into
  `.cover.out` afterwards.
* `go run mage.go lint` - run golangci-lint (configured by `.golangci.yml`).
* `go run mage.go style` - check formatting. `go run mage.go fmt` fixes it.

CI runs `style`, `lint`, `test` and `binary`, all of which must pass.

## Web interface

A web interface is optional. If `web/package.json` exists, the build installs the Node.js
version in `.nvmrc` and builds `web/` into `web/dist` for embedding. Otherwise the web build
is skipped. Set `SKIP_WEB=1` to skip it regardless.

## Packaging

Packages target Ubuntu noble. `go run mage.go deb <os-arch>` builds the `.deb` from
`packaging/`, and takes its library dependencies from `dpkg-shlibdeps`, so it must run on
noble for the architecture it packages. `go run mage.go aptRepo` builds the GitHub Pages
APT repository. See the README for the release workflow.

## Testing the GUI

`pkg/entrypoints/tmux_tabbed_terminal/gui_test.go` re-runs the test binary under
`xvfb-run` against a private tmux server (`tmux -L`). Never point tests or manual runs at
the user's default tmux server; use `socket-name` in a scratch config file.
