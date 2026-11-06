package termwidget

import (
	"testing"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/termwidget/emulator"
)

func TestHashLine(t *testing.T) {
	e := emulator.New(10, 3)
	hashes := func() [3]uint64 {
		return [3]uint64{hashLine(e.Line(0)), hashLine(e.Line(1)), hashLine(e.Line(2))}
	}
	before := hashes()
	if before[0] != before[1] {
		t.Fatal("blank lines hash differently")
	}
	for _, tc := range []struct {
		name  string
		input string
		row   int
	}{
		{"text", "\x1b[2;1Hab", 1},
		{"style", "\x1b[2;1H\x1b[1mab", 1},
		{"color", "\x1b[2;1H\x1b[31mab", 1},
		{"combining", "\x1b[3;1Hé", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _ = e.Write([]byte(tc.input))
			after := hashes()
			for y := range after {
				if changed := after[y] != before[y]; changed != (y == tc.row) {
					t.Errorf("row %d changed = %v", y, changed)
				}
			}
			before = after
		})
	}
}
