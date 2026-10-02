// Package theme parses terminal colors and provides the stock GNOME Terminal palettes.
package theme

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/pkg/errors"
)

// ErrBadColor is returned for a color string which can't be parsed.
var ErrBadColor = errors.New("invalid color")

// Color is an RGBA color with components from 0 to 1.
type Color struct {
	R, G, B, A float64
}

const (
	maxByte  = 255
	hexBase  = 16
	hexShort = 3 // #rgb
	hexRGB   = 6 // #rrggbb
	hexRGBA  = 8 // #rrggbbaa
	rgbParts = 3
	rgbaArgs = 4
)

// ParseColor parses "#rgb", "#rrggbb", "#rrggbbaa", "rgb(r,g,b)" or "rgba(r,g,b,a)". The
// rgb() forms are what GNOME Terminal stores in its settings.
func ParseColor(s string) (Color, error) {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(s, "#"):
		return parseHex(s[1:])
	case strings.HasPrefix(s, "rgb(") || strings.HasPrefix(s, "rgba("):
		return parseRGBFunc(s)
	}
	return Color{}, errors.Wrap(ErrBadColor, s)
}

func parseHex(h string) (Color, error) {
	if len(h) == hexShort {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != hexRGB && len(h) != hexRGBA {
		return Color{}, errors.Wrap(ErrBadColor, "#"+h)
	}
	v, err := strconv.ParseUint(h, hexBase, 32)
	if err != nil {
		return Color{}, errors.Wrap(ErrBadColor, "#"+h)
	}
	if len(h) == hexRGB {
		v = v<<8 | maxByte
	}
	return Color{
		R: float64(v>>24&maxByte) / maxByte,
		G: float64(v>>16&maxByte) / maxByte,
		B: float64(v>>8&maxByte) / maxByte,
		A: float64(v&maxByte) / maxByte,
	}, nil
}

func parseRGBFunc(s string) (Color, error) {
	open, closing := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || closing < open {
		return Color{}, errors.Wrap(ErrBadColor, s)
	}
	parts := strings.Split(s[open+1:closing], ",")
	if len(parts) != rgbParts && len(parts) != rgbaArgs {
		return Color{}, errors.Wrap(ErrBadColor, s)
	}
	vals := make([]float64, len(parts))
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return Color{}, errors.Wrap(ErrBadColor, s)
		}
		vals[i] = f
	}
	c := Color{R: vals[0] / maxByte, G: vals[1] / maxByte, B: vals[2] / maxByte, A: 1}
	if len(vals) == rgbaArgs {
		c.A = vals[3]
	}
	return c, nil
}

// MustParseColor is ParseColor for constants. It panics on error.
func MustParseColor(s string) Color {
	c, err := ParseColor(s)
	if err != nil {
		panic(err)
	}
	return c
}

// ParsePalette parses a list of colors.
func ParsePalette(colors []string) ([]Color, error) {
	result := make([]Color, 0, len(colors))
	for _, s := range colors {
		c, err := ParseColor(s)
		if err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, nil
}

// Palettes are the built-in palettes of GNOME Terminal, by lower-case name.
//
//nolint:gochecknoglobals
var Palettes = map[string][]string{
	"gnome": {
		"#171421", "#c01c28", "#26a269", "#a2734c", "#12488b", "#a347ba", "#2aa1b3", "#d0cfcc",
		"#5e5c64", "#f66151", "#33d17a", "#e9ad0c", "#2a7bde", "#c061cb", "#33c7de", "#ffffff",
	},
	"tango": {
		"#000000", "#cc0000", "#4e9a06", "#c4a000", "#3465a4", "#75507b", "#06989a", "#d3d7cf",
		"#555753", "#ef2929", "#8ae234", "#fce94f", "#729fcf", "#ad7fa8", "#34e2e2", "#eeeeec",
	},
	"linux": {
		"#000000", "#aa0000", "#00aa00", "#aa5500", "#0000aa", "#aa00aa", "#00aaaa", "#aaaaaa",
		"#555555", "#ff5555", "#55ff55", "#ffff55", "#5555ff", "#ff55ff", "#55ffff", "#ffffff",
	},
	"xterm": {
		"#000000", "#cd0000", "#00cd00", "#cdcd00", "#0000ee", "#cd00cd", "#00cdcd", "#e5e5e5",
		"#7f7f7f", "#ff0000", "#00ff00", "#ffff00", "#5c5cff", "#ff00ff", "#00ffff", "#ffffff",
	},
	"solarized": {
		"#073642", "#dc322f", "#859900", "#b58900", "#268bd2", "#d33682", "#2aa198", "#eee8d5",
		"#002b36", "#cb4b16", "#586e75", "#657b83", "#839496", "#6c71c4", "#93a1a1", "#fdf6e3",
	},
}

// DefaultPalette is the palette used when nothing else is configured.
const DefaultPalette = "gnome"

// Luminance returns the relative luminance of a color, from 0 for black to 1 for white,
// as defined by WCAG.
func (c Color) Luminance() float64 {
	lin := func(v float64) float64 {
		if v <= 0.04045 { //nolint:mnd // sRGB transfer function
			return v / 12.92 //nolint:mnd
		}
		return math.Pow((v+0.055)/1.055, 2.4) //nolint:mnd
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B) //nolint:mnd
}

// IsDark reports whether a background color is dark, so text on it should be light.
func (c Color) IsDark() bool {
	return c.Luminance() < darkThreshold
}

// darkThreshold is the luminance at which black and white text have equal contrast.
const darkThreshold = 0.179

// Mix returns c moved towards other by amount, from 0 (c) to 1 (other). Alpha is kept.
func (c Color) Mix(other Color, amount float64) Color {
	return Color{
		R: c.R + (other.R-c.R)*amount,
		G: c.G + (other.G-c.G)*amount,
		B: c.B + (other.B-c.B)*amount,
		A: c.A,
	}
}

// Offset returns c a few shades away from itself: lighter if it's dark, darker if it's
// light. It keeps a surface distinct from a neighbor of color c while matching it.
func (c Color) Offset(amount float64) Color {
	if c.IsDark() {
		return c.Mix(Color{R: 1, G: 1, B: 1, A: 1}, amount)
	}
	return c.Mix(Color{R: 0, G: 0, B: 0, A: 1}, amount)
}

// CSS returns the color as a CSS rgba() value.
func (c Color) CSS() string {
	return fmt.Sprintf("rgba(%d,%d,%d,%.3f)",
		int(math.Round(c.R*maxByte)), int(math.Round(c.G*maxByte)), int(math.Round(c.B*maxByte)), c.A)
}
