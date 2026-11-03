package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Benchmarks of the agent over links with a one-way delay, as a network adds, so each
// reports how many round trips an operation costs as well as its time. Run them with
// go test -run '^$' -bench . ./pkg/agent.

// benchDelays are the one-way delays benchmarks run over: none, and a long-distance
// network's.
var benchDelays = []time.Duration{0, 25 * time.Millisecond}

// link is one direction of a connection which delivers what's written to it after a
// delay, at a limited rate if rate isn't 0, buffering without limit, as a network does.
type link struct {
	delay time.Duration
	// rate is in bytes a second.
	rate float64

	mu sync.Mutex
	// free is when the link has sent everything written so far.
	free   time.Time
	chunks chan linkChunk
	r      *io.PipeReader
	w      *io.PipeWriter
}

type linkChunk struct {
	data []byte
	at   time.Time
}

func newLink(delay time.Duration, rate float64) *link {
	r, w := io.Pipe()
	l := &link{delay: delay, rate: rate, chunks: make(chan linkChunk, 1<<16), r: r, w: w}
	go func() {
		for c := range l.chunks {
			time.Sleep(time.Until(c.at))
			if _, err := w.Write(c.data); err != nil {
				return
			}
		}
		_ = w.Close()
	}()
	return l
}

func (l *link) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	sent := time.Now()
	if l.rate > 0 {
		sent = later(sent, l.free).Add(time.Duration(float64(len(p)) / l.rate * float64(time.Second)))
		l.free = sent
	}
	l.chunks <- linkChunk{data: bytes.Clone(p), at: sent.Add(l.delay)}
	return len(p), nil
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func (l *link) Close() error {
	close(l.chunks)
	return nil
}

// benchPair starts an agent and a client talking over links with the given one-way delay.
func benchPair(b *testing.B, delay time.Duration) *Client {
	return benchPairRate(b, delay, 0)
}

// benchPairRate is benchPair over links limited to rate bytes a second.
func benchPairRate(b *testing.B, delay time.Duration, rate float64) *Client {
	b.Helper()
	if runtime.GOOS == "windows" {
		b.Skip("the agent runs on Unix hosts")
	}
	toAgent, toClient := newLink(delay, rate), newLink(delay, rate)
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan struct{})
	go func() {
		_ = Serve(ctx, toAgent.r, toClient, "bench")
		_ = toClient.Close()
		close(served)
	}()
	c, err := NewClient(toClient.r, toAgent, toAgent)
	if err != nil {
		b.Fatal(err)
	}
	// The connection's set up before timing starts.
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

// reportRoundTrips reports the time per operation in round trips of the link.
func reportRoundTrips(b *testing.B, delay time.Duration) {
	if delay > 0 {
		b.ReportMetric(float64(b.Elapsed())/float64(b.N)/float64(2*delay), "rtts/op")
	}
}

func forDelays(b *testing.B, f func(b *testing.B, delay time.Duration)) {
	for _, d := range benchDelays {
		b.Run("delay="+d.String(), func(b *testing.B) { f(b, d) })
	}
}

func BenchmarkHello(b *testing.B) {
	forDelays(b, func(b *testing.B, delay time.Duration) {
		c := benchPair(b, delay)
		for b.Loop() {
			if _, err := c.Hello(context.Background(), "bench"); err != nil {
				b.Fatal(err)
			}
		}
		reportRoundTrips(b, delay)
	})
}

// BenchmarkRun runs a program which does nothing, as a poll runs tmux.
func BenchmarkRun(b *testing.B) {
	forDelays(b, func(b *testing.B, delay time.Duration) {
		c := benchPair(b, delay)
		for b.Loop() {
			if _, err := c.Run(context.Background(), []string{"true"}, nil); err != nil {
				b.Fatal(err)
			}
		}
		reportRoundTrips(b, delay)
	})
}

// benchSizes are the sizes of output the throughput benchmarks read.
var benchSizes = []int{64 << 10, 16 << 20}

// BenchmarkRunOutput runs a program with much output, as previewing a file does.
func BenchmarkRunOutput(b *testing.B) {
	for _, size := range benchSizes {
		b.Run("size="+strconv.Itoa(size), func(b *testing.B) {
			forDelays(b, func(b *testing.B, delay time.Duration) {
				c := benchPair(b, delay)
				b.SetBytes(int64(size))
				for b.Loop() {
					res, err := c.Run(context.Background(), []string{"head", "-c", strconv.Itoa(size), "/dev/zero"}, nil)
					if err != nil || len(res.Stdout) != size {
						b.Fatal(err)
					}
				}
				reportRoundTrips(b, delay)
			})
		})
	}
}

// BenchmarkStream streams a program's output, as downloading a file does.
func BenchmarkStream(b *testing.B) {
	for _, size := range benchSizes {
		b.Run("size="+strconv.Itoa(size), func(b *testing.B) {
			forDelays(b, func(b *testing.B, delay time.Duration) {
				c := benchPair(b, delay)
				b.SetBytes(int64(size))
				for b.Loop() {
					var n countWriter
					if err := c.Stream(context.Background(), &n, []string{"head", "-c", strconv.Itoa(size), "/dev/zero"}); err != nil || int(n) != size {
						b.Fatal(n, err)
					}
				}
				reportRoundTrips(b, delay)
			})
		})
	}
}

type countWriter int

func (w *countWriter) Write(p []byte) (int, error) {
	*w += countWriter(len(p))
	return len(p), nil
}

// attachCat attaches a terminal running cat, waiting until it's ready.
func attachCat(b *testing.B, c *Client) *Terminal {
	b.Helper()
	term, err := c.Attach(context.Background(), []string{"sh", "-c", "stty raw -echo; echo ready; exec cat"}, TerminalOptions{Cols: 80, Rows: 24})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = term.Close() })
	readUntil(b, term, "ready")
	return term
}

func readUntil(b *testing.B, r io.Reader, want string) {
	b.Helper()
	var got []byte
	buf := make([]byte, 64<<10)
	for !bytes.Contains(got, []byte(want)) {
		n, err := r.Read(buf)
		if err != nil {
			b.Fatalf("reading %q: %v, after %q", want, err, got)
		}
		got = append(got, buf[:n]...)
	}
}

// BenchmarkAttachEcho types a key into a terminal and reads it back, as echo does.
func BenchmarkAttachEcho(b *testing.B) {
	forDelays(b, func(b *testing.B, delay time.Duration) {
		term := attachCat(b, benchPair(b, delay))
		buf := make([]byte, 16)
		for b.Loop() {
			if _, err := term.Write([]byte("x")); err != nil {
				b.Fatal(err)
			}
			if n, err := term.Read(buf); err != nil || n != 1 {
				b.Fatal(n, err)
			}
		}
		reportRoundTrips(b, delay)
	})
}

// BenchmarkAttachOutput reads a terminal's output, as a program printing much does.
func BenchmarkAttachOutput(b *testing.B) {
	const size = 16 << 20
	forDelays(b, func(b *testing.B, delay time.Duration) {
		c := benchPair(b, delay)
		b.SetBytes(size)
		for b.Loop() {
			term, err := c.Attach(context.Background(), []string{"head", "-c", fmt.Sprint(size), "/dev/zero"}, TerminalOptions{Cols: 80, Rows: 24})
			if err != nil {
				b.Fatal(err)
			}
			var n countWriter
			if _, err := io.Copy(&n, term); err != nil || n != size {
				b.Fatal(n, err)
			}
		}
		reportRoundTrips(b, delay)
	})
}

// BenchmarkAttachEchoDuringDownload types into a terminal while a download shares the
// connection, over a link of 10 Mbit/s: the echo waits behind whatever of the download is
// queued ahead of it.
func BenchmarkAttachEchoDuringDownload(b *testing.B) {
	const rate = 10e6 / 8
	forDelays(b, func(b *testing.B, delay time.Duration) {
		c := benchPairRate(b, delay, rate)
		term := attachCat(b, c)
		ctx, cancel := context.WithCancel(context.Background())
		downloaded := make(chan struct{})
		go func() {
			defer close(downloaded)
			_ = c.Stream(ctx, io.Discard, []string{"cat", "/dev/zero"})
		}()
		b.Cleanup(func() {
			cancel()
			<-downloaded
		})
		// The download gets going.
		time.Sleep(time.Second)
		buf := make([]byte, 16)
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
		reportRoundTrips(b, delay)
	})
}
