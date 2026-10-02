package ui

import (
	"fmt"
	"time"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/gtk"
	"go.uber.org/zap"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/theme"
)

// stylesheet uses the theme's named colors, so it follows light and dark themes.
const stylesheet = `
.ttt-sidebar row.ttt-session {
	padding: 6px 10px 6px 6px;
}
.ttt-session-name {
	font-weight: normal;
}
.ttt-sidebar row.ttt-unseen .ttt-session-name {
	font-weight: bold;
}
.ttt-sidebar row.ttt-shown:not(:selected) {
	box-shadow: inset 3px 0 alpha(@theme_selected_bg_color, 0.6);
}
.ttt-session-subtitle {
	font-size: smaller;
}
@keyframes ttt-pulse {
	from { opacity: 1; }
	to { opacity: 0.2; }
}
.ttt-busy-dot {
	color: @theme_selected_bg_color;
	animation: ttt-pulse 1.1s ease-in-out infinite alternate;
}
.ttt-unseen-dot {
	color: @theme_selected_bg_color;
	font-size: smaller;
}
row:selected .ttt-busy-dot, row:selected .ttt-unseen-dot {
	color: @theme_selected_fg_color;
}
.ttt-group-header {
	padding: 10px 10px 2px 10px;
	font-size: smaller;
	font-weight: bold;
}
.ttt-search {
	margin: 6px;
}
.ttt-drop-zone {
	background-color: alpha(@theme_selected_bg_color, 0.25);
	border: 2px solid alpha(@theme_selected_bg_color, 0.8);
	border-radius: 4px;
}
.ttt-sidebar row.ttt-host {
	padding: 8px 4px 4px 8px;
}
.ttt-host-name {
	font-weight: bold;
}
.ttt-host-status {
	font-size: smaller;
}
.ttt-host-error .ttt-host-status {
	color: @error_color;
}
.ttt-host button {
	min-height: 16px;
	min-width: 16px;
	padding: 0 2px;
}
.ttt-sidebar-toolbar {
	padding: 2px;
}
.ttt-pane-header {
	padding: 1px 4px 1px 8px;
	background-color: @theme_bg_color;
	border-bottom: 1px solid @borders;
	opacity: 0.75;
}
.ttt-pane.ttt-active .ttt-pane-header {
	opacity: 1;
	box-shadow: inset 0 -2px @theme_selected_bg_color;
}
.ttt-pane-header button {
	min-height: 16px;
	min-width: 16px;
	padding: 0;
}
.ttt-empty-title {
	font-size: larger;
	font-weight: bold;
}
`

// installCSS adds the application stylesheet to the default screen.
func installCSS(log *zap.Logger) {
	provider, err := gtk.CssProviderNew()
	if err != nil {
		log.Warn("Could not create CSS provider", zap.Error(err))
		return
	}
	if err := provider.LoadFromData(stylesheet); err != nil {
		log.Warn("Could not load stylesheet", zap.Error(err))
		return
	}
	screen, err := gdk.ScreenGetDefault()
	if err != nil {
		log.Warn("No default screen for the stylesheet", zap.Error(err))
		return
	}
	gtk.AddProviderForScreen(screen, provider, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
}

// Offsets of the surfaces around the terminals from the terminal background: a few shades
// lighter on a dark terminal, darker on a light one.
const (
	surfaceOffset = 0.07
	hoverOffset   = 0.13
	borderOffset  = 0.18
)

// terminalStylesheet styles the session list, pane headers, split dividers and empty panes
// from the terminal colors, so they sit with the terminals rather than the GTK theme.
func terminalStylesheet(fg, bg theme.Color) string {
	fg.A, bg.A = 1, 1
	surface := bg.Offset(surfaceOffset).CSS()
	hover := bg.Offset(hoverOffset).CSS()
	border := bg.Offset(borderOffset).CSS()
	return fmt.Sprintf(`
.ttt-nav, .ttt-nav list, .ttt-nav scrolledwindow, .ttt-nav viewport, .ttt-nav .ttt-sidebar-toolbar {
	background-color: %[1]s;
	color: %[4]s;
}
.ttt-nav row.ttt-session:hover:not(:selected) {
	background-color: %[2]s;
}
.ttt-nav separator {
	background-color: %[3]s;
}
.ttt-nav button {
	color: %[4]s;
}
.ttt-nav button:hover {
	background-color: %[2]s;
}
.ttt-window paned > separator {
	background-color: %[3]s;
	background-image: none;
	border-color: %[3]s;
}
.ttt-pane-header {
	background-color: %[1]s;
	color: %[4]s;
	border-bottom-color: %[3]s;
}
.ttt-pane-header button {
	color: %[4]s;
}
/* The scrollbar sits on the terminal: its trough is the terminal background, its slider
   the text color, faint until used. */
.ttt-pane scrollbar {
	background-color: %[5]s;
	background-image: none;
	border: none;
}
.ttt-pane scrollbar slider {
	background-color: alpha(%[4]s, 0.3);
	min-width: 6px;
	border: none;
}
.ttt-pane scrollbar slider:hover {
	background-color: alpha(%[4]s, 0.5);
}
.ttt-pane scrollbar slider:active {
	background-color: @theme_selected_bg_color;
}
.ttt-empty {
	background-color: %[5]s;
	color: %[4]s;
}

/* The search box is a well cut into the list: the terminal background, a touch of the
   accent hue so it reads as an input, and an accent border that firms up on focus. The
   accent is the theme's selection color, which also marks busy sessions and the
   selected row, so every highlight in the list shares one hue. */
.ttt-nav entry.ttt-search {
	background-color: mix(@theme_selected_bg_color, %[5]s, 0.92);
	background-image: none;
	color: %[4]s;
	border: 1px solid mix(@theme_selected_bg_color, %[1]s, 0.7);
	box-shadow: none;
}
.ttt-nav entry.ttt-search:focus {
	border-color: @theme_selected_bg_color;
	box-shadow: inset 0 0 0 1px @theme_selected_bg_color;
}
.ttt-nav entry.ttt-search image {
	color: mix(@theme_selected_bg_color, %[4]s, 0.4);
}

/* Toggled buttons, such as grouping, take the accent mixed into the list surface. */
.ttt-nav button:checked {
	background-color: mix(@theme_selected_bg_color, %[1]s, 0.65);
	background-image: none;
	color: %[4]s;
	border-color: transparent;
	box-shadow: none;
}
.ttt-nav button:checked:hover {
	background-color: mix(@theme_selected_bg_color, %[1]s, 0.5);
}
`, surface, hover, border, fg.CSS(), bg.CSS())
}

// installTerminalCSS adds or replaces the stylesheet derived from the terminal colors.
func (a *App) installTerminalCSS() {
	fg, bg := a.appearance.Foreground, a.appearance.Background
	if a.appearance.UseThemeColors {
		probe, err := gtk.LabelNew("")
		if err != nil {
			return
		}
		fg, bg = themeColors(&probe.Widget)
	}
	if a.terminalCSS == nil {
		provider, err := gtk.CssProviderNew()
		if err != nil {
			a.log.Warn("Could not create CSS provider", zap.Error(err))
			return
		}
		screen, err := gdk.ScreenGetDefault()
		if err != nil {
			a.log.Warn("No default screen for the stylesheet", zap.Error(err))
			return
		}
		// Above the application stylesheet, so these colors win.
		gtk.AddProviderForScreen(screen, provider, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION+1)
		a.terminalCSS = provider
	}
	if err := a.terminalCSS.LoadFromData(terminalStylesheet(fg, bg)); err != nil {
		a.log.Warn("Could not load terminal stylesheet", zap.Error(err))
	}
}

// styled is any widget with a style context.
type styled interface {
	GetStyleContext() (*gtk.StyleContext, error)
}

// addClass adds a CSS class to a widget.
func addClass(w styled, class string) {
	if ctx, err := w.GetStyleContext(); err == nil {
		ctx.AddClass(class)
	}
}

// setClass adds or removes a CSS class.
func setClass(w styled, class string, on bool) {
	ctx, err := w.GetStyleContext()
	if err != nil {
		return
	}
	if on {
		ctx.AddClass(class)
	} else {
		ctx.RemoveClass(class)
	}
}

// timeNow is the clock, for activity states.
//
//nolint:gochecknoglobals
var timeNow = time.Now
