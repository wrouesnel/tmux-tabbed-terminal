package tmux

import "testing"

func TestCaptureCrashes(t *testing.T) {
	for version, want := range map[string]bool{
		"next-3.4": true, "next-3.4\n": true, "3.3a": false, "3.4": false, "2.7": false, "next-3.6": false,
	} {
		if got := captureCrashes(version); got != want {
			t.Errorf("%q: got %v, want %v", version, got, want)
		}
	}
}
