package vte_test

import (
	"testing"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/vte"
)

// TestSixelSupported checks the feature check runs against the installed VTE, whichever
// way it was built. It needs no display.
func TestSixelSupported(t *testing.T) {
	t.Logf("VTE built with SIXEL support: %v", vte.SixelSupported())
}
