package agent

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
	// sums are the SHA-256 checksums of the files the ID was made from, by name.
	sums map[string][sha256.Size]byte
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
		b.sums = map[string][sha256.Size]byte{}
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				b.err = err
				return
			}
			sum := sha256.Sum256(data)
			b.sums[filepath.Base(f)] = sum
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

// Markers which start lines of their own, so the client can find them after anything the
// host's shell startup files print: the start script's, saying the agent is missing and
// what platform the host is, and the agent's, saying it's ready and its version.
const (
	markerStart   = "ttt-agent-start"
	markerMissing = "ttt-agent-missing"
	// maxPreamble is the most the client reads looking for a marker.
	maxPreamble = 64 << 10
)

// startScript runs the agent, installing it first if it isn't there. Its argument is the
// agents' ID. If it's missing, the script says so and what platform the host is, and reads
// a line with the gzipped agent's size and SHA-256 checksum, then the agent itself: it
// reads exactly that much, rather than to the end of its input, which is the connection
// the agent then serves. The agent is checked against the checksum before it's put where
// it's looked for.
const startScript = `set -e
d="${XDG_CACHE_HOME:-$HOME/.cache}/tmux-tabbed-terminal/agent"
p="$d/$1/ttt-agent-$(uname -s)-$(uname -m)"
if [ ! -x "$p" ]; then
	echo "` + markerMissing + ` $(uname -s) $(uname -m)"
	read -r size sum
	mkdir -p "$d/$1"
	chmod 700 "$d"
	t="$p.$$.tmp"
	trap 'rm -f "$t"' EXIT
	head -c "$size" | gzip -dc > "$t"
	got=$( (sha256sum "$t" 2>/dev/null || shasum -a 256 "$t") | cut -d ' ' -f 1)
	if [ "$got" != "$sum" ]; then
		echo "the agent's checksum is $got, not $sum" >&2
		exit 1
	fi
	chmod 700 "$t"
	mv -f "$t" "$p"
	trap - EXIT
	# Agents of other versions, unused for a month, are removed.
	find "$d" -mindepth 1 -maxdepth 1 ! -name "$1" -mtime +30 -exec rm -rf {} + 2>/dev/null || true
fi
exec "$p" serve`

// Connect starts the agent on a host through launch, installing it first if it isn't
// there, and returns a client talking to it. It takes one command on the host either way.
// version is the application's version, which the agent's must match. It returns an error
// wrapping ErrUnsupported if there's no agent for the host. The agent outlives ctx, which
// only limits how long it has to start.
func Connect(ctx context.Context, launch Launcher, binaries *Binaries, version string) (*Client, error) {
	id, err := binaries.ID(version)
	if err != nil {
		return nil, err
	}
	cmd := launch(context.WithoutCancel(ctx), []string{"sh", "-c", startScript, "sh", id})
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &lockedWriter{w: &limitedBuffer{max: maxStderr}}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	proc := &process{cmd: cmd, stdin: stdin}
	r := bufio.NewReaderSize(stdout, maxPreamble)
	// Ending the command ends a read or write waiting on it.
	stop := context.AfterFunc(ctx, func() { _ = proc.Close() })
	err = handshake(r, stdin, binaries, version)
	if !stop() {
		_ = proc.Close()
		return nil, errors.Wrapf(ctx.Err(), "starting the agent: %s", strings.TrimSpace(stderr.String()))
	}
	if err != nil {
		_ = proc.Close()
		if errors.Is(err, ErrUnsupported) {
			return nil, err
		}
		return nil, errors.Wrapf(err, "starting the agent: %s", strings.TrimSpace(stderr.String()))
	}
	c, err := NewClient(r, stdin, proc)
	if err != nil {
		_ = proc.Close()
		return nil, err
	}
	return c, nil
}

// handshake reads the start script's output until the agent says it's ready, sending it
// the agent if it says it's missing, and checks the agent's version.
func handshake(r *bufio.Reader, w io.Writer, binaries *Binaries, version string) error {
	installed := false
	for {
		line, err := readMarker(r)
		if err != nil {
			return err
		}
		fields := strings.Fields(line)
		if fields[0] == markerStart {
			if len(fields) != 2 || fields[1] != version { //nolint:mnd // the marker and the version
				return errors.Errorf("the agent is %q, not version %s", line, version)
			}
			return nil
		}
		if installed {
			return errors.New("the agent was installed but isn't there")
		}
		if len(fields) != 3 { //nolint:mnd // the marker and two names
			return errors.Errorf("unexpected platform %q", line)
		}
		if err := install(w, binaries, fields[1], fields[2]); err != nil {
			return err
		}
		installed = true
	}
}

// readMarker reads lines until one of the markers, and returns it.
func readMarker(r *bufio.Reader) (string, error) {
	read := 0
	for read < maxPreamble {
		line, err := r.ReadString('\n')
		read += len(line)
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, markerStart+" ") || strings.HasPrefix(line, markerMissing+" ") {
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

// install sends the start script the agent for a host's platform, given as uname -s and
// uname -m: its size and checksum, then the agent.
func install(w io.Writer, binaries *Binaries, system, machine string) error {
	goos, goarch, ok := goPlatform(system, machine)
	if !ok {
		return errors.Wrapf(ErrUnsupported, "%s %s", system, machine)
	}
	name := binaryName(goos, goarch)
	want, ok := binaries.sums[name]
	if !ok {
		return errors.Wrapf(ErrUnsupported, "%s/%s", goos, goarch)
	}
	gz, err := os.ReadFile(filepath.Join(binaries.Dir, name))
	if err != nil {
		return err
	}
	// An upgrade may have replaced the agents since the ID was made from them: installing
	// another version's agent under this ID would leave the host with one this application
	// can't talk to.
	if sha256.Sum256(gz) != want {
		return errors.Errorf("the agents in %s have changed since the application started: restart it", binaries.Dir)
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return errors.Wrap(err, "reading the agent")
	}
	h := sha256.New()
	if _, err := io.Copy(h, zr); err != nil { //nolint:gosec // our own build output
		return errors.Wrap(err, "reading the agent")
	}
	if _, err := fmt.Fprintf(w, "%d %s\n", len(gz), hex.EncodeToString(h.Sum(nil))); err != nil {
		return errors.Wrap(err, "installing the agent")
	}
	if _, err := w.Write(gz); err != nil {
		return errors.Wrap(err, "installing the agent")
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
