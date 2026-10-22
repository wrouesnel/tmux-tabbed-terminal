package ui

import (
	"sort"
	"strconv"
	"strings"

	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/pkg/errors"
)

// accelerators are the keyboard shortcuts, by detailed action name. They follow GNOME
// Terminal where it has an equivalent, and Terminator for splits.
//
//nolint:gochecknoglobals
var accelerators = func() map[string][]string {
	accels := map[string][]string{
		"app.new-window":     {"<Primary><Shift>n"},
		"app.preferences":    {"<Primary>comma"},
		"win.close-window":   {"<Primary><Shift>q"},
		"win.new-session":    {"<Primary><Shift>t"},
		"win.close-pane":     {"<Primary><Shift>w"},
		"win.copy":           {"<Primary><Shift>c"},
		"win.paste":          {"<Primary><Shift>v"},
		"win.split-right":    {"<Primary><Shift>e"},
		"win.split-down":     {"<Primary><Shift>o"},
		"win.next-session":   {"<Primary>Page_Down"},
		"win.prev-session":   {"<Primary>Page_Up"},
		"win.next-pane":      {"<Primary>Tab"},
		"win.prev-pane":      {"<Primary><Shift>Tab", "<Primary><Shift>ISO_Left_Tab"},
		"win.zoom-in":        {"<Primary>plus", "<Primary>equal", "<Primary>KP_Add"},
		"win.zoom-out":       {"<Primary>minus", "<Primary>KP_Subtract"},
		"win.zoom-normal":    {"<Primary>0", "<Primary>KP_0"},
		"win.show-sidebar":   {"<Primary><Shift>s"},
		"win.find-session":   {"<Primary><Shift>f"},
		"win.fullscreen":     {"F11"},
		"win.rename-session": {"<Primary><Shift>r"},
	}
	for i := 1; i <= tabShortcuts; i++ {
		accels["win.switch-to-"+strconv.Itoa(i)] = []string{"<Alt>" + strconv.Itoa(i)}
	}
	return accels
}()

// shortcuts returns the keyboard shortcuts with overrides, from the configuration file,
// in place of the defaults, and an error for each override which can't be used: an
// unknown action or an accelerator GTK can't parse. Unusable ones are skipped.
func shortcuts(overrides map[string][]string) (map[string][]string, []error) {
	result := make(map[string][]string, len(accelerators))
	for action, accels := range accelerators {
		result[action] = accels
	}
	names := make([]string, 0, len(overrides))
	for name := range overrides {
		names = append(names, name)
	}
	sort.Strings(names)

	var errs []error
	for _, name := range names {
		action, ok := shortcutAction(name)
		if !ok {
			errs = append(errs, errors.Errorf("keyboard shortcut for unknown action %q", name))
			continue
		}
		accels := []string{}
		for _, accel := range overrides[name] {
			if key, mods := gtk.AcceleratorParse(accel); key == 0 && mods == 0 {
				errs = append(errs, errors.Errorf("keyboard shortcut %q for %s isn't a GTK accelerator", accel, name))
				continue
			}
			accels = append(accels, accel)
		}
		result[action] = accels
	}
	return result, errs
}

// shortcutAction returns the detailed action name of a shortcut's key in the
// configuration: a full name as it is, or the short name of an action with a default
// shortcut.
func shortcutAction(name string) (string, bool) {
	if strings.HasPrefix(name, "app.") || strings.HasPrefix(name, "win.") {
		return name, true
	}
	for _, prefix := range []string{"win.", "app."} {
		if _, ok := accelerators[prefix+name]; ok {
			return prefix + name, true
		}
	}
	return "", false
}

// section builds a menu section from label and detailed action pairs.
func section(items ...string) *glib.MenuModel {
	menu := glib.MenuNew()
	for i := 0; i+1 < len(items); i += 2 {
		menu.Append(items[i], items[i+1])
	}
	return &menu.MenuModel
}

// menuOf joins sections into a menu.
func menuOf(sections ...*glib.MenuModel) *glib.MenuModel {
	menu := glib.MenuNew()
	for _, s := range sections {
		menu.AppendSectionWithoutLabel(s)
	}
	return &menu.MenuModel
}

// mainMenu is the menu behind the header bar's menu button.
func mainMenu() *glib.MenuModel {
	return menuOf(
		section("New Window", "app.new-window", "New Session", "win.new-session"),
		section("Copy", "win.copy", "Paste", "win.paste"),
		section("Split Right", "win.split-right", "Split Down", "win.split-down", "Close Pane", "win.close-pane"),
		section("Rename Session…", "win.rename-session", "Kill Session…", "win.kill-session"),
		section("Zoom In", "win.zoom-in", "Zoom Out", "win.zoom-out", "Normal Size", "win.zoom-normal"),
		section("Find Session…", "win.find-session", "Group Sessions by Application", "win.group-sessions"),
		section("Show Sessions", "win.show-sidebar", "Show Tab Bar", "win.show-tabs",
			"Full Screen", "win.fullscreen"),
		section("Preferences", "app.preferences", "About", "app.about"),
	)
}

// terminalMenu is the context menu of a terminal, with link items when it was opened on
// a link.
func terminalMenu(onLink bool) *glib.MenuModel {
	var sections []*glib.MenuModel
	if onLink {
		sections = append(sections, section("Open Link", "win.open-link", "Download", "win.download-link",
			"Copy Link", "win.copy-link"))
	}
	return menuOf(append(sections,
		section("Copy", "win.copy", "Paste", "win.paste"),
		section("Split Right", "win.split-right", "Split Down", "win.split-down"),
		section("Save Scrollback", "win.save-scrollback", "Save Scrollback As…", "win.save-scrollback-as",
			"Scrollback Dir…", "win.scrollback-dir"),
		section("New Session", "win.new-session", "Close Pane", "win.close-pane"),
	)...)
}
