package agent

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os/exec"
	"strconv"
	"testing"
)

// pipePair starts an agent and a client over plain in-memory pipes, so that what a
// benchmark measures is the agent's protocol alone.
func pipePair(b *testing.B) *Client {
	b.Helper()
	toAgentR, toAgentW := io.Pipe()
	toClientR, toClientW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan struct{})
	go func() {
		_ = Serve(ctx, toAgentR, toClientW, "bench")
		_ = toClientW.Close()
		close(served)
	}()
	r := bufio.NewReader(toClientR)
	if _, err := readMarker(r); err != nil {
		b.Fatal(err)
	}
	c, err := NewClient(r, toAgentW, toAgentW)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := c.Hello(context.Background(), "bench"); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = c.Close()
		cancel()
		<-served
	})
	return c
}

// echoRounds writes a line of size bytes to w and reads it back from r, as a control
// client's command and reply do.
func echoRounds(b *testing.B, w io.Writer, r io.Reader, size int) {
	line := append(bytes.Repeat([]byte("x"), size-1), '\n')
	buf := make([]byte, size)
	b.SetBytes(int64(size))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := w.Write(line); err != nil {
			b.Fatal(err)
		}
		if _, err := io.ReadFull(r, buf); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPipeEcho echoes lines through cat run by the agent's Pipe, as a remote control
// client's commands and replies go.
func BenchmarkPipeEcho(b *testing.B) {
	for _, size := range []int{100, 12 << 10} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			c := pipePair(b)
			inR, inW := io.Pipe()
			outR, outW := io.Pipe()
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				_ = c.Pipe(ctx, inR, outW, []string{"cat"})
				close(done)
			}()
			b.Cleanup(func() { cancel(); _ = inW.Close(); _ = outR.Close(); <-done })
			echoRounds(b, inW, outR, size)
		})
	}
}

// BenchmarkDirectEcho is BenchmarkPipeEcho with cat run directly, as ssh's standard input
// and output would be read without the agent.
func BenchmarkDirectEcho(b *testing.B) {
	for _, size := range []int{100, 12 << 10} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			cmd := exec.Command("cat")
			w, _ := cmd.StdinPipe()
			r, _ := cmd.StdoutPipe()
			if err := cmd.Start(); err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = w.Close(); _ = cmd.Wait() })
			echoRounds(b, w, r, size)
		})
	}
}
