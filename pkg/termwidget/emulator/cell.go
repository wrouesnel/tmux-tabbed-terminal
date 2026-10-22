package emulator

import (
	"unicode"

	"golang.org/x/text/width"
)

// Color is a cell's foreground or background color: the default, an index into the
// 256-color palette, or a 24-bit RGB value.
type Color uint32

const (
	colorKindMask  Color = 0xff << 24
	colorKindIndex Color = 1 << 24
	colorKindRGB   Color = 2 << 24
)

// DefaultColor is the terminal's default foreground or background.
const DefaultColor Color = 0

// IndexColor returns palette color i.
func IndexColor(i uint8) Color { return colorKindIndex | Color(i) }

// RGBColor returns a 24-bit color.
func RGBColor(r, g, b uint8) Color {
	return colorKindRGB | Color(r)<<16 | Color(g)<<8 | Color(b)
}

// IsDefault reports whether c is the default color.
func (c Color) IsDefault() bool { return c&colorKindMask == 0 }

// Index returns the palette index of an indexed color.
func (c Color) Index() (uint8, bool) {
	return uint8(c), c&colorKindMask == colorKindIndex //nolint:gosec // the low byte is the index
}

// RGB returns the components of an RGB color.
func (c Color) RGB() (uint8, uint8, uint8, bool) {
	//nolint:gosec // each component is one byte of c
	return uint8(c >> 16), uint8(c >> 8), uint8(c), c&colorKindMask == colorKindRGB
}

// Flags are a cell's text attributes.
type Flags uint16

// Text attributes, as SGR sets them.
const (
	Bold Flags = 1 << iota
	Dim
	Italic
	Underline
	Blink
	Reverse
	Invisible
	Strike
)

// Style is how a cell's text is drawn, and the hyperlink it belongs to.
type Style struct {
	Fg, Bg Color
	Flags  Flags
	// Link is the hyperlink's ID for Emulator.Link, or 0 for none.
	Link uint32
}

// Cell is one character cell of the screen.
type Cell struct {
	// Rune is the character, a space in a blank cell.
	Rune rune
	// Combining are combining marks drawn over Rune.
	Combining []rune
	// Width is 1, or 2 for a wide character, whose right half is the next cell, which has
	// width 0.
	Width uint8
	Style
}

// Text returns the cell's character with its combining marks, or "" for the right half
// of a wide character.
func (c *Cell) Text() string {
	if c.Width == 0 {
		return ""
	}
	if len(c.Combining) == 0 {
		return string(c.Rune)
	}
	return string(append([]rune{c.Rune}, c.Combining...))
}

// blank returns an erased cell, which keeps the background color of style.
func blank(style Style) Cell {
	return Cell{Rune: ' ', Width: 1, Style: Style{Bg: style.Bg}}
}

// Line is one row of the screen.
type Line struct {
	Cells []Cell
	// Wrapped is set when the text ran on to the next line because it reached the right
	// margin, rather than ending with a newline.
	Wrapped bool
}

func newLine(cols int, style Style) Line {
	l := Line{Cells: make([]Cell, cols)}
	for i := range l.Cells {
		l.Cells[i] = blank(style)
	}
	return l
}

// RuneWidth is how many cells a character takes: two for East Asian wide ones, none for
// combining marks and other zero-width characters.
func RuneWidth(r rune) int {
	switch {
	case r < 0x300:
		return 1
	case unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf), r == 0x200b:
		return 0
	}
	switch width.LookupRune(r).Kind() { //nolint:exhaustive // the rest are narrow
	case width.EastAsianWide, width.EastAsianFullwidth:
		return 2 //nolint:mnd // a wide character
	}
	return 1
}
