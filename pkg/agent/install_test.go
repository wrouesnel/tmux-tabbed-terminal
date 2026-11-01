package agent

import (
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/pkg/errors"
)

const testVersion = "v9.9.9-test"

// buildAgent builds the agent for this platform into a Binaries directory, as packaging
// does for each platform.
func buildAgent(t *testing.T) *Binaries {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the agent runs on Unix hosts")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "ttt-agent")
	build := exec.Command("go", "build", "-o", bin,
		"-ldflags=-X github.com/wrouesnel/tmux-tabbed-terminal/version.Version="+testVersion,
		"github.com/wrouesnel/tmux-tabbed-terminal/cmd/ttt-agent")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the agent: %v\n%s", err, out)
	}
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(data)
	_ = zw.Close()
	if err := os.WriteFile(filepath.Join(dir, binaryName(runtime.GOOS, runtime.GOARCH)), gz.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(bin)
	return &Binaries{Dir: dir}
}

// localLauncher runs the remote command here, with a home of its own, through a shell
// which prints junk first, as some shell startup files do. It counts the commands run.
func localLauncher(t *testing.T, launches *atomic.Int32) Launcher {
	home := t.TempDir()
	return func(ctx context.Context, remote []string) *exec.Cmd {
		launches.Add(1)
		cmd := exec.CommandContext(ctx, "sh", append([]string{"-c", `echo "Welcome!"; exec "$@"`, "sh"}, remote...)...)
		cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CACHE_HOME=")
		return cmd
	}
}

func TestConnect(t *testing.T) {
	binaries := buildAgent(t)
	var launches atomic.Int32
	launch := localLauncher(t, &launches)
	ctx := context.Background()

	c, err := Connect(ctx, launch, binaries, testVersion)
	if err != nil {
		t.Fatal(err)
	}
	if n := launches.Load(); n != 3 { //nolint:mnd // start, install, start
		t.Errorf("the first connection took %d commands", n)
	}
	res, err := c.Run(ctx, []string{"echo", "hi"}, nil)
	if err != nil || string(res.Stdout) != "hi\n" {
		t.Errorf("run: %+v, %v", res, err)
	}
	_ = c.Close()

	// Installed: one command.
	launches.Store(0)
	c, err = Connect(ctx, launch, binaries, testVersion)
	if err != nil {
		t.Fatal(err)
	}
	if n := launches.Load(); n != 1 {
		t.Errorf("connecting again took %d commands", n)
	}
	_ = c.Close()

	// The agent must be the application's version.
	if _, err := Connect(ctx, launch, binaries, "v0.0.1"); err == nil {
		t.Error("connected to an agent of another version")
	}
}

func TestConnectUnsupported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the agent runs on Unix hosts")
	}
	// Agents for another platform only.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, binaryName("plan9", "mips")), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var launches atomic.Int32
	_, err := Connect(context.Background(), localLauncher(t, &launches), &Binaries{Dir: dir}, testVersion)
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("err %v", err)
	}
}

func TestGoPlatform(t *testing.T) {
	for in, want := range map[[2]string][2]string{
		{"Linux", "x86_64"}: {"linux", "amd64"}, {"Linux", "aarch64"}: {"linux", "arm64"},
		{"Darwin", "arm64"}: {"darwin", "arm64"}, {"Linux", "armv7l"}: {}, {"SunOS", "i86pc"}: {},
	} {
		goos, goarch, ok := goPlatform(in[0], in[1])
		if ok != (want != [2]string{}) || (ok && [2]string{goos, goarch} != want) {
			t.Errorf("%v: %s %s %v", in, goos, goarch, ok)
		}
	}
}
