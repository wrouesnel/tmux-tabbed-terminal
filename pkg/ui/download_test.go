package ui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFreePath(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.tar.gz", "a.tar (2).gz", "notes", ".bashrc"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]string{
		"a.tar.gz": "a.tar (3).gz",
		"notes":    "notes (2)",
		".bashrc":  ".bashrc (2)",
		"new.txt":  "new.txt",
	}
	for name, want := range cases {
		if got := freePath(dir, name); got != filepath.Join(dir, want) {
			t.Errorf("%s: got %s, want %s", name, filepath.Base(got), want)
		}
	}
}

func TestDownloadName(t *testing.T) {
	cases := map[string]string{
		"/var/log/my report.pdf": "my report.pdf",
		"~/a.tar.gz":             "a.tar.gz",
		"/usr/lib/":              "lib",
		"/":                      "download",
		"..":                     "download",
	}
	for p, want := range cases {
		if got := downloadName(p); got != want {
			t.Errorf("%s: got %q, want %q", p, got, want)
		}
	}
}

func TestDownloadable(t *testing.T) {
	cases := []struct {
		l    link
		want bool
	}{
		{link{file: "/a", host: "far"}, true},
		{link{file: "/a", host: LocalHost}, false},
		{link{file: "", host: "far"}, false},
		{link{file: "/a"}, false},
	}
	for _, c := range cases {
		if got := c.l.downloadable(); got != c.want {
			t.Errorf("%+v: got %v", c.l, got)
		}
	}
}
