package pty

import (
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestConPty runs cmd.exe in a pseudo console. Reading ends when it exits, since the
// console is closed then.
func TestConPty(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/c", "echo hello-conpty")
	p, err := Start(cmd, 80, 25)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Resize(100, 30); err != nil {
		t.Errorf("Resize: %v", err)
	}
	done := make(chan []byte)
	go func() {
		out, _ := io.ReadAll(p)
		done <- out
	}()
	select {
	case out := <-done:
		if !strings.Contains(string(out), "hello-conpty") {
			t.Errorf("output %q", out)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("reading didn't end after the program exited")
	}
	if err := cmd.Wait(); err != nil {
		t.Errorf("Wait: %v", err)
	}
}
