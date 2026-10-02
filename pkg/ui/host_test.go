package ui

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestSessionKey(t *testing.T) {
	key := sessionKey("admin@db", "$3")
	host, id := splitKey(key)
	if host != "admin@db" || id != "$3" {
		t.Fatalf("splitKey(%q) = %q, %q", key, host, id)
	}
	if sessionKey(LocalHost, "$3") == key {
		t.Fatal("keys of the same ID on different hosts are equal")
	}
}

func TestHostsFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "hosts.yml")
	if hosts, err := loadHosts(path); err != nil || hosts != nil {
		t.Fatalf("missing file: %v, %v", hosts, err)
	}
	want := []HostConfig{
		{Destination: "buildbox"},
		{Destination: "admin@db", SSHOptions: []string{"-p", "2222"}, SocketName: "work"},
	}
	if err := saveHosts(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := loadHosts(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
