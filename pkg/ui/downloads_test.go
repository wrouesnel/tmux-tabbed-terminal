package ui

import (
	"bytes"
	"errors"
	"testing"
)

func TestFormatSize(t *testing.T) {
	for n, want := range map[int64]string{
		0: "0 bytes", 1: "1 byte", 999: "999 bytes", 1000: "1.0 kB", 1234: "1.2 kB",
		5_000_000: "5.0 MB", 34_500_000_000: "34.5 GB",
	} {
		if got := formatSize(n); got != want {
			t.Errorf("formatSize(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestDownloadProgress(t *testing.T) {
	d := newDownload("big.iso", "buildbox", func() {})
	if d.fraction() != -1 || d.progressText() != "Starting…" {
		t.Errorf("before the size is known: %v, %q", d.fraction(), d.progressText())
	}
	d.total.Store(4_000_000)
	var out bytes.Buffer
	w := countingWriter{w: &out, n: &d.received}
	if _, err := w.Write(make([]byte, 1_000_000)); err != nil {
		t.Fatal(err)
	}
	if d.fraction() != 0.25 || d.progressText() != "1.0 MB of 4.0 MB" {
		t.Errorf("a quarter in: %v, %q", d.fraction(), d.progressText())
	}

	d.state = downloadDone
	if d.fraction() != 1 {
		t.Errorf("done: fraction %v", d.fraction())
	}
	d.state, d.err = downloadFailed, errors.New("connection lost")
	if d.progressText() != "Failed: connection lost" {
		t.Errorf("failed: %q", d.progressText())
	}
	empty := newDownload("empty", "h", func() {})
	empty.total.Store(0)
	if empty.fraction() != 0 {
		t.Errorf("empty file: %v", empty.fraction())
	}
}

func TestDownloadListTop(t *testing.T) {
	l := &downloadList{app: &App{}}
	older, newer := newDownload("a", "h", func() {}), newDownload("b", "h", func() {})
	l.add(older)
	l.add(newer)
	if l.top() != newer {
		t.Error("the top download isn't the newest running")
	}
	newer.state = downloadDone
	if l.top() != older {
		t.Error("a finished download is still on top")
	}
	l.clearFinished()
	if len(l.items) != 1 || l.items[0] != older {
		t.Errorf("clearing finished left %d", len(l.items))
	}
	older.state = downloadCancelled
	if l.top() != nil {
		t.Error("a cancelled download is on top")
	}
}

func TestMiddleEllipsis(t *testing.T) {
	for in, want := range map[string]string{
		"short.txt":                      "short.txt",
		"a-very-long-name-indeed.tar.gz": "a-very-lo…d.tar.gz",
		"ünïcödé-ünïcödé-ünïcödé.bin":    "ünïcödé-ü…cödé.bin",
	} {
		if got := middleEllipsis(in, 18); got != want || len([]rune(got)) > 18 {
			t.Errorf("middleEllipsis(%q) = %q, want %q", in, got, want)
		}
	}
}
