package ui

// useSystemTmux reports whether the tmux in PATH is run rather than the one installed
// with the application.
func (a *App) useSystemTmux() bool {
	if a.state.UseSystemTmux != nil {
		return *a.state.UseSystemTmux
	}
	return a.cfg.Tmux.UseSystemTmux
}

// chosenShell is the default shell chosen in Preferences or the configuration file, or ""
// for none.
func (a *App) chosenShell() string {
	if a.state.DefaultShell != nil {
		return *a.state.DefaultShell
	}
	return a.cfg.Tmux.DefaultShell
}

// fallbackShell is the default shell when none is chosen: PowerShell for the tmux
// installed with the application, which has no shell of the user's to run, and otherwise
// "", leaving tmux's own.
func (a *App) fallbackShell() string {
	if bundledTmux() != "" && !a.useSystemTmux() {
		return powerShell()
	}
	return ""
}

// defaultShell is the shell new local sessions, windows and panes run, or "" for tmux's
// own choice.
func (a *App) defaultShell() string {
	if shell := a.chosenShell(); shell != "" {
		return shell
	}
	return a.fallbackShell()
}

// setUseSystemTmux remembers the choice of tmux. It applies when the application next
// starts: the sessions listed are those of the tmux in use.
func (a *App) setUseSystemTmux(use bool) {
	a.state.UseSystemTmux = &use
	a.saveState()
}

// setDefaultShell remembers the default shell and applies it to sessions, windows and
// panes made from now on.
func (a *App) setDefaultShell(shell string) {
	a.state.DefaultShell = &shell
	a.saveState()
	if local := a.host(LocalHost); local != nil {
		local.client.SetDefaultShell(a.defaultShell())
	}
}
