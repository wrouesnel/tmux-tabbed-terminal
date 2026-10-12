package gtkx

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPixbufFit(t *testing.T) {
	cases := []struct {
		name         string
		w, h         int
		wantW, wantH int
	}{
		{"wide", 800, 400, 360, 180},
		{"tall", 300, 900, 90, 270},
		{"small, not enlarged", 10, 20, 10, 20},
		{"exact", 360, 270, 360, 270},
	}
	for _, c := range cases {
		pb, err := PixbufFit(pngOf(t, c.w, c.h), 360, 270)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if pb.GetWidth() != c.wantW || pb.GetHeight() != c.wantH {
			t.Errorf("%s: got %dx%d, want %dx%d", c.name, pb.GetWidth(), pb.GetHeight(), c.wantW, c.wantH)
		}
	}
	for _, bad := range [][]byte{nil, []byte("not an image at all"), pngOf(t, 4, 4)[:30]} {
		if _, err := PixbufFit(bad, 360, 270); err == nil {
			t.Errorf("%q: decoded", bad)
		}
	}
}
