package sessionlist

import (
	"strings"
	"testing"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

func TestLowerLess(t *testing.T) {
	words := []string{"", "a", "A", "ab", "Ab", "b", "B", "claude", "Claude", "codex", "Émile", "émile", "zed", "Z", "a b", "a-b"}
	for _, a := range words {
		for _, b := range words {
			if got, want := lowerLess(a, b), strings.ToLower(a) < strings.ToLower(b); got != want {
				t.Errorf("lowerLess(%q, %q) = %v, want %v", a, b, got, want)
			}
		}
	}
}

func TestAppendOrderAllocates(t *testing.T) {
	groups := map[string]string{"$1": ShellGroup, "$2": "claude", "$3": "Codex"}
	sessions := []*tmux.Session{{ID: "$1"}, {ID: "$2"}, {ID: "$3"}}
	entries := AppendOrder(nil, sessions, groups, true)
	allocs := testing.AllocsPerRun(100, func() {
		entries = AppendOrder(entries[:0], sessions, groups, true)
	})
	if allocs != 0 {
		t.Errorf("AppendOrder into a slice with room allocated %v times", allocs)
	}
}
