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
	if data, err := readLocalFile(small); err != nil || string(data) != "data" {
		t.Errorf("small file: %q, %v", data, err)
	}
	big := sparseFile(t, dir, previewMaxBytes+1)
	if _, err := readLocalFile(big); !errors.Is(err, errPreviewTooBig) {
		t.Errorf("big file: %v", err)
	}
	if _, err := readLocalFile(dir); err == nil {
		t.Error("read a directory")
	}
	if _, err := readLocalFile(filepath.Join(dir, "gone.png")); err == nil {
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
	if got, err := readHostFile(ctx, h, name); err != nil || !bytes.Equal(got, want) {
		t.Errorf("got %q, %v, want %q", got, err, want)
	}
	big := sparseFile(t, dir, previewMaxBytes+1)
	if _, err := readHostFile(ctx, h, big); !errors.Is(err, errPreviewTooBig) {
		t.Errorf("big file: %v", err)
	}
	if _, err := readHostFile(ctx, h, filepath.Join(dir, "gone.png")); err == nil {
		t.Error("read a missing file")
	}
}

// TestReadHostFileStart reads the start of files, here and through the ssh code path.
func TestReadHostFileStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake ssh is a shell script")
	}
	fake, err := filepath.Abs("../tmux/testdata/fake-ssh")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	name := filepath.Join(dir, "it's a plan.md")
	if err := os.WriteFile(name, []byte("# Plan\n\nmore"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, h := range []*Host{
		{client: &tmux.Client{Binary: "tmux"}},
		{client: &tmux.Client{
			Binary: "tmux",
			SSH:    &tmux.SSH{Destination: "test-host", Binary: fake, ControlDir: t.TempDir()},
		}},
	} {
		if got, err := readHostFileStart(ctx, h, name, 6); err != nil || string(got) != "# Plan" {
			t.Errorf("got %q, %v", got, err)
		}
		if _, err := readHostFileStart(ctx, h, filepath.Join(dir, "gone.md"), 6); err == nil {
			t.Error("read a missing file")
		}
	}
}

func TestWholeBlocks(t *testing.T) {
	for in, want := range map[string]string{
		"# Plan\n\nOne paragraph.\n\nA paragraph cut": "# Plan\n\nOne paragraph.\n",
		"no blank line, cut off or not":               "no blank line, cut off or not",
	} {
		if got := string(wholeBlocks([]byte(in), len(in))); got != want {
			t.Errorf("wholeBlocks(%q) = %q, want %q", in, got, want)
		}
	}
	// A file read whole keeps its last block.
	for _, in := range []string{"# Plan\n\nThe end.", ""} {
		if got := string(wholeBlocks([]byte(in), len(in)+1)); got != in {
			t.Errorf("wholeBlocks(%q) = %q, want it whole", in, got)
		}
	}
}

func TestHTMLText(t *testing.T) {
	doc := `<!DOCTYPE html><html><head><title>The  &amp; Report</title>
<style>body { color: red }</style><script>var x = "<p>no</p>";</script></head>
<body><h1>Results</h1><!-- hidden --><p>All <b>42</b> tests
passed &lt;ok&gt;.</p><ul><li>one</li><li>two</li></ul><div>cut off <span`
	want := "The & Report\n\nResults\nAll 42 tests\npassed <ok>.\none\ntwo\ncut off"
	if got := htmlText(doc); got != want {
		t.Errorf("htmlText = %q, want %q", got, want)
	}
	if !isHTMLPath("/a/Report.HTML") || isHTMLPath("/a/report.md") {
		t.Error("isHTMLPath")
	}
}
