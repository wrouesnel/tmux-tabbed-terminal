package ui

import (
	"fmt"
	"math"
	"sort"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/gtk"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/theme"
)

// schemeConfiguredID stands for schemeConfigured in the scheme menu, which can't have an
// empty ID.
const schemeConfiguredID = "configured"

const (
	prefsSpacing = 12
	prefsWidth   = 460
)

// preferences is the Preferences dialog. Every change applies straight away.
type preferences struct {
	app        *App
	dlg        *gtk.Dialog
	scheme     *gtk.ComboBoxText
	foreground *gtk.ColorButton
	background *gtk.ColorButton
	palette    *gtk.ComboBoxText
	systemFont *gtk.CheckButton
	font       *gtk.FontButton
	bold       *gtk.CheckButton
	// loading is set while the widgets are filled in, so their signals don't apply.
	loading bool
}

// showPreferences opens the Preferences dialog, or raises it if it's open.
func (a *App) showPreferences() {
	if a.prefs != nil {
		a.prefs.dlg.Present()
		return
	}
	p := newPreferences(a)
	a.prefs = p
	p.dlg.Connect("destroy", func() { a.prefs = nil })
	p.dlg.ShowAll()
	p.syncSensitivity()
}

func colorToRGBA(c theme.Color) *gdk.RGBA { return gdk.NewRGBA(c.R, c.G, c.B, 1) }

// rgbaToHex formats a color button's color for the preferences file.
func rgbaToHex(c *gdk.RGBA) string {
	f := c.Floats()
	b := func(v float64) int { return int(math.Round(v * 255)) } //nolint:mnd // 8-bit channel
	return fmt.Sprintf("#%02x%02x%02x", b(f[0]), b(f[1]), b(f[2]))
}

// prefsLabel is a row label of the dialog.
func prefsLabel(text string) *gtk.Label {
	l, _ := gtk.LabelNew(text)
	l.SetXAlign(1)
	addClass(l, "dim-label")
	return l
}

func newPreferences(a *App) *preferences {
	p := &preferences{app: a, loading: true}
	defer func() { p.loading = false }()
	prefs := a.state.Appearance

	p.dlg, _ = gtk.DialogNew()
	p.dlg.SetTitle("Preferences")
	p.dlg.SetDefaultSize(prefsWidth, -1)
	if w := a.activeWindow(); w != nil {
		p.dlg.SetTransientFor(w.window)
	}
	_, _ = p.dlg.AddButton("_Close", gtk.RESPONSE_CLOSE)
	p.dlg.Connect("response", func() { p.dlg.Destroy() })

	grid, _ := gtk.GridNew()
	grid.SetRowSpacing(prefsSpacing / 2) //nolint:mnd // half spacing between rows
	grid.SetColumnSpacing(prefsSpacing)
	grid.SetMarginStart(prefsSpacing * 2) //nolint:mnd // dialog margins
	grid.SetMarginEnd(prefsSpacing * 2)   //nolint:mnd
	grid.SetMarginTop(prefsSpacing * 2)   //nolint:mnd
	grid.SetMarginBottom(prefsSpacing)
	row := 0
	add := func(label string, widget gtk.IWidget) {
		grid.Attach(prefsLabel(label), 0, row, 1, 1)
		grid.Attach(widget, 1, row, 1, 1)
		row++
	}

	// Colors.
	p.scheme, _ = gtk.ComboBoxTextNew()
	p.scheme.SetHExpand(true)
	defaultLabel := "Default (configuration file)"
	if a.cfg.Appearance.UseGnomeTerminalProfile {
		defaultLabel = "Default (GNOME Terminal's default profile)"
	}
	p.scheme.Append(schemeConfiguredID, defaultLabel)
	profiles, _ := gnomeTerminalProfiles()
	for _, prof := range profiles {
		p.scheme.Append(schemeProfilePrefix+prof.UUID, "GNOME Terminal: "+prof.Name)
	}
	p.scheme.Append(schemeTheme, "GTK theme")
	for _, s := range theme.Schemes {
		p.scheme.Append(s.ID, s.Name)
	}
	p.scheme.Append(schemeCustom, "Custom")
	active := prefs.Scheme
	if active == schemeConfigured {
		active = schemeConfiguredID
	}
	if !p.scheme.SetActiveID(active) {
		p.scheme.SetActiveID(schemeConfiguredID)
	}
	p.scheme.Connect("changed", func() {
		if p.scheme.GetActiveID() == schemeCustom && !p.loading {
			// Start a custom scheme from the colors in use.
			p.loadCurrentColors()
		}
		p.apply()
	})
	add("Colors", p.scheme)

	fg, bg := a.appearance.Foreground, a.appearance.Background
	if a.appearance.UseThemeColors {
		if probe, err := gtk.LabelNew(""); err == nil {
			fg, bg = themeColors(&probe.Widget)
		}
	}
	p.foreground, _ = gtk.ColorButtonNewWithRGBA(colorToRGBA(fg))
	p.background, _ = gtk.ColorButtonNewWithRGBA(colorToRGBA(bg))
	if c, err := theme.ParseColor(prefs.Foreground); err == nil {
		p.foreground.SetRGBA(colorToRGBA(c))
	}
	if c, err := theme.ParseColor(prefs.Background); err == nil {
		p.background.SetRGBA(colorToRGBA(c))
	}
	p.foreground.Connect("color-set", p.apply)
	p.background.Connect("color-set", p.apply)
	colors, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, prefsSpacing/2) //nolint:mnd // half spacing
	fgLabel, _ := gtk.LabelNew("Text")
	bgLabel, _ := gtk.LabelNew("Background")
	colors.PackStart(fgLabel, false, false, 0)
	colors.PackStart(p.foreground, false, false, 0)
	colors.PackStart(bgLabel, false, false, prefsSpacing/2) //nolint:mnd
	colors.PackStart(p.background, false, false, 0)
	add("Custom colors", colors)

	p.palette, _ = gtk.ComboBoxTextNew()
	names := make([]string, 0, len(theme.Palettes))
	for name := range theme.Palettes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p.palette.Append(name, name)
	}
	if !p.palette.SetActiveID(prefs.Palette) {
		p.palette.SetActiveID(theme.DefaultPalette)
	}
	p.palette.Connect("changed", p.apply)
	add("Custom palette", p.palette)

	// Font.
	p.systemFont, _ = gtk.CheckButtonNewWithLabel("Use the system monospace font")
	p.systemFont.SetActive(prefs.UseSystemFont == nil || *prefs.UseSystemFont)
	p.systemFont.Connect("toggled", func() {
		p.syncSensitivity()
		p.apply()
	})
	add("Font", p.systemFont)
	p.font, _ = gtk.FontButtonNewWithFont(a.appearance.Font)
	p.font.Connect("font-set", p.apply)
	add("", p.font)

	p.bold, _ = gtk.CheckButtonNewWithLabel("Show bold text in bright colors")
	p.bold.SetActive(a.appearance.BoldIsBright)
	p.bold.Connect("toggled", p.apply)
	add("Text", p.bold)

	hint, _ := gtk.LabelNew("Changes apply to every terminal straight away. A GNOME Terminal profile " +
		"is followed as it's edited in GNOME Terminal.")
	hint.SetLineWrap(true)
	hint.SetXAlign(0)
	hint.SetMarginTop(prefsSpacing)
	addClass(hint, "dim-label")
	grid.Attach(hint, 0, row, 2, 1) //nolint:mnd // both columns

	content, _ := p.dlg.GetContentArea()
	content.PackStart(grid, true, true, 0)
	return p
}

// loadCurrentColors sets the custom color buttons to the colors in use.
func (p *preferences) loadCurrentColors() {
	app := p.app.appearance
	fg, bg := app.Foreground, app.Background
	if app.UseThemeColors {
		if probe, err := gtk.LabelNew(""); err == nil {
			fg, bg = themeColors(&probe.Widget)
		}
	}
	p.foreground.SetRGBA(colorToRGBA(fg))
	p.background.SetRGBA(colorToRGBA(bg))
}

// syncSensitivity enables the controls which apply to the current choices.
func (p *preferences) syncSensitivity() {
	custom := p.scheme.GetActiveID() == schemeCustom
	p.foreground.SetSensitive(custom)
	p.background.SetSensitive(custom)
	p.palette.SetSensitive(custom)
	p.font.SetSensitive(!p.systemFont.GetActive())
}

// apply turns the dialog's state into appearance preferences and applies them.
func (p *preferences) apply() {
	if p.loading {
		return
	}
	p.syncSensitivity()
	scheme := p.scheme.GetActiveID()
	if scheme == schemeConfiguredID {
		scheme = schemeConfigured
	}
	systemFont := p.systemFont.GetActive()
	bold := p.bold.GetActive()
	prefs := AppearancePrefs{
		Scheme:        scheme,
		Font:          p.font.GetFont(),
		UseSystemFont: &systemFont,
		BoldIsBright:  &bold,
	}
	if scheme == schemeCustom {
		prefs.Foreground = rgbaToHex(p.foreground.GetRGBA())
		prefs.Background = rgbaToHex(p.background.GetRGBA())
		prefs.Palette = p.palette.GetActiveID()
	}
	p.app.setAppearancePrefs(prefs)
}
