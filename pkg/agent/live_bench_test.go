package agent_test

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/agent"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/agent/agenttest"
)

// Benchmarks of the agent on a real host, through this machine's ssh, as the application
// reaches one. They need TTT_TEST_SSH_HOST, a linux/amd64 host as ssh is configured to
// reach it, and are skipped without it.

const liveHostEnv = "TTT_TEST_SSH_HOST"

// liveConnect starts the agent on the host, installing it if it isn't there.
func liveConnect(b *testing.B) (*agent.Client, agent.Launcher, *agent.Binaries) {
	b.Helper()
	host := os.Getenv(liveHostEnv)
	if host == "" {
		b.Skip(liveHostEnv + " names no ssh host to benchmark against")
	}
	const version = "v9.9.9-bench"
	bins := agenttest.BuildFor(b, version, "linux", "amd64")
	launch := func(ctx context.Context, remote []string) *exec.Cmd {
		return exec.CommandContext(ctx, "ssh", append([]string{"-T", "--", host}, shellJoin(remote))...)
	}
	c, err := agent.Connect(context.Background(), launch, bins, version)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = c.Close() })
	return c, launch, bins
}

// shellJoin quotes argv for the remote shell, as pkg/tmux does.
func shellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}

// BenchmarkLiveConnect starts the agent, already installed, and greets it, as the
// application does the first time it reaches a host.
func BenchmarkLiveConnect(b *testing.B) {
	_, launch, bins := liveConnect(b)
	for b.Loop() {
		c, err := agent.Connect(context.Background(), launch, bins, "v9.9.9-bench")
		if err != nil {
			b.Fatal(err)
		}
		_ = c.Close()
	}
}

// BenchmarkLiveRun runs a program which does nothing, as a poll runs tmux.
func BenchmarkLiveRun(b *testing.B) {
	c, _, _ := liveConnect(b)
	for b.Loop() {
		if _, err := c.Run(context.Background(), []string{"true"}, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLiveEchoDuringDownload types into a terminal while a download shares the
// connection, reporting the worst echo too.
func BenchmarkLiveEchoDuringDownload(b *testing.B) {
	c, _, _ := liveConnect(b)
	term, err := c.Attach(context.Background(), []string{"sh", "-c", "stty raw -echo; echo ready; exec cat"}, agent.TerminalOptions{Cols: 80, Rows: 24})
	if err != nil {
		b.Fatal(err)
	}
	defer term.Close()
	buf := make([]byte, 64<<10)
	for got := ""; !strings.Contains(got, "ready"); {
		n, err := term.Read(buf)
		if err != nil {
			b.Fatal(err)
		}
		got += string(buf[:n])
	}
	ctx, cancel := context.WithCancel(context.Background())
	downloaded := make(chan struct{})
	go func() {
		defer close(downloaded)
		_ = c.Stream(ctx, io.Discard, []string{"cat", "/dev/zero"})
	}()
	defer func() {
		cancel()
		<-downloaded
	}()
	time.Sleep(3 * time.Second)
	var worst time.Duration
	for b.Loop() {
		start := time.Now()
		if _, err := term.Write([]byte("x")); err != nil {
			b.Fatal(err)
		}
		if n, err := term.Read(buf); err != nil || n != 1 {
			b.Fatal(n, err)
		}
		worst = max(worst, time.Since(start))
	}
	b.ReportMetric(float64(worst)/float64(time.Millisecond), "worst-ms")
}

// BenchmarkLiveDownload downloads 4 MiB.
func BenchmarkLiveDownload(b *testing.B) {
	const size = 4 << 20
	c, _, _ := liveConnect(b)
	b.SetBytes(size)
	for b.Loop() {
		if err := c.Stream(context.Background(), io.Discard, []string{"head", "-c", "4194304", "/dev/zero"}); err != nil {
			b.Fatal(err)
		}
	}
}
