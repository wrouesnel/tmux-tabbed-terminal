package ui

import (
	"embed"
	"os"
	"path/filepath"

	"github.com/gotk3/gotk3/gtk"
	"go.uber.org/zap"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

// Icons this application draws itself, where the icon theme has nothing suitable.
const iconFilter = "ttt-filter-symbolic"

//go:embed icons/*.svg
var iconFiles embed.FS

// installIcons makes the embedded icons available to the icon theme. GTK finds loose
// icon files in its search path, and recolors those named -symbolic like the theme's own.
func installIcons(log *zap.Logger) {
	dir := filepath.Join(tmux.DefaultControlDir(), "icons")
	if err := os.MkdirAll(dir, 0o700); err != nil { //nolint:mnd // owner only
		log.Warn("Could not install icons", zap.Error(err))
		return
	}
	entries, _ := iconFiles.ReadDir("icons")
	for _, e := range entries {
		data, err := iconFiles.ReadFile("icons/" + e.Name())
		if err != nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), data, 0o600); err != nil { //nolint:mnd // owner only
			log.Warn("Could not install icon", zap.String("icon", e.Name()), zap.Error(err))
		}
	}
	if theme, err := gtk.IconThemeGetDefault(); err == nil {
		theme.AppendSearchPath(dir)
	}
}
