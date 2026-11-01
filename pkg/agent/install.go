package agent

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
)

// Launcher makes the command which runs a program on a host, such as ssh with the program
// as its remote command. The program reads the command's standard input and writes its
// standard output.
type Launcher func(ctx context.Context, remote []string) *exec.Cmd

// Binaries are the agents the application can install on hosts: files in a directory named
// ttt-agent-GOOS-GOARCH.gz, gzipped.
type Binaries struct {
	Dir string

	once sync.Once
	id   string
	err  error
}

// ErrUnsupported is the error of a host there's no agent for, such as one whose platform
// none of the binaries is built for.
var ErrUnsupported = errors.New("no agent for the host's platform")

// binaryName is the file of the agent for a platform, in a Binaries directory.
func binaryName(goos, goarch string) string {
	return "ttt-agent-" + goos + "-" + goarch + ".gz"
}

// ID names this set of agents on a host: the version, and a hash of the binaries, so
// different builds with the same version, as in development, don't share one.
func (b *Binaries) ID(version string) (string, error) {
	b.once.Do(func() {
		files, err := filepath.Glob(filepath.Join(b.Dir, binaryName("*", "*")))
		if err != nil || len(files) == 0 {
			b.err = errors.Wrapf(ErrUnsupported, "no agents in %s", b.Dir)
			return
		}
		sort.Strings(files)
		h := sha256.New()
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				b.err = err
				return
			}
			sum := sha256.Sum256(data)
			h.Write([]byte(filepath.Base(f)))
			h.Write(sum[:])
		}
		b.id = hex.EncodeToString(h.Sum(nil)[:6])
	})
	if b.err != nil {
		return "", b.err
	}
	return safeName(version) + "-" + b.id, nil
}

// safeName keeps a version to letters, digits, dots, dashes and underscores, for a
// directory name.
func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		}
		return '_'
	}, s)
}

// goPlatform turns uname -s and uname -m into Go's names for a platform.
func goPlatform(system, machine string) (string, string, bool) {
	goos := map[string]string{"Linux": "linux", "Darwin": "darwin", "FreeBSD": "freebsd"}[system]
	goarch := map[string]string{
		"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64",
	}[machine]
	return goos, goarch, goos != "" && goarch != ""
}

// Markers the start script writes on a line of its own, so the client can find them after
// anything the host's shell startup files print.
const (
	markerStart   = "ttt-agent-start"
	markerMissing = "ttt-agent-missing"
	// maxPreamble is the most the client reads looking for a marker.
	maxPreamble = 64 << 10
)

// startScript runs the agent if it's installed, saying so first, or says it's missing and
// what platform the host is. Its arguments are the agents' ID.
const startScript = `p="${XDG_CACHE_HOME:-$HOME/.cache}/tmux-tabbed-terminal/agent/$1/ttt-agent-$(uname -s)-$(uname -m)"
if [ -x "$p" ]; then
	echo ` + markerStart + `
	exec "$p" serve
fi
echo "` + markerMissing + ` $(uname -s) $(uname -m)"`

// installScript writes the gzipped agent on its standard input to where startScript looks
// for it, checking it against a SHA-256 checksum. Its arguments are the agents' ID and the
// checksum.
const installScript = `set -e
d="${XDG_CACHE_HOME:-$HOME/.cache}/tmux-tabbed-terminal/agent"
p="$d/$1/ttt-agent-$(uname -s)-$(uname -m)"
mkdir -p "$d/$1"
chmod 700 "$d"
t="$p.$$.tmp"
trap 'rm -f "$t"' EXIT
gzip -dc > "$t"
sum=$( (sha256sum "$t" 2>/dev/null || shasum -a 256 "$t") | cut -d ' ' -f 1)
if [ "$sum" != "$2" ]; then
	echo "the agent's checksum is $sum, not $2" >&2
	exit 1
fi
chmod 700 "$t"
mv -f "$t" "$p"
# Agents of other versions, unused for a month, are removed.
find "$d" -mindepth 1 -maxdepth 1 ! -name "$1" -mtime +30 -exec rm -rf {} + 2>/dev/null || true`

// Connect starts the agent on a host through launch, installing it first if it isn't
// there, and returns a client talking to it. version is the application's version, which
// the agent's must match. It returns an error wrapping ErrUnsupported if there's no agent
// for the host.
func Connect(ctx context.Context, launch Launcher, binaries *Binaries, version string) (*Client, error) {
	id, err := binaries.ID(version)
	if err != nil {
		return nil, err
	}
	c, platform, err := start(ctx, launch, id)
	if err != nil {
		return nil, err
	}
	if c == nil {
		if err := install(ctx, launch, binaries, id, platform); err != nil {
			return nil, err
		}
		if c, _, err = start(ctx, launch, id); err != nil {
			return nil, err
		}
		if c == nil {
			return nil, errors.New("the agent was installed but isn't there")
		}
	}
	hello, err := c.Hello(ctx, version)
	if err != nil {
		_ = c.Close()
		return nil, errors.Wrap(err, "greeting the agent")
	}
	if hello.GetVersion() != version {
		_ = c.Close()
		return nil, errors.Errorf("the agent is version %s, not %s", hello.GetVersion(), version)
	}
	return c, nil
}

// start runs startScript. If the agent started, it returns a client talking to it; if it's
// missing, it returns no client and the host's uname -s and uname -m.
func start(ctx context.Context, launch Launcher, id string) (*Client, [2]string, error) {
	cmd := launch(ctx, []string{"sh", "-c", startScript, "sh", id})
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, [2]string{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, [2]string{}, err
	}
	stderr := &lockedWriter{w: &limitedBuffer{max: maxStderr}}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, [2]string{}, err
	}
	proc := &process{cmd: cmd, stdin: stdin}
	r := bufio.NewReaderSize(stdout, maxPreamble)
	line, err := readMarker(r)
	if err != nil {
		_ = proc.Close()
		return nil, [2]string{}, errors.Wrapf(err, "starting the agent: %s", strings.TrimSpace(stderr.String()))
	}
	if fields := strings.Fields(line); fields[0] == markerMissing {
		_ = proc.Close()
		if len(fields) != 3 { //nolint:mnd // the marker and two names
			return nil, [2]string{}, errors.Errorf("unexpected platform %q", line)
		}
		return nil, [2]string{fields[1], fields[2]}, nil
	}
	c, err := NewClient(r, stdin, proc)
	if err != nil {
		_ = proc.Close()
		return nil, [2]string{}, err
	}
	return c, [2]string{}, nil
}

// readMarker reads lines until one of the start script's markers, and returns it.
func readMarker(r *bufio.Reader) (string, error) {
	read := 0
	for read < maxPreamble {
		line, err := r.ReadString('\n')
		read += len(line)
		line = strings.TrimSpace(line)
		if line == markerStart || strings.HasPrefix(line, markerMissing+" ") {
			return line, nil
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", errors.New("the host closed the connection")
			}
			return "", err
		}
	}
	return "", errors.New("the host's shell printed too much before the agent")
}

// install uploads the agent for a host's platform, given as uname -s and uname -m.
func install(ctx context.Context, launch Launcher, binaries *Binaries, id string, platform [2]string) error {
	goos, goarch, ok := goPlatform(platform[0], platform[1])
	if !ok {
		return errors.Wrapf(ErrUnsupported, "%s %s", platform[0], platform[1])
	}
	gz, err := os.ReadFile(filepath.Join(binaries.Dir, binaryName(goos, goarch)))
	if errors.Is(err, os.ErrNotExist) {
		return errors.Wrapf(ErrUnsupported, "%s/%s", goos, goarch)
	}
	if err != nil {
		return err
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return errors.Wrap(err, "reading the agent")
	}
	h := sha256.New()
	if _, err := io.Copy(h, zr); err != nil { //nolint:gosec // our own build output
		return errors.Wrap(err, "reading the agent")
	}
	cmd := launch(ctx, []string{"sh", "-c", installScript, "sh", id, hex.EncodeToString(h.Sum(nil))})
	cmd.Stdin = bytes.NewReader(gz)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return errors.Wrapf(err, "installing the agent: %s", strings.TrimSpace(stderr.String()))
	}
	return nil
}

// process is a running launch command, closed by closing its standard input, which ends
// the agent, and then waiting for it, or killing it if it doesn't end.
type process struct {
	cmd   *exec.Cmd
	stdin io.Closer
	once  sync.Once
}

// processExitWait is how long a closed process has to end before it's killed.
const processExitWait = 5 * time.Second

func (p *process) Close() error {
	p.once.Do(func() {
		_ = p.stdin.Close()
		done := make(chan struct{})
		go func() {
			_ = p.cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(processExitWait):
			_ = p.cmd.Process.Kill()
			<-done
		}
	})
	return nil
}

// lockedWriter serializes writes to w, which exec may make from its own goroutine while
// the caller reads w.
type lockedWriter struct {
	mu sync.Mutex
	w  *limitedBuffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func (l *lockedWriter) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.String()
}
