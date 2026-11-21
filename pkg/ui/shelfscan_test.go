package ui

import (
	"context"
	"fmt"
	"os/exec"
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
	var files []string
	for deadline := time.Now().Add(5 * time.Second); ; {
		sc.read()
		files = slices.Clone(sc.files)
		if sc.ok && slices.Contains(files, want[0]) && slices.Contains(files, want[1]) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("read %v (ok %v), want %v", files, sc.ok, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
	sc.read()
	if !sc.ok || !slices.Equal(sc.files, files) {
		t.Fatalf("second read %v, want %v", sc.files, files)
	}
	for i := range files {
		if unsafe.StringData(sc.files[i]) != unsafe.StringData(files[i]) {
			t.Errorf("%q was made again", files[i])
		}
	}
}
