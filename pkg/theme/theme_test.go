package theme_test

import (
	"math"
	"testing"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/theme"
)

func near(a, b float64) bool { return math.Abs(a-b) < 0.002 }

func TestParseColor(t *testing.T) {
	cases := map[string]theme.Color{
		"#ff0000":               {R: 1, G: 0, B: 0, A: 1},
		"#0f0":                  {R: 0, G: 1, B: 0, A: 1},
		"#00000080":             {R: 0, G: 0, B: 0, A: 128.0 / 255},
		"rgb(23,20,33)":         {R: 23.0 / 255, G: 20.0 / 255, B: 33.0 / 255, A: 1},
		" rgba(0, 0, 255, 0.5)": {R: 0, G: 0, B: 1, A: 0.5},
	}
	for in, want := range cases {
		got, err := theme.ParseColor(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if !near(got.R, want.R) || !near(got.G, want.G) || !near(got.B, want.B) || !near(got.A, want.A) {
			t.Errorf("%q: got %+v, want %+v", in, got, want)
		}
	}
	for _, bad := range []string{"", "red", "#12", "#gggggg", "rgb(1,2)", "rgb(a,b,c)"} {
		if _, err := theme.ParseColor(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}

func TestPalettesParse(t *testing.T) {
	for name, colors := range theme.Palettes {
		pal, err := theme.ParsePalette(colors)
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if len(pal) != 16 {
			t.Errorf("%s: got %d colors, want 16", name, len(pal))
		}
	}
	if _, ok := theme.Palettes[theme.DefaultPalette]; !ok {
		t.Error("default palette is missing")
	}
}

func TestOffset(t *testing.T) {
	dark := theme.MustParseColor("#2e3436")
	light := theme.MustParseColor("#ffffff")
	if !dark.IsDark() || light.IsDark() {
		t.Fatalf("IsDark: dark=%v light=%v", dark.IsDark(), light.IsDark())
	}
	if got := dark.Offset(0.1); got.Luminance() <= dark.Luminance() {
		t.Errorf("dark offset %+v is not lighter than %+v", got, dark)
	}
	if got := light.Offset(0.1); got.Luminance() >= light.Luminance() {
		t.Errorf("light offset %+v is not darker than %+v", got, light)
	}
	if got := light.Offset(0.1).CSS(); got != "rgba(230,230,230,1.000)" {
		t.Errorf("CSS: got %s", got)
	}
}
