package ui

import (
	"fmt"
	"strings"

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

// GnomeProfile is one GNOME Terminal profile.
type GnomeProfile struct {
	UUID string
	Name string
}

// gnomeTerminalProfiles lists GNOME Terminal's profiles and returns the default one's
// UUID. It returns nothing if GNOME Terminal isn't installed.
func gnomeTerminalProfiles() ([]GnomeProfile, string) {
	if !hasSchema(schemaProfilesList) || !hasSchema(schemaProfile) {
		return nil, ""
	}
	list := glib.SettingsNew(schemaProfilesList)
	profiles := []GnomeProfile{}
	for _, uuid := range list.GetStrv("list") {
		name := gnomeTerminalProfile(uuid).GetString("visible-name")
		if name == "" {
			name = uuid
		}
		profiles = append(profiles, GnomeProfile{UUID: uuid, Name: name})
	}
	return profiles, list.GetString("default")
}

// gnomeTerminalProfile returns the settings of a GNOME Terminal profile. The schemas must
// be installed.
func gnomeTerminalProfile(uuid string) *glib.Settings {
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

// Color scheme sources chosen in Preferences. A scheme setting is one of these, a
// built-in scheme's ID, or schemeProfilePrefix and a GNOME Terminal profile UUID.
const (
	// schemeConfigured follows the configuration file, and GNOME Terminal's default
	// profile if use-gnome-terminal-profile is set.
	schemeConfigured    = ""
	schemeTheme         = "theme"
	schemeCustom        = "custom"
	schemeProfilePrefix = "profile:"
)

// AppearancePrefs are the appearance choices made in Preferences. They override the
// configuration file. Empty fields leave it alone.
type AppearancePrefs struct {
	// Scheme picks where colors come from: see the scheme constants.
	Scheme string `yaml:"scheme,omitempty"`
	// Foreground, Background and Palette are the custom scheme's colors and palette name.
	Foreground string `yaml:"foreground,omitempty"`
	Background string `yaml:"background,omitempty"`
	Palette    string `yaml:"palette,omitempty"`
	// Font is a Pango font description. UseSystemFont, if set, says whether to use the
	// desktop's monospace font instead.
	Font          string `yaml:"font,omitempty"`
	UseSystemFont *bool  `yaml:"use-system-font,omitempty"`
	BoldIsBright  *bool  `yaml:"bold-is-bright,omitempty"`
}

// ResolveAppearance works out the terminal look: Preferences first, then the
// configuration file, then GNOME Terminal's default profile and the desktop settings. It
// also returns the GNOME Terminal profile it read, if any, so changes to it can be
// followed.
func ResolveAppearance(cfg AppearanceConfig, prefs AppearancePrefs, log *zap.Logger) (*Appearance, *glib.Settings) {
	app := &Appearance{
		Font:            systemMonospaceFont(),
		UseThemeColors:  true,
		ScrollbackLines: cfg.ScrollbackLines,
	}
	app.Palette, _ = theme.ParsePalette(theme.Palettes[theme.DefaultPalette])

	var profile *glib.Settings
	profiles, defaultUUID := gnomeTerminalProfiles()
	usesProfile := func(uuid string) {
		for _, p := range profiles {
			if p.UUID == uuid {
				profile = gnomeTerminalProfile(uuid)
				applyProfile(app, profile, log)
				return
			}
		}
		log.Warn("No such GNOME Terminal profile", zap.String("uuid", uuid))
	}

	switch scheme := prefs.Scheme; {
	case scheme == schemeConfigured:
		if cfg.UseGnomeTerminalProfile && defaultUUID != "" {
			usesProfile(defaultUUID)
		}
		applyConfig(app, cfg, log)
	case strings.HasPrefix(scheme, schemeProfilePrefix):
		usesProfile(strings.TrimPrefix(scheme, schemeProfilePrefix))
	case scheme == schemeTheme:
		app.UseThemeColors = true
	case scheme == schemeCustom:
		fg, ferr := theme.ParseColor(prefs.Foreground)
		bg, berr := theme.ParseColor(prefs.Background)
		if ferr == nil && berr == nil {
			app.UseThemeColors, app.Foreground, app.Background = false, fg, bg
		}
		if pal, err := theme.ParsePalette(theme.Palettes[prefs.Palette]); err == nil && len(pal) == paletteSize {
			app.Palette = pal
		}
	default:
		if s := theme.SchemeByID(scheme); s != nil {
			app.UseThemeColors = false
			app.Foreground = theme.MustParseColor(s.Foreground)
			app.Background = theme.MustParseColor(s.Background)
			app.Palette, _ = theme.ParsePalette(theme.Palettes[s.Palette])
		} else {
			log.Warn("Unknown color scheme", zap.String("scheme", scheme))
		}
	}

	// Fonts and bold apply whatever the colors are.
	if prefs.Scheme != schemeConfigured && cfg.Font != "" {
		app.Font = cfg.Font
	}
	if prefs.UseSystemFont != nil {
		if *prefs.UseSystemFont {
			app.Font = systemMonospaceFont()
		} else if prefs.Font != "" {
			app.Font = prefs.Font
		}
	}
	if prefs.BoldIsBright != nil {
		app.BoldIsBright = *prefs.BoldIsBright
	}
	return app, profile
}

// applyConfig applies the configuration file's appearance settings.
func applyConfig(app *Appearance, cfg AppearanceConfig, log *zap.Logger) {
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
