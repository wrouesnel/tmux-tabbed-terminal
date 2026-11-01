// Package agenttest builds the agent for tests.
package agenttest

import (
	"bytes"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/agent"
)

// Build builds the agent for this platform, as version, into a Binaries directory, as
// packaging does for each platform. It skips the test on Windows, which the agent doesn't
// run on.
func Build(t *testing.T, version string) *agent.Binaries {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the agent runs on Unix hosts")
	}
	return BuildFor(t, version, runtime.GOOS, runtime.GOARCH)
}

// BuildFor builds the agent for a platform, as version, into a Binaries directory, for a
// host of that platform.
func BuildFor(t *testing.T, version, goos, goarch string) *agent.Binaries {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "ttt-agent")
	build := exec.Command("go", "build", "-o", bin,
		"-ldflags=-X github.com/wrouesnel/tmux-tabbed-terminal/version.Version="+version,
		"github.com/wrouesnel/tmux-tabbed-terminal/cmd/ttt-agent")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch)
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
	name := filepath.Join(dir, "ttt-agent-"+goos+"-"+goarch+".gz")
	if err := os.WriteFile(name, gz.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(bin)
	return &agent.Binaries{Dir: dir}
}
