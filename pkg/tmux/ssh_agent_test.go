package tmux_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/agent/agenttest"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

// countingSSH writes a fake ssh which counts its runs, one line each, in a log file.
func countingSSH(t *testing.T) (string, func() int) {
	t.Helper()
	fake, err := filepath.Abs("testdata/fake-ssh")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "runs")
	script := filepath.Join(dir, "ssh")
	body := "#!/bin/sh\necho run >> '" + log + "'\necho \"$*\" >> '" + log + ".args'\nexec '" + fake + "' \"$@\"\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil { //nolint:gosec // an executable script
		t.Fatal(err)
	}
	return script, func() int {
		data, _ := os.ReadFile(log)
		return strings.Count(string(data), "run\n")
	}
}

// TestRemoteThroughAgent runs a session's lifecycle, a file copy, and commands to a host
// three hops away, through the agent on the first host, as on Windows: without connection
// sharing, everything goes over the agent's one ssh connection.
func TestRemoteThroughAgent(t *testing.T) {
	const version = "v9.9.9-agent-test"
	binaries := agenttest.Build(t, version)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	ctx := context.Background()
	local := newTestClient(t)
	fake, runs := countingSSH(t)
	ssh := &tmux.SSH{
		Destination: "test-host", Binary: fake, NoConnectionSharing: true,
		Agents: binaries, AgentVersion: version,
	}
	remote := &tmux.Client{Binary: "tmux", SocketName: local.SocketName, SSH: ssh}

	id, err := remote.NewSession(ctx, "it's remote", "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	snap, err := remote.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if s := snap.Session(id); s == nil || s.Name != "it's remote" {
		t.Fatalf("session through the agent: %+v", snap)
	}
	if _, err := ssh.Output(ctx, "sh", "-c", "exit 3"); err == nil {
		t.Error("a failing command succeeded")
	}

	content := []byte("binary\x00\xff and text\n")
	name := filepath.Join(t.TempDir(), "it's a $file.bin")
	if err := os.WriteFile(name, content, 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	size := int64(-1)
	if err := ssh.CopyFileSized(ctx, &out, name, func(n int64) { size = n }); err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	if !bytes.Equal(out.Bytes(), content) || size != int64(len(content)) {
		t.Errorf("copied %q, size %d", out.Bytes(), size)
	}
	if err := ssh.CopyFile(ctx, &out, filepath.Dir(name)); err == nil || !strings.Contains(err.Error(), "isn't a file") {
		t.Errorf("copying a directory: %v", err)
	}

	// Hosts beyond run their ssh on the first host, through its agent.
	second := &tmux.SSH{Destination: "inner", Binary: fake, Via: ssh}
	third := &tmux.SSH{Destination: "db", Binary: fake, Via: second}
	got, err := third.Output(ctx, "echo", "a b", "$HOME")
	if err != nil || got != "a b $HOME\n" {
		t.Fatalf("Output through hops: %q, %v", got, err)
	}
	if err := remote.KillSession(ctx, id); err != nil {
		t.Fatalf("KillSession: %v", err)
	}

	// The first connection starts, installs and starts the agent. Everything after goes
	// through it, apart from the hops' own ssh, run on the first host.
	if n := runs(); n > 3+6 { //nolint:mnd // the agent's three, and two per hop command
		t.Errorf("%d ssh runs", n)
	}
	before := runs()
	for range 5 {
		if _, err := remote.Snapshot(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n := runs() - before; n != 0 {
		t.Errorf("polling made %d ssh connections", n)
		args, _ := os.ReadFile(filepath.Join(filepath.Dir(fake), "runs.args"))
		t.Logf("ssh runs:\n%s", args)
	}
}
