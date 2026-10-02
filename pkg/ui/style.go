package ui

import (
	"time"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/gtk"
	"go.uber.org/zap"
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
.ttt-unseen-dot {
	color: @theme_selected_bg_color;
	font-size: smaller;
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
