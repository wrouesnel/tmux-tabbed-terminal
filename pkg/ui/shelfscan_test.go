package ui

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"slices"
	"testing"
	"time"
	"unsafe"

	"go.uber.org/zap"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

// TestShelfScannerRead reads a real pane for the files it mentions, twice: the second
// read finds the same files, with the strings the first made.
func TestShelfScannerRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		// MSYS2 tmux's panes name Windows directories, and local paths are taken as
		// Windows ones: the reading this tests doesn't differ by platform.
		t.Skip("the paths here are Unix ones")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux")
	}
	dir := t.TempDir()
	socket := fmt.Sprintf("ttt-shelfscan-%d", time.Now().UnixNano())
	client := tmux.NewClient()
	client.SocketName = socket
	ctx := context.Background()
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	id, err := client.NewSession(ctx, "scan", dir)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := client.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	aw := snap.Session(id).ActiveWindow()
	if err := exec.Command("tmux", "-L", socket, "send-keys", "-t", aw.PaneID,
		"clear; echo see /tmp/notes.md and ./pic.png:3:1 too", "Enter").Run(); err != nil {
		t.Fatal(err)
	}

	app := &App{ctx: ctx, log: zap.NewNop()}
	sc := &shelfScanner{app: app, seen: map[string]*seenLink{}}
	sc.run, sc.addLine, sc.addLink, sc.finish = sc.read, sc.line, sc.link, func() {}
	sc.h = &Host{client: client}
	sc.single, sc.only = true, tmux.Pane{ID: aw.PaneID, Path: dir}
	want := []string{"/tmp/notes.md", dir + "/pic.png"}
	// Read until the pane has settled, with what's typed and what it prints on screen,
	// and two reads in a row find the same files.
	var files []string
	for deadline := time.Now().Add(5 * time.Second); ; {
		sc.read()
		if sc.ok && slices.Contains(sc.files, want[0]) && slices.Contains(sc.files, want[1]) &&
			slices.Equal(sc.files, files) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("read %v (ok %v), want %v", sc.files, sc.ok, want)
		}
		files = slices.Clone(sc.files)
		time.Sleep(50 * time.Millisecond)
	}
	for i := range files {
		if unsafe.StringData(sc.files[i]) != unsafe.StringData(files[i]) {
			t.Errorf("%q was made again", files[i])
		}
	}
}
