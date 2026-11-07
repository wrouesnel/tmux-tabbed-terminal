package filewatch_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/filewatch"
)

// watch starts watching path, and returns a channel of its changes once it's watching.
func watch(t *testing.T, path string) <-chan struct{} {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	changes := make(chan struct{}, 16)
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- filewatch.Watch(ctx, path, func() { close(started) }, func() { changes <- struct{}{} })
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Watch: %v", err)
		}
	})
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("Watch: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("never started watching")
	}
	return changes
}

func waitChange(t *testing.T, changes <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-changes:
	case <-time.After(5 * time.Second):
		t.Fatalf("no change seen after %s", what)
	}
	// Drain the rest of the burst.
	for {
		select {
		case <-changes:
		case <-time.After(100 * time.Millisecond):
			return
		}
	}
}

func TestWatchFollowsWritesAndReplacements(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "plan.md")
	changes := watch(t, file)

	// Made after the watch started.
	if err := os.WriteFile(file, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitChange(t, changes, "making the file")

	if err := os.WriteFile(file, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitChange(t, changes, "writing the file")

	// Saved as editors do: another file renamed over it.
	tmp := filepath.Join(dir, ".plan.md.tmp")
	if err := os.WriteFile(tmp, []byte("three"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, file); err != nil {
		t.Fatal(err)
	}
	waitChange(t, changes, "renaming over the file")

	// And again, to show the watch outlives the replacement.
	if err := os.WriteFile(file, []byte("four"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitChange(t, changes, "writing the replaced file")
}

func TestWatchIgnoresOtherFiles(t *testing.T) {
	dir := t.TempDir()
	changes := watch(t, filepath.Join(dir, "plan.md"))
	if err := os.WriteFile(filepath.Join(dir, "other.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changes:
		t.Fatal("a change to another file was reported")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestWatchFailsWithoutDirectory(t *testing.T) {
	err := filewatch.Watch(context.Background(), filepath.Join(t.TempDir(), "missing", "plan.md"),
		func() { t.Error("started watching a missing directory") }, func() {})
	if err == nil {
		t.Fatal("no error watching in a missing directory")
	}
}
