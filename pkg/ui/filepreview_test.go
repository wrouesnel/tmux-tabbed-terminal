package ui

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

func TestPreviewable(t *testing.T) {
	for name, want := range map[string]bool{
		"README.md": true, "notes.MARKDOWN": true, "a/b.png": true, "shot.JPG": true,
		"main.go": false, "Makefile": false, "md": false,
	} {
		if got := previewable(name); got != want {
			t.Errorf("previewable(%q) = %v", name, got)
		}
	}
}

func TestPreviewResolve(t *testing.T) {
	f := &filePreview{file: "/work/docs/plan.md"}
	for dest, want := range map[string]string{
		"design.md":           "/work/docs/design.md",
		"../shots/a%20b.png":  "/work/shots/a b.png",
		"./x.md#section":      "/work/docs/x.md",
		"/etc/motd":           "/etc/motd",
		"~/notes.md":          "~/notes.md",
		"https://example.com": "",
		"#section":            "",
		"mailto:a@b.c":        "",
	} {
		if got := f.resolve(dest); got != want {
			t.Errorf("resolve(%q) = %q, want %q", dest, got, want)
		}
	}
	remote := &filePreview{file: "~/plans/plan.md", host: "far"}
	for dest, want := range map[string]string{
		"img/a.png": "~/plans/img/a.png",
		// The remote host's own file, not this machine's.
		"file:///srv/a.go":     "/srv/a.go",
		"file://far/srv/b.png": "/srv/b.png",
	} {
		if got := remote.resolve(dest); got != want {
			t.Errorf("remote resolve(%q) = %q, want %q", dest, got, want)
		}
	}
	local := &filePreview{file: "/work/plan.md", host: LocalHost}
	for dest, want := range map[string]string{
		"file:///srv/a.go":                  "/srv/a.go",
		"file://elsewhere.invalid/srv/a.go": "",
	} {
		if got := local.resolve(dest); got != want {
			t.Errorf("local resolve(%q) = %q, want %q", dest, got, want)
		}
	}
}

// TestFileStamp checks that a file's stamp changes when it's rewritten, here and through
// the ssh code path, with a fake ssh that runs the remote command through a local shell.
func TestFileStamp(t *testing.T) {
	hosts := map[string]*Host{"local": {client: &tmux.Client{Binary: "tmux"}}}
	if runtime.GOOS != "windows" {
		fake, err := filepath.Abs("../tmux/testdata/fake-ssh")
		if err != nil {
			t.Fatal(err)
		}
		hosts["remote"] = &Host{client: &tmux.Client{
			Binary: "tmux",
			SSH:    &tmux.SSH{Destination: "test-host", Binary: fake, ControlDir: t.TempDir()},
		}}
	}
	for which, h := range hosts {
		t.Run(which, func(t *testing.T) {
			ctx := context.Background()
			name := filepath.Join(t.TempDir(), "it's plan.md")
			if err := os.WriteFile(name, []byte("# One\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			first, err := fileStamp(ctx, h, name)
			if err != nil || first == "" {
				t.Fatalf("stamp %q, %v", first, err)
			}
			if again, _ := fileStamp(ctx, h, name); again != first {
				t.Errorf("unchanged file's stamp went from %q to %q", first, again)
			}
			// The same size, a second later, as a rewrite within a second might not show
			// in a remote stamp's whole seconds.
			if err := os.WriteFile(name, []byte("# Two\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			later := time.Now().Add(time.Second)
			if err := os.Chtimes(name, later, later); err != nil {
				t.Fatal(err)
			}
			if changed, _ := fileStamp(ctx, h, name); changed == first {
				t.Errorf("rewritten file's stamp stayed %q", changed)
			}
			if _, err := fileStamp(ctx, h, name+".gone"); err == nil {
				t.Error("a missing file has a stamp")
			}
		})
	}
}
