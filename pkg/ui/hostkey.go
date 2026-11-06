package ui

import (
	"context"
	"fmt"

	"github.com/gotk3/gotk3/gtk"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

// hostKeyStatus is how a host whose key ssh refused is shown in its session list, or ""
// if ssh didn't refuse it.
func hostKeyStatus(err error) string {
	p, ok := tmux.FindHostKeyProblem(err)
	switch {
	case !ok:
		return ""
	case p.Changed:
		return "host key changed"
	}
	return "unknown host key"
}

// offerHostKeyFix asks, if err is ssh refusing h's key, whether to trust the key h sends
// now, and if the user does, trusts it and calls retry. It reports whether err was such
// a refusal. A refusal on the way to h's origin is offered for the origin's own host.
func (a *App) offerHostKeyFix(w *Window, h *Host, err error, retry func()) bool {
	p, ok := tmux.FindHostKeyProblem(err)
	if !ok {
		return false
	}
	if origin := a.host(h.Config.Via); origin != nil && origin.err != nil {
		return true
	}
	ssh := h.client.SSH
	ask := func(keyType, fingerprint string) {
		if !w.askTrustHostKey(h.Name, originPhrase(a.originOf(h)), p, keyType, fingerprint) {
			return
		}
		w.setStatus("Trusting the key of " + h.Name + "…")
		runAsync(a, func(ctx context.Context) (struct{}, error) {
			return struct{}{}, ssh.TrustHostKey(ctx, p, fingerprint)
		}, func(_ struct{}, err error) {
			w.setStatus("")
			if err != nil {
				w.showError("Could not trust the key of "+h.Name, err.Error())
				return
			}
			retry()
		})
	}
	if p.Fingerprint != "" {
		ask(p.KeyType, p.Fingerprint)
		return true
	}
	// ssh doesn't say which key it refused when no key is known: ask the host.
	w.setStatus("Checking the key of " + h.Name + "…")
	runAsync(a, func(ctx context.Context) ([2]string, error) {
		keyType, fingerprint, err := ssh.ProbeHostKey(ctx)
		return [2]string{keyType, fingerprint}, err
	}, func(key [2]string, err error) {
		w.setStatus("")
		if err != nil {
			w.showError("Could not check the key of "+h.Name, err.Error())
			return
		}
		ask(key[0], key[1])
	})
	return true
}

// originOf is the name of the host h's ssh runs on.
func (a *App) originOf(h *Host) string {
	if h.Config.Via == "" {
		return LocalHost
	}
	return h.Config.Via
}

// askTrustHostKey asks whether to trust the key a host sends. A changed key is a warning:
// it's what an attacker in the middle would send.
func (w *Window) askTrustHostKey(name, origin string, p *tmux.HostKeyProblem, keyType, fingerprint string) bool {
	msgType, primary, button := gtk.MESSAGE_QUESTION, name+" isn't a known host", "_Trust Key"
	explain := "ssh on " + origin + " has no key recorded for the host, so it can't tell " +
		"that it's the host you mean. If you can, check the key's fingerprint with whoever runs the host."
	if p.Changed {
		msgType, primary, button = gtk.MESSAGE_WARNING, "The host key of "+name+" has changed", "_Trust New Key"
		explain = "The host sent a different key from the one ssh on " + origin + " has recorded " +
			"for it. That happens when a host is reinstalled or its keys are replaced, but " +
			"it's also what someone intercepting the connection would send. Check the new key's " +
			"fingerprint with whoever runs the host before trusting it."
		if p.KnownHosts != "" {
			explain += fmt.Sprintf("\n\nThe old key is in %s, line %d. Trusting the new key removes it.", p.KnownHosts, p.Line)
		}
	}
	if keyType == "" {
		keyType = "host"
	}
	explain += fmt.Sprintf("\n\nThe %s key's fingerprint:\n%s", keyType, fingerprint)

	dlg := gtk.MessageDialogNew(w.window, gtk.DIALOG_MODAL|gtk.DIALOG_DESTROY_WITH_PARENT, msgType,
		gtk.BUTTONS_NONE, "%s", primary)
	defer dlg.Destroy()
	dlg.FormatSecondaryText("%s", explain)
	_, _ = dlg.AddButton("_Cancel", gtk.RESPONSE_CANCEL)
	if btn, err := dlg.AddButton(button, gtk.RESPONSE_ACCEPT); err == nil && p.Changed {
		addClass(btn, "destructive-action")
	}
	// Trusting a key should be a decision, not Enter pressed in passing.
	dlg.SetDefaultResponse(gtk.RESPONSE_CANCEL)
	return dlg.Run() == gtk.RESPONSE_ACCEPT
}

// checkHostKey offers to trust a listed host's key, if ssh refused it.
func (w *Window) checkHostKey(name string) {
	h := w.app.host(name)
	if h == nil || h.Local() {
		return
	}
	if w.app.offerHostKeyFix(w, h, h.err, h.requestPoll) {
		return
	}
	dlg := gtk.MessageDialogNew(w.window, gtk.DIALOG_MODAL|gtk.DIALOG_DESTROY_WITH_PARENT, gtk.MESSAGE_INFO,
		gtk.BUTTONS_CLOSE, "%s", "ssh accepts the key of "+name)
	defer dlg.Destroy()
	dlg.FormatSecondaryText("%s", "Its last connection didn't fail over the host's key.")
	dlg.Run()
}

// originPhrase is how an origin is named in a sentence.
func originPhrase(origin string) string {
	if origin == LocalHost {
		return "this computer"
	}
	return origin
}

// addHostDraft is what Add Host was given.
type addHostDraft struct {
	destination, origin string
	forwardAgent        bool
}

// offerHostKeyFixOnce offers to trust a listed host's key when its polls start failing
// because ssh refuses it, once for each way they fail.
func (a *App) offerHostKeyFixOnce(h *Host) {
	if h.err == nil {
		h.hostKeyOffered = ""
		return
	}
	w := a.activeWindow()
	if w == nil || h.Local() || h.hostKeyOffered == h.err.Error() {
		return
	}
	if _, ok := tmux.FindHostKeyProblem(h.err); ok {
		h.hostKeyOffered = h.err.Error()
		a.offerHostKeyFix(w, h, h.err, h.requestPoll)
	}
}
