package agent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"
	"time"
)

// startPair starts an agent and a client talking to it over in-memory pipes, as they
// would over ssh.
func startPair(t *testing.T) *Client {
	t.Helper()
	toAgent, fromClient := io.Pipe()
	toClient, fromAgent := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() {
		served <- Serve(ctx, toAgent, fromAgent, "v1.2.3")
		_ = fromAgent.Close()
	}()
	c, err := NewClient(toClient, fromClient, fromClient, toClient)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.Close()
		cancel()
		select {
		case err := <-served:
			if err != nil {
				t.Errorf("serve: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("the agent didn't stop")
		}
	})
	return c
}

func TestHello(t *testing.T) {
	c := startPair(t)
	hello, err := c.Hello(context.Background(), "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if hello.GetVersion() != "v1.2.3" || hello.GetOs() != runtime.GOOS || hello.GetArch() != runtime.GOARCH || hello.GetHome() == "" {
		t.Errorf("hello %v", hello)
	}
}

func TestRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the agent runs on Unix hosts")
	}
	c := startPair(t)
	ctx := context.Background()
	res, err := c.Run(ctx, []string{"sh", "-c", `cat; echo " $1" >&2; exit 3`, "sh", "it's"}, []byte("in"))
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Stdout) != "in" || string(res.Stderr) != " it's\n" || res.Code != 3 {
		t.Errorf("result %+v", res)
	}
	var exitErr *ExitError
	if err := res.Err(); !errors.As(err, &exitErr) || exitErr.Stderr != " it's\n" {
		t.Errorf("err %v", err)
	}
	// Big output, beyond gRPC's default message size.
	res, err = c.Run(ctx, []string{"head", "-c", "10000000", "/dev/zero"}, nil)
	if err != nil || len(res.Stdout) != 10000000 || res.Err() != nil {
		t.Errorf("big output: %d bytes, %v, %v", len(res.Stdout), err, res.Err())
	}
	if _, err := c.Run(ctx, []string{"no-such-program-here"}, nil); err == nil {
		t.Error("a missing program ran")
	}
	res, err = c.Run(ctx, []string{"sh", "-c", "kill -KILL $$"}, nil)
	if err != nil || res.Code != -1 || res.Signal != "killed" {
		t.Errorf("killed: %+v, %v", res, err)
	}
}

func TestStream(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the agent runs on Unix hosts")
	}
	c := startPair(t)
	ctx := context.Background()
	var out bytes.Buffer
	if err := c.Stream(ctx, &out, []string{"head", "-c", "5000000", "/dev/zero"}); err != nil || out.Len() != 5000000 {
		t.Errorf("stream: %d bytes, %v", out.Len(), err)
	}
	var exitErr *ExitError
	err := c.Stream(ctx, io.Discard, []string{"sh", "-c", "echo nope >&2; exit 1"})
	if !errors.As(err, &exitErr) || exitErr.Stderr != "nope\n" {
		t.Errorf("failing stream: %v", err)
	}
	// Cancelling the call kills the program.
	cctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	began := time.Now()
	if err := c.Stream(cctx, io.Discard, []string{"sleep", "30"}); err == nil {
		t.Error("a cancelled stream succeeded")
	}
	if time.Since(began) > 5*time.Second {
		t.Error("cancelling didn't stop the stream")
	}
}

func TestAttach(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the agent runs on Unix hosts")
	}
	c := startPair(t)
	term, err := c.Attach(context.Background(), []string{"sh", "-c", `stty size; echo "$FOO"; tty; read line; echo "got $line"; exit 4`},
		TerminalOptions{Cols: 100, Rows: 30, Env: map[string]string{"FOO": "bar"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(term.Name(), "/dev/") {
		t.Errorf("tty %q", term.Name())
	}
	if _, err := term.Write([]byte("hello\r")); err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(term)
	text := strings.ReplaceAll(string(out), "\r\n", "\n")
	for _, want := range []string{"30 100\n", "bar\n", term.Name() + "\n", "got hello\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("output %q lacks %q", text, want)
		}
	}
	var exitErr *ExitError
	if err := term.Wait(); !errors.As(err, &exitErr) || err.Error() != "exit status 4" {
		t.Errorf("wait: %v", err)
	}

	// Resizing, and hanging up.
	term, err = c.Attach(context.Background(), []string{"sh", "-c", `trap 'stty size; exit 0' WINCH; echo ready; while :; do sleep 0.05; done`},
		TerminalOptions{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	// Resized once the trap is set.
	var seen []byte
	buf := make([]byte, 256)
	for !bytes.Contains(seen, []byte("ready")) {
		n, err := term.Read(buf)
		if err != nil {
			t.Fatalf("before ready: %q, %v", seen, err)
		}
		seen = append(seen, buf[:n]...)
	}
	if err := term.Resize(132, 43); err != nil {
		t.Fatal(err)
	}
	out, _ = io.ReadAll(term)
	if !strings.Contains(string(out), "43 132") {
		t.Errorf("after resize: %q", out)
	}
	term, err = c.Attach(context.Background(), []string{"sleep", "30"}, TerminalOptions{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	_ = term.Close()
	_, _ = io.ReadAll(term)
	if time.Since(began) > 5*time.Second {
		t.Error("closing didn't hang the program up")
	}
}

func TestGoPlatform(t *testing.T) {
	for in, want := range map[[2]string][2]string{
		{"Linux", "x86_64"}: {"linux", "amd64"}, {"Linux", "aarch64"}: {"linux", "arm64"},
		{"Darwin", "arm64"}: {"darwin", "arm64"}, {"Linux", "armv7l"}: {}, {"SunOS", "i86pc"}: {},
	} {
		goos, goarch, ok := goPlatform(in[0], in[1])
		if ok != (want != [2]string{}) || (ok && [2]string{goos, goarch} != want) {
			t.Errorf("%v: %s %s %v", in, goos, goarch, ok)
		}
	}
}
