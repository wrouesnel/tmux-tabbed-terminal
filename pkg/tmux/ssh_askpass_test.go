package tmux_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/askpass"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

// TestMain runs the test binary as SSH_ASKPASS when ssh runs it so, as the application
// runs itself.
func TestMain(m *testing.M) {
	if os.Getenv(askpass.ModeEnv) == "1" {
		os.Exit(askpass.Main(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

func TestSSHConfigFileOption(t *testing.T) {
	local := &tmux.SSH{Destination: "box", ControlDir: t.TempDir()}
	before := local.CheckArgs()
	if slices.Contains(before, "-F") {
		t.Fatalf("-F without a configuration file: %q", before)
	}
	local.SetConfigFile("/etc/alt/ssh_config")
	args := local.MasterArgs()
	if i := slices.Index(args, "-F"); i < 0 || args[i+1] != "/etc/alt/ssh_config" {
		t.Fatalf("master args lack -F: %q", args)
	}
	// Another configuration may reach another host by the same name, so it gets its own
	// master.
	if controlPath(before) == controlPath(local.CheckArgs()) {
		t.Error("the control path didn't change with the configuration file")
	}

	// A hop run from another host uses that host's configuration.
	hop := &tmux.SSH{Destination: "inner", Via: local}
	hop.SetConfigFile("/etc/alt/ssh_config")
	if slices.Contains(hop.MasterArgs(), "-F") {
		t.Errorf("a hop run elsewhere got this machine's configuration: %q", hop.MasterArgs())
	}
}

// controlPath returns the ControlPath option of ssh arguments.
func controlPath(args []string) string {
	for _, a := range args {
		if p, ok := strings.CutPrefix(a, "ControlPath="); ok {
			return p
		}
	}
	return ""
}

func TestSSHBatchModeWithoutAskpass(t *testing.T) {
	plain := &tmux.SSH{Destination: "box", ControlDir: t.TempDir()}
	if !slices.Contains(plain.MasterArgs(), "BatchMode=yes") {
		t.Errorf("a master without askpass doesn't use BatchMode: %q", plain.MasterArgs())
	}
	asking := &tmux.SSH{Destination: "box", ControlDir: t.TempDir(), AskpassEnv: []string{"SSH_ASKPASS=/x"}}
	if slices.Contains(asking.MasterArgs(), "BatchMode=yes") {
		t.Errorf("a master with askpass uses BatchMode, so it can't ask: %q", asking.MasterArgs())
	}
	// A hop run from another host can't ask the user here.
	hop := &tmux.SSH{Destination: "inner", Via: asking, AskpassEnv: []string{"SSH_ASKPASS=/x"}}
	if !slices.Contains(hop.MasterArgs(), "BatchMode=yes") {
		t.Errorf("a hop run elsewhere doesn't use BatchMode: %q", hop.MasterArgs())
	}
}

// TestSSHMasterAsksForPassphrase connects through a fake ssh whose key has a passphrase:
// the master asks for it through SSH_ASKPASS, here the test binary, which asks the
// askpass server, and commands then run over the master without asking again.
func TestSSHMasterAsksForPassphrase(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake ssh is a shell script")
	}
	fake, err := filepath.Abs("testdata/fake-ssh-askpass")
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "ssh.log")
	t.Setenv("FAKE_SSH_LOG", log)

	var mu sync.Mutex
	var prompts []string
	srv, err := askpass.Listen(t.TempDir(), func(req askpass.Request) askpass.Response {
		mu.Lock()
		defer mu.Unlock()
		prompts = append(prompts, req.Prompt)
		return askpass.Response{OK: true, Secret: "right"}
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer srv.Close()

	ssh := &tmux.SSH{Destination: "box", Binary: fake, ControlDir: t.TempDir(), AskpassEnv: srv.Env(exe)}
	ctx := context.Background()
	for i := range 2 {
		out, err := ssh.Output(ctx, "echo", "hello")
		if err != nil || out != "hello\n" {
			t.Fatalf("command %d: %q, %v", i, out, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(prompts) != 1 || !strings.Contains(prompts[0], "passphrase for key") {
		t.Errorf("prompts: got %q, want one for the key's passphrase", prompts)
	}
	calls, _ := os.ReadFile(log)
	for _, line := range strings.Split(strings.TrimSpace(string(calls)), "\n") {
		if strings.Contains(line, "-N") && strings.Contains(line, "BatchMode=yes") {
			t.Errorf("the master ran in BatchMode: %s", line)
		}
	}
}

// TestSSHMasterWrongPassphrase checks a master fails, rather than hangs, when the user's
// answer is wrong or they cancel.
func TestSSHMasterWrongPassphrase(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake ssh is a shell script")
	}
	fake, err := filepath.Abs("testdata/fake-ssh-askpass")
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_SSH_LOG", filepath.Join(t.TempDir(), "ssh.log"))
	for _, resp := range []askpass.Response{{OK: true, Secret: "wrong"}, {}} {
		srv, err := askpass.Listen(t.TempDir(), func(askpass.Request) askpass.Response { return resp })
		if err != nil {
			t.Fatalf("Listen: %v", err)
		}
		ssh := &tmux.SSH{Destination: "box", Binary: fake, ControlDir: t.TempDir(), AskpassEnv: srv.Env(exe)}
		if _, err := ssh.Output(context.Background(), "true"); err == nil {
			t.Errorf("answer %+v: connected", resp)
		}
		srv.Close()
	}
}

// TestSSHCommandAfterSlowAnswer checks a command whose time went on the user answering
// ssh's question still runs once the master is up.
func TestSSHCommandAfterSlowAnswer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake ssh is a shell script")
	}
	fake, err := filepath.Abs("testdata/fake-ssh-askpass")
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_SSH_LOG", filepath.Join(t.TempDir(), "ssh.log"))
	srv, err := askpass.Listen(t.TempDir(), func(askpass.Request) askpass.Response {
		time.Sleep(600 * time.Millisecond) // the user typing
		return askpass.Response{OK: true, Secret: "right"}
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer srv.Close()

	ssh := &tmux.SSH{Destination: "box", Binary: fake, ControlDir: t.TempDir(), AskpassEnv: srv.Env(exe)}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if out, err := ssh.Output(ctx, "echo", "late"); err != nil || out != "late\n" {
		t.Fatalf("command after a slow answer: %q, %v", out, err)
	}
}
