//go:build !windows

package ui

// setUpFonts leaves fonts to the desktop: only Windows has fonts installed with the
// application.
func setUpFonts() {}

// bundledFont returns "": there's no font installed with the application.
func bundledFont() string { return "" }
