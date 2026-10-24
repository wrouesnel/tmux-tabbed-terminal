package pty

import (
	"bytes"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// msysShell returns MSYS2's or Cygwin's sh, if it's installed with script and stty.
func msysShell(t *testing.T) string {
	t.Helper()
	for _, sh := range []string{"sh", `C:\msys64\usr\bin\sh.exe`, `C:\cygwin64\bin\sh.exe`} {
		if path, err := exec.LookPath(sh); err == nil {
			if _, _, ok := cygwinTools(path); ok {
				return path
			}
		}
	}
	t.Skip("no MSYS2 or Cygwin sh with script and stty")
	return ""
}

// TestCygwinPty runs MSYS2's sh on a Cygwin pty, which has a name and is resized.
func TestCygwinPty(t *testing.T) {
	cmd := exec.Command(msysShell(t), "-c", `stty size; tty; read x; stty size`)
	p, err := Start(cmd, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if !strings.HasPrefix(p.Name(), "/dev/pty") {
		t.Errorf("Name() = %q", p.Name())
	}
	var mu sync.Mutex
	var out bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 1024)
		for {
			n, err := p.Read(buf)
			mu.Lock()
			out.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	waitFor := func(s string) {
		t.Helper()
		for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			mu.Lock()
			got := out.String()
			mu.Unlock()
			if strings.Contains(got, s) {
				return
			}
		}
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("no %q in output %q", s, out.String())
	}
	waitFor("24 80")
	waitFor(p.Name())
	if strings.Contains(out.String(), "7799") {
		t.Errorf("the marker was left in the output: %q", out.String())
	}
	if err := p.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	// stty runs in the background: give it time before the shell reads the size.
	time.Sleep(2 * time.Second)
	if _, err := io.WriteString(p, "\r"); err != nil {
		t.Fatal(err)
	}
	waitFor("30 100")
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("reading didn't end after the program exited")
	}
	if err := cmd.Wait(); err != nil {
		t.Errorf("Wait: %v", err)
	}
}

// TestCygwinPtyClose hangs a program up.
func TestCygwinPtyClose(t *testing.T) {
	cmd := exec.Command(msysShell(t), "-c", "sleep 600")
	p, err := Start(cmd, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	waited := make(chan error)
	go func() { waited <- cmd.Wait() }()
	_ = p.Close()
	select {
	case <-waited:
	case <-time.After(20 * time.Second):
		t.Fatal("the program didn't end when its pty was closed")
	}
}
