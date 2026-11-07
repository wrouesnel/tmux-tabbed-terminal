package ui

import "testing"

func TestDestinationHost(t *testing.T) {
	for dest, want := range map[string]string{
		"db":                      "db",
		"admin@db.example.com":    "db.example.com",
		"ssh://admin@db:2222":     "db",
		"ssh://[fe80::1]:22":      "fe80::1",
		"me@corp.example@bastion": "bastion",
		"ssh://db.example.com":    "db.example.com",
	} {
		if got := destinationHost(dest); got != want {
			t.Errorf("destinationHost(%q) = %q, want %q", dest, got, want)
		}
	}
}

func TestKeySlug(t *testing.T) {
	if got := keySlug("fe80::1%eth0"); got != "fe80__1_eth0" {
		t.Errorf("keySlug = %q", got)
	}
}

func TestWithHostIdentity(t *testing.T) {
	block := "# Added by tmux-tabbed-terminal: the key id_ed25519_db logs in to db.\n" +
		"Host db\n    IdentityFile ~/.ssh/id_ed25519_db\n"
	for config, want := range map[string]string{
		"":                   block,
		"Host x\n  User y\n": "Host x\n  User y\n\n" + block,
		"Host x\n  User y":   "Host x\n  User y\n\n" + block,
	} {
		if got := withHostIdentity(config, "db", "~/.ssh/id_ed25519_db"); got != want {
			t.Errorf("withHostIdentity(%q) = %q, want %q", config, got, want)
		}
	}
}
