package tmux_tabbed_terminal_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wrouesnel/ctxstdio"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/entrypoints/tmux_tabbed_terminal"
	"github.com/wrouesnel/tmux-tabbed-terminal/version"
)

// runEntrypoint invokes the entrypoint as though from the command line and captures its output.
func runEntrypoint(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	stdOut, stdErr := new(bytes.Buffer), new(bytes.Buffer)
	ctx := ctxstdio.Set(context.Background(), stdOut, stdErr, os.Stdin)
	exitCode := tmux_tabbed_terminal.Entrypoint(ctx, args)
	return exitCode, stdOut.String(), stdErr.String()
}

// writeConfig writes a config file to a temporary directory and returns its path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath
}

func TestVersion(t *testing.T) {
	exitCode, stdOut, _ := runEntrypoint(t, "--version")
	if exitCode != 0 {
		t.Fatalf("exit code: got %d, want 0", exitCode)
	}
	if !strings.Contains(stdOut, version.Version) {
		t.Errorf("stdout %q does not contain version %q", stdOut, version.Version)
	}
}

func TestConfigOverridesDefaults(t *testing.T) {
	configPath := writeConfig(t, "tmux:\n  socket-name: work\nactivity:\n  poll-interval: 500ms\n")
	exitCode, stdOut, stdErr := runEntrypoint(t, "--config-file", configPath, "config")
	if exitCode != 0 {
		t.Fatalf("exit code: got %d, want 0: %s", exitCode, stdErr)
	}
	for _, want := range []string{"socket-name: work", "poll-interval: 500ms", "timeout: 2s", "confirm-kill: true"} {
		if !strings.Contains(stdOut, want) {
			t.Errorf("config output does not contain %q:\n%s", want, stdOut)
		}
	}
}

func TestDefaultConfigIsOptional(t *testing.T) {
	// Point the XDG config directory somewhere empty.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if exitCode, _, stdErr := runEntrypoint(t, "config"); exitCode != 0 {
		t.Fatalf("exit code: got %d, want 0: %s", exitCode, stdErr)
	}
}

func TestExampleConfigLoads(t *testing.T) {
	exitCode, _, stdErr := runEntrypoint(t, "--config-file", "../../../packaging/config.example.yml", "config")
	if exitCode != 0 {
		t.Fatalf("exit code: got %d, want 0: %s", exitCode, stdErr)
	}
}

func TestMissingConfigFileFails(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "missing.yml")
	if exitCode, _, _ := runEntrypoint(t, "--config-file", configPath, "config"); exitCode != 1 {
		t.Fatalf("exit code: got %d, want 1", exitCode)
	}
}

func TestInvalidFlagFails(t *testing.T) {
	exitCode, _, stdErr := runEntrypoint(t, "--no-such-flag")
	if exitCode == 0 {
		t.Fatal("exit code: got 0, want non-zero")
	}
	if !strings.Contains(stdErr, "no-such-flag") {
		t.Errorf("stderr %q does not mention the bad flag", stdErr)
	}
}
