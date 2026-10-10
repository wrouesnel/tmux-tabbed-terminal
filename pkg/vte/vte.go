// Package vte is the terminal widget panes show tmux clients in, as a gotk3 widget.
//
// On Linux and other Unix systems it binds the parts of the VTE terminal widget
// (vte-2.91, GTK3) this program uses. Child processes are started with os/exec on a pty
// created by VTE, rather than through vte_terminal_spawn_async, so that Go owns the
// process and knows its tty.
//
// VTE doesn't support Windows, where the widget is a placeholder for now and starting a
// process fails with ErrUnsupported.
package vte

// Color is an RGBA color with components from 0 to 1.
type Color struct {
	R, G, B, A float64
}
