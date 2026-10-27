package askpass_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/askpass"
)

const keyPrompt = "Enter passphrase for key '/home/u/.ssh/id_ed25519': "

func TestServerAnswers(t *testing.T) {
	var got askpass.Request
	srv, err := askpass.Listen(t.TempDir(), func(req askpass.Request) askpass.Response {
		got = req
		return askpass.Response{OK: true, Secret: "s3cret"}
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer srv.Close()

	env := map[string]string{}
	for _, kv := range srv.Env("/bin/app") {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	if env["SSH_ASKPASS"] != "/bin/app" || env[askpass.ModeEnv] != "1" || env["SSH_ASKPASS_REQUIRE"] != "force" {
		t.Fatalf("environment: %v", env)
	}
	fi, err := os.Stat(env[askpass.SocketEnv])
	if err != nil {
		t.Fatalf("socket: %v", err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("socket is readable by others: %v", fi.Mode())
	}

	// Main, as ssh runs it, with the server's environment.
	for k, v := range env {
		t.Setenv(k, v)
	}
	var stdout, stderr bytes.Buffer
	if code := askpass.Main(context.Background(), []string{keyPrompt}, &stdout, &stderr); code != 0 {
		t.Fatalf("Main: exit %d: %s", code, stderr.String())
	}
	if stdout.String() != "s3cret\n" {
		t.Errorf("stdout: got %q, want the secret and a newline", stdout.String())
	}
	if got.Prompt != keyPrompt || got.Token != "" || got.PID != os.Getppid() {
		t.Errorf("request the handler got: %+v", got)
	}

	// A request without the token gets no answer.
	if _, err := askpass.Ask(context.Background(), env[askpass.SocketEnv], askpass.Request{Prompt: "x"}); err == nil {
		t.Error("a request without the token was answered")
	}
}

func TestMainCancelled(t *testing.T) {
	srv, err := askpass.Listen(t.TempDir(), func(askpass.Request) askpass.Response { return askpass.Response{} })
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer srv.Close()
	for _, kv := range srv.Env("/bin/app") {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	var stdout, stderr bytes.Buffer
	if code := askpass.Main(context.Background(), []string{keyPrompt}, &stdout, &stderr); code != 1 || stdout.Len() != 0 {
		t.Errorf("cancelled: exit %d, stdout %q; want 1 and nothing", code, stdout.String())
	}
}

func TestMainWithoutServer(t *testing.T) {
	t.Setenv(askpass.SocketEnv, "")
	var stdout, stderr bytes.Buffer
	if code := askpass.Main(context.Background(), []string{keyPrompt}, &stdout, &stderr); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
}

func TestMemory(t *testing.T) {
	m := askpass.NewMemory()
	first := askpass.Request{Prompt: keyPrompt, PID: 100}
	if _, ok := m.Recall(first); ok {
		t.Fatal("an empty memory recalled a passphrase")
	}
	m.Remember(first, "right")

	// Another ssh, as for another host with the same key, gets it without asking.
	second := askpass.Request{Prompt: keyPrompt, PID: 200}
	if got, ok := m.Recall(second); !ok || got != "right" {
		t.Fatalf("recall for another ssh: %q, %v", got, ok)
	}
	// The same ssh asking again means the key refused it: it's forgotten.
	if _, ok := m.Recall(second); ok {
		t.Fatal("recalled a passphrase the key refused")
	}
	if _, ok := m.Recall(askpass.Request{Prompt: keyPrompt, PID: 300}); ok {
		t.Fatal("a refused passphrase was still remembered")
	}

	// Passwords and host key questions are never remembered.
	pw := askpass.Request{Prompt: "user@host's password: ", PID: 400}
	m.Remember(pw, "hunter2")
	if _, ok := m.Recall(askpass.Request{Prompt: pw.Prompt, PID: 401}); ok {
		t.Error("remembered a password")
	}
	confirm := askpass.Request{Prompt: "Allow use of key " + keyPrompt, Kind: "confirm", PID: 500}
	if askpass.IsKeyPassphrase(confirm) {
		t.Error("a confirmation counts as a passphrase")
	}

	m.Remember(first, "right")
	m.Forget()
	if _, ok := m.Recall(second); ok {
		t.Error("Forget left a passphrase")
	}
}
