package ui

// #include <stdlib.h>
import "C"

import (
	"os"
	"path/filepath"
	"unsafe"
)

// pangoBackendEnv chooses how Pango draws text on Windows: with Windows' renderer, or with
// fontconfig and FreeType, as on GNOME.
const pangoBackendEnv = "PANGOCAIRO_BACKEND"

// bundledFontName is the font installed with the application for terminals, the size
// GNOME Terminal shows it on Ubuntu.
const bundledFontName = "Ubuntu Sans Mono 13"

// setUpFonts makes GTK draw text with fontconfig, as on GNOME, if the application was
// installed with its fonts and fontconfig's configuration for them. Windows' renderer
// draws terminal text thin, and over Remote Desktop, which turns ClearType off, without
// smoothing. PANGOCAIRO_BACKEND=win32 keeps Windows' renderer. It must be called before
// GTK draws any text.
func setUpFonts() {
	if _, set := os.LookupEnv(pangoBackendEnv); set || !hasBundledFonts() {
		return
	}
	// Pango reads it with the C runtime's getenv, which doesn't see what os.Setenv sets:
	// _putenv_s sets it for both.
	name, value := C.CString(pangoBackendEnv), C.CString("fc")
	defer C.free(unsafe.Pointer(name))
	defer C.free(unsafe.Pointer(value))
	C._putenv_s(name, value)
}

// hasBundledFonts reports whether fontconfig's configuration was installed with the
// application, in etc\fonts beside its bin folder, where fontconfig looks for it.
func hasBundledFonts() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(filepath.Dir(filepath.Dir(exe)), "etc", "fonts", "fonts.conf"))
	return err == nil
}

// bundledFont returns the font installed with the application, if GTK draws with
// fontconfig, which finds it, or "".
func bundledFont() string {
	if backend := os.Getenv(pangoBackendEnv); (backend == "fc" || backend == "fontconfig") && hasBundledFonts() {
		return bundledFontName
	}
	return ""
}
