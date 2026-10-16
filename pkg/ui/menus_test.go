package ui

import (
	"slices"
	"testing"
)

func TestShortcuts(t *testing.T) {
	got, errs := shortcuts(map[string][]string{
		"paste":               {"<Control><Shift>v", "<Shift>Insert"},
		"split-right":         {},
		"win.save-scrollback": {"<Control><Shift>p"},
		"no-such-action":      {"<Control>x"},
		"copy":                {"<Control><Shift>c", "not a key<"},
	})

	if want := []string{"<Control><Shift>v", "<Shift>Insert"}; !slices.Equal(got["win.paste"], want) {
		t.Errorf("paste: got %q, want %q", got["win.paste"], want)
	}
	if len(got["win.split-right"]) != 0 {
		t.Errorf("split-right: got %q, want none", got["win.split-right"])
	}
	if want := []string{"<Control><Shift>p"}; !slices.Equal(got["win.save-scrollback"], want) {
		t.Errorf("full action name: got %q, want %q", got["win.save-scrollback"], want)
	}
	if want := []string{"<Control><Shift>c"}; !slices.Equal(got["win.copy"], want) {
		t.Errorf("copy keeps its parsable shortcut: got %q, want %q", got["win.copy"], want)
	}
	if !slices.Equal(got["app.new-window"], accelerators["app.new-window"]) {
		t.Errorf("an action not overridden lost its default: got %q", got["app.new-window"])
	}
	if _, ok := got["win.no-such-action"]; ok {
		t.Error("an unknown action got a shortcut")
	}
	if len(errs) != 2 {
		t.Errorf("got %d errors, want 2 (unknown action, bad accelerator): %v", len(errs), errs)
	}
	if !slices.Equal(accelerators["win.paste"], []string{"<Primary><Shift>v"}) {
		t.Errorf("the defaults were changed: %q", accelerators["win.paste"])
	}
}
