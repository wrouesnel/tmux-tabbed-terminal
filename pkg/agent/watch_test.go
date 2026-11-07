package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatch(t *testing.T) {
	c := startPair(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "plan.md")
	ctx, cancel := context.WithCancel(context.Background())
	started, changed := make(chan struct{}), make(chan struct{}, 16)
	done := make(chan error, 1)
	go func() {
		done <- c.Watch(ctx, file, func() { close(started) }, func() { changed <- struct{}{} })
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("watch: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the agent never started watching")
	}
	if err := os.WriteFile(file, []byte("plan"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("no change reported")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("a cancelled watch returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling didn't stop the watch")
	}

	// A directory that isn't there fails.
	err := c.Watch(context.Background(), filepath.Join(dir, "missing", "plan.md"), func() {}, func() {})
	if err == nil {
		t.Error("watching in a missing directory succeeded")
	}
}

func TestWatchPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	for in, want := range map[string]string{
		"~/notes/plan.md": filepath.Join(home, "notes", "plan.md"),
		"/tmp/plan.md":    "/tmp/plan.md",
		"plan.md":         "plan.md",
	} {
		if got, err := watchPath(in); err != nil || got != want {
			t.Errorf("watchPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := watchPath(""); err == nil {
		t.Error("an empty path was accepted")
	}
}
