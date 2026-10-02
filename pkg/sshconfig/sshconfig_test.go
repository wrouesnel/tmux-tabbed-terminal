package sshconfig_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/sshconfig"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config")
	write(t, config, `# comment
Include config.d/*
Host buildbox db
    HostName build.example.com
    User admin
    HostName ignored.example.com

Host *.internal !bastion web-?
    User nobody

host=quoted "spaced name"
  hostname 10.0.0.5

Match host foo
  HostName should-not-apply

Host buildbox
  User later
Include "nested"
`)
	write(t, filepath.Join(dir, "config.d", "a"), "Host included\n  HostName inc.example.com\n")
	write(t, filepath.Join(dir, "nested"), "Host nested-host\nInclude nested\n")

	hosts, err := sshconfig.Load(config)
	if err != nil {
		t.Fatal(err)
	}
	want := []sshconfig.Host{
		{Alias: "included", HostName: "inc.example.com"},
		{Alias: "buildbox", HostName: "build.example.com", User: "admin"},
		{Alias: "db", HostName: "build.example.com", User: "admin"},
		{Alias: "quoted", HostName: "10.0.0.5"},
		{Alias: "spaced name", HostName: "10.0.0.5"},
		{Alias: "nested-host"},
	}
	if !reflect.DeepEqual(hosts, want) {
		t.Fatalf("got  %+v\nwant %+v", hosts, want)
	}
}

func TestLoadMissing(t *testing.T) {
	hosts, err := sshconfig.Load(filepath.Join(t.TempDir(), "nope"))
	if err != nil || len(hosts) != 0 {
		t.Fatalf("missing file: %v, %v", hosts, err)
	}
}
