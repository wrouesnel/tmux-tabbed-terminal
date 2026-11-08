package ui

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestLocalFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix paths")
	}
	got := localFiles([]string{
		"file:///home/me/it%27s%20a%20file.png",
		"file://localhost/tmp/b.txt",
		"https://example.com/c.txt",
		"file://otherhost/d.txt",
		"not a uri%",
	})
	want := []string{"/home/me/it's a file.png", "/tmp/b.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("localFiles = %q, want %q", got, want)
	}
}

func TestParseURIList(t *testing.T) {
	got := parseURIList([]byte("# a comment\r\nfile:///a/b%20c\r\n\r\nfile:///d\r\n\x00"))
	want := []string{"file:///a/b%20c", "file:///d"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseURIList = %q, want %q", got, want)
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"/home/me/plan.md":      "/home/me/plan.md",
		"/home/me/a b.png":      "'/home/me/a b.png'",
		"/home/me/it's.png":     `'/home/me/it'\''s.png'`,
		"/home/me/$HOME`x`.txt": "'/home/me/$HOME`x`.txt'",
		"":                      "''",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSaveUpload names files as SSH.Upload does on other hosts: never over another file.
func TestSaveUpload(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct{ name, want string }{
		{"a.tar.gz", "a.tar.gz"},
		{"a.tar.gz", "a.tar-2.gz"},
		{".bashrc", ".bashrc"},
		{".bashrc", ".bashrc-2"},
		{"notes", "notes"},
		{"notes", "notes-2"},
	} {
		saved, err := saveUpload(strings.NewReader(c.name), dir, c.name)
		if err != nil || saved != filepath.Join(dir, c.want) {
			t.Fatalf("saveUpload %q: %q, %v; want %q", c.name, saved, err, c.want)
		}
		if got, _ := os.ReadFile(saved); string(got) != c.name {
			t.Errorf("saved %q in %s", got, saved)
		}
	}
	// The uploads, and no hidden part files.
	if entries, _ := os.ReadDir(dir); len(entries) != 6 { //nolint:mnd // the uploads
		t.Errorf("%d files in the directory", len(entries))
	}
	if _, err := saveUpload(strings.NewReader("x"), filepath.Join(dir, "missing"), "a"); err == nil {
		t.Error("saved to a missing directory")
	}
}
