package pty

import (
	"bytes"
	"io"
	"os/exec"
	"strings"
	"testing"
)

func TestStartReadsOutputAndSize(t *testing.T) {
	cmd := exec.Command("sh", "-c", "stty size; tty; read line; echo got:$line")
	p, err := Start(cmd, 77, 13)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if !strings.HasPrefix(p.Name(), "/dev/pts/") {
		t.Errorf("name %q", p.Name())
	}
	if _, err := p.Write([]byte("hi\r")); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	_, err = io.Copy(&out, p)
	if err != nil {
		t.Fatalf("reading after exit: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"13 77", p.Name(), "got:hi"} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q lacks %q", got, want)
		}
	}
}

func TestResize(t *testing.T) {
	cmd := exec.Command("sh", "-c", "read line; stty size")
	p, err := Start(cmd, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	_, _ = p.Write([]byte("\r"))
	out, _ := io.ReadAll(p)
	_ = cmd.Wait()
	if !strings.Contains(string(out), "30 100") {
		t.Errorf("output %q", out)
	}
}
