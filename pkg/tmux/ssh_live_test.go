package tmux_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/agent/agenttest"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

// Environment of TestLiveSSHThroughAgent.
const (
	// liveHostEnv names a real ssh host, as ssh is configured to reach it, with tmux.
	liveHostEnv = "TTT_TEST_SSH_HOST"
	// livePlatformEnv is the host's platform, as GOOS/GOARCH: linux/amd64 if unset.
	livePlatformEnv = "TTT_TEST_SSH_PLATFORM"
)

// TestLiveSSHThroughAgent reaches a real host through this machine's ssh, without
// connection sharing and through the agent, as the application does on Windows: it runs a
// session's lifecycle, copies a file, and attaches a terminal to the session and resizes
// it. It needs TTT_TEST_SSH_HOST, and is skipped without it.
func TestLiveSSHThroughAgent(t *testing.T) {
	host := os.Getenv(liveHostEnv)
	if host == "" {
		t.Skip(liveHostEnv + " names no ssh host to test against")
	}
	goos, goarch, _ := strings.Cut(os.Getenv(livePlatformEnv), "/")
	if goos == "" {
		goos, goarch = "linux", "amd64"
	}
	const version = "v9.9.9-live-test"
	ssh := &tmux.SSH{
		Destination: host, NoConnectionSharing: true,
		Agents: agenttest.BuildFor(t, version, goos, goarch), AgentVersion: version,
	}
	client := &tmux.Client{Binary: "tmux", SocketName: "ttt-live-test", SSH: ssh}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	id, err := client.NewSession(ctx, "it's live", "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = client.KillSession(context.Background(), id) })
	snap, err := client.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if s := snap.Session(id); s == nil || s.Name != "it's live" {
		t.Fatalf("session: %+v", snap)
	}

	content := "binary\x00\xff and text\n"
	if _, err := ssh.Output(ctx, "sh", "-c", `printf 'binary\000\377 and text\n' > "$HOME/.ttt-live-test"`); err != nil {
		t.Fatalf("writing a file: %v", err)
	}
	var out bytes.Buffer
	if err := ssh.CopyFile(ctx, &out, "~/.ttt-live-test"); err != nil || out.String() != content {
		t.Errorf("CopyFile: %q, %v", out.String(), err)
	}

	term, err := client.AttachThroughAgent(ctx, id, nil, map[string]string{"TERM": "xterm-256color"})
	if err != nil || term == nil {
		t.Fatalf("AttachThroughAgent: %v, %v", term, err)
	}
	if err := term.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	// The client's size reaches the session.
	deadline := time.Now().Add(20 * time.Second)
	for {
		size, err := ssh.Output(ctx, "tmux", "-L", "ttt-live-test", "display-message", "-p", "-t", id, "#{client_width}x#{client_height}")
		if err == nil && strings.TrimSpace(size) == "100x30" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the client's size is %q, %v", size, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, err := term.Write([]byte("echo typed-$((6*7))\r")); err != nil {
		t.Fatal(err)
	}
	// Killing the session ends the client, and the terminal's output with it.
	read := make(chan []byte)
	go func() {
		data, _ := io.ReadAll(term)
		read <- data
	}()
	time.Sleep(time.Second)
	if err := client.KillSession(ctx, id); err != nil {
		t.Fatalf("KillSession: %v", err)
	}
	select {
	case data := <-read:
		if !bytes.Contains(data, []byte("typed-42")) {
			t.Errorf("the terminal showed %q", data)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the terminal didn't end with its session")
	}
	_ = term.Wait()
}
