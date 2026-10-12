package ui

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

func TestIsImagePath(t *testing.T) {
	for p, want := range map[string]bool{
		"img/a.png": true, "/x/Photo.JPG": true, "b.jpeg": true, "c.gif": true,
		"docs/README.md": false, "noext": false, "dir.png/file": false, ".png": true,
	} {
		if got := isImagePath(p); got != want {
			t.Errorf("%s: got %v", p, got)
		}
	}
}

// sparseFile makes a file of size bytes which takes no space.
func sparseFile(t *testing.T, dir string, size int64) string {
	t.Helper()
	name := filepath.Join(dir, "big.png")
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestReadLocalImage(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "a.png")
	if err := os.WriteFile(small, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, err := readLocalImage(small); err != nil || string(data) != "data" {
		t.Errorf("small file: %q, %v", data, err)
	}
	big := sparseFile(t, dir, previewMaxBytes+1)
	if _, err := readLocalImage(big); !errors.Is(err, errPreviewTooBig) {
		t.Errorf("big file: %v", err)
	}
	if _, err := readLocalImage(dir); err == nil {
		t.Error("read a directory")
	}
	if _, err := readLocalImage(filepath.Join(dir, "gone.png")); err == nil {
		t.Error("read a missing file")
	}
}

// TestReadRemoteImage reads images through the ssh code path, with a fake ssh that runs
// the remote command through a local shell.
func TestReadRemoteImage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake ssh is a shell script")
	}
	fake, err := filepath.Abs("../tmux/testdata/fake-ssh")
	if err != nil {
		t.Fatal(err)
	}
	h := &Host{client: &tmux.Client{
		Binary: "tmux",
		SSH:    &tmux.SSH{Destination: "test-host", Binary: fake, ControlDir: t.TempDir()},
	}}
	dir := t.TempDir()
	// Binary data, which must come through unchanged.
	want := []byte{0x89, 'P', 'N', 'G', 0, 0xff, '\n', 0xfe, 0}
	name := filepath.Join(dir, "it's a.png")
	if err := os.WriteFile(name, want, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if got, err := readHostImage(ctx, h, name); err != nil || !bytes.Equal(got, want) {
		t.Errorf("got %q, %v, want %q", got, err, want)
	}
	big := sparseFile(t, dir, previewMaxBytes+1)
	if _, err := readHostImage(ctx, h, big); !errors.Is(err, errPreviewTooBig) {
		t.Errorf("big file: %v", err)
	}
	if _, err := readHostImage(ctx, h, filepath.Join(dir, "gone.png")); err == nil {
		t.Error("read a missing file")
	}
}
