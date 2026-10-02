package ui

import (
	"fmt"

	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/pkg/errors"
	"go.uber.org/zap"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/theme"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/vte"
)

// GSettings schemas read for the GNOME look.
const (
	schemaInterface    = "org.gnome.desktop.interface"
	schemaProfilesList = "org.gnome.Terminal.ProfilesList"
	schemaProfile      = "org.gnome.Terminal.Legacy.Profile"
	profilePathFormat  = "/org/gnome/terminal/legacy/profiles:/:%s/"
	fallbackFont       = "Monospace 11"
)

// Appearance is the resolved look of every terminal.
type Appearance struct {
	Font string
	// UseThemeColors takes the foreground and background from the GTK theme.
	UseThemeColors bool
	Foreground     theme.Color
	Background     theme.Color
	Palette        []theme.Color
	BoldIsBright   bool
	// Cursor and CursorForeground are nil to use the default cursor colors.
	Cursor           *theme.Color
	CursorForeground *theme.Color
	AudibleBell      bool
	ScrollbackLines  int
}

// hasSchema reports whether a GSettings schema is installed. glib aborts the process if
// asked for a schema which isn't.
func hasSchema(id string) bool {
	source := glib.SettingsSchemaSourceGetDefault()
	return source != nil && source.Lookup(id, true) != nil
}

// gnomeTerminalProfile returns the settings of GNOME Terminal's default profile, or nil.
func gnomeTerminalProfile() *glib.Settings {
	if !hasSchema(schemaProfilesList) || !hasSchema(schemaProfile) {
		return nil
	}
	uuid := glib.SettingsNew(schemaProfilesList).GetString("default")
	if uuid == "" {
		return nil
	}
	return glib.SettingsNewWithPath(schemaProfile, fmt.Sprintf(profilePathFormat, uuid))
}

// systemMonospaceFont returns the desktop's monospace font.
func systemMonospaceFont() string {
	if hasSchema(schemaInterface) {
		if font := glib.SettingsNew(schemaInterface).GetString("monospace-font-name"); font != "" {
			return font
		}
	}
	return fallbackFont
}

// ResolveAppearance works out the terminal look from the configuration, GNOME Terminal's
// default profile and the desktop settings, in that order of precedence.
func ResolveAppearance(cfg AppearanceConfig, log *zap.Logger) *Appearance {
	app := &Appearance{
		Font:            systemMonospaceFont(),
		UseThemeColors:  true,
		ScrollbackLines: cfg.ScrollbackLines,
	}
	app.Palette, _ = theme.ParsePalette(theme.Palettes[theme.DefaultPalette])

	if cfg.UseGnomeTerminalProfile {
		if profile := gnomeTerminalProfile(); profile != nil {
			applyProfile(app, profile, log)
		}
	}

	if cfg.Font != "" {
		app.Font = cfg.Font
	}
	if cfg.Foreground != "" || cfg.Background != "" {
		fg, ferr := theme.ParseColor(cfg.Foreground)
		bg, berr := theme.ParseColor(cfg.Background)
		if ferr != nil || berr != nil {
			log.Warn("Foreground and background must both be valid colors: using theme colors",
				zap.String("foreground", cfg.Foreground), zap.String("background", cfg.Background))
		} else {
			app.UseThemeColors, app.Foreground, app.Background = false, fg, bg
		}
	}
	if len(cfg.Palette) > 0 {
		if pal, err := paletteFromConfig(cfg.Palette); err != nil {
			log.Warn("Ignoring invalid palette", zap.Error(err))
		} else {
			app.Palette = pal
		}
	}
	if cfg.BoldIsBright != nil {
		app.BoldIsBright = *cfg.BoldIsBright
	}
	return app
}

// paletteFromConfig parses a palette name or a list of colors.
func paletteFromConfig(p []string) ([]theme.Color, error) {
	if len(p) == 1 {
		if named, ok := theme.Palettes[p[0]]; ok {
			return theme.ParsePalette(named)
		}
	}
	pal, err := theme.ParsePalette(p)
	if err != nil {
		return nil, err
	}
	if len(pal) != paletteSize {
		return nil, errors.Errorf("palette has %d colors: want %d or a palette name", len(pal), paletteSize)
	}
	return pal, nil
}

const paletteSize = 16

// applyProfile copies the settings of a GNOME Terminal profile.
func applyProfile(app *Appearance, profile *glib.Settings, log *zap.Logger) {
	if !profile.GetBoolean("use-system-font") {
		if font := profile.GetString("font"); font != "" {
			app.Font = font
		}
	}
	if !profile.GetBoolean("use-theme-colors") {
		fg, ferr := theme.ParseColor(profile.GetString("foreground-color"))
		bg, berr := theme.ParseColor(profile.GetString("background-color"))
		if ferr == nil && berr == nil {
			app.UseThemeColors, app.Foreground, app.Background = false, fg, bg
		}
	}
	if pal, err := theme.ParsePalette(profile.GetStrv("palette")); err == nil && len(pal) == paletteSize {
		app.Palette = pal
	} else if err != nil {
		log.Debug("Ignoring GNOME Terminal palette", zap.Error(err))
	}
	app.BoldIsBright = profile.GetBoolean("bold-is-bright")
	app.AudibleBell = profile.GetBoolean("audible-bell")
	if profile.GetBoolean("cursor-colors-set") {
		bg, berr := theme.ParseColor(profile.GetString("cursor-background-color"))
		fg, ferr := theme.ParseColor(profile.GetString("cursor-foreground-color"))
		if berr == nil && ferr == nil {
			app.Cursor, app.CursorForeground = &bg, &fg
		}
	}
}

func toVTE(c theme.Color) vte.Color { return vte.Color{R: c.R, G: c.G, B: c.B, A: c.A} }

// Apply sets the appearance on a terminal. Theme colors are read from the terminal's style
// context, so this is called again when the theme changes.
func (a *Appearance) Apply(term *vte.Terminal) {
	term.SetFont(a.Font)
	term.SetBoldIsBright(a.BoldIsBright)
	term.SetAudibleBell(a.AudibleBell)
	term.SetScrollbackLines(a.ScrollbackLines)
	term.SetMouseAutohide(true)

	fg, bg := a.Foreground, a.Background
	if a.UseThemeColors {
		fg, bg = themeColors(term.Widget)
	}
	palette := make([]vte.Color, len(a.Palette))
	for i, c := range a.Palette {
		palette[i] = toVTE(c)
	}
	term.SetColors(toVTE(fg), toVTE(bg), palette)

	if a.Cursor != nil {
		cursor, cursorFg := toVTE(*a.Cursor), toVTE(*a.CursorForeground)
		term.SetCursorColors(&cursor, &cursorFg)
	} else {
		term.SetCursorColors(nil, nil)
	}
}

// themeColors returns the text and base colors of the GTK theme, as GNOME Terminal does
// for profiles that use theme colors.
func themeColors(w *gtk.Widget) (theme.Color, theme.Color) {
	fg := theme.Color{R: 0, G: 0, B: 0, A: 1}
	bg := theme.Color{R: 1, G: 1, B: 1, A: 1}
	ctx, err := w.GetStyleContext()
	if err != nil {
		return fg, bg
	}
	// The theme's named colors don't depend on where the widget is, so they're right
	// even for a widget not yet in a window. Its own color is the fallback.
	if c, ok := ctx.LookupColor("theme_text_color"); ok {
		f := c.Floats()
		fg = theme.Color{R: f[0], G: f[1], B: f[2], A: f[3]}
	} else if c := ctx.GetColor(ctx.GetState()); c != nil {
		f := c.Floats()
		fg = theme.Color{R: f[0], G: f[1], B: f[2], A: f[3]}
	}
	if c, ok := ctx.LookupColor("theme_base_color"); ok {
		f := c.Floats()
		bg = theme.Color{R: f[0], G: f[1], B: f[2], A: f[3]}
	}
	return fg, bg
}
