package ui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"go.uber.org/zap"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/askpass"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

// prompt is a request from ssh waiting for the user, and where its answer goes.
type prompt struct {
	req    askpass.Request
	answer chan<- askpass.Response
}

// startAskpass starts answering ssh's requests, as for a key's passphrase, with dialogs,
// and sets the environment remote hosts' ssh masters ask through. Without it, or if it
// fails, masters run in BatchMode and need a key without a passphrase or an agent.
func (a *App) startAskpass() {
	if !a.cfg.SSH.AskForPassphrases {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		a.log.Warn("Not asking for ssh passphrases: no path to this program", zap.Error(err))
		return
	}
	a.passphrases = askpass.NewMemory()
	srv, err := askpass.Listen(tmux.DefaultControlDir(), a.askUser)
	if err != nil {
		a.log.Warn("Not asking for ssh passphrases", zap.Error(err))
		return
	}
	a.askpass, a.askpassEnv = srv, srv.Env(exe)
}

// stopAskpass stops answering ssh's requests and forgets the passphrases.
func (a *App) stopAskpass() {
	if a.askpass != nil {
		_ = a.askpass.Close()
		a.passphrases.Forget()
	}
}

// askUser answers a request from ssh: with a passphrase remembered for the key, or else
// with a dialog. It runs on the askpass server's goroutine and waits for the user.
func (a *App) askUser(req askpass.Request) askpass.Response {
	switch {
	case req.Kind == "none":
		// A notice, such as to touch a security key, which ssh takes down when it's
		// done: there's nothing to answer.
		return askpass.Response{OK: true}
	case askpass.IsKeyPassphrase(req):
		if secret, ok := a.passphrases.Recall(req); ok {
			return askpass.Response{OK: true, Secret: secret}
		}
	}
	answer := make(chan askpass.Response, 1)
	glib.IdleAdd(func() { a.queuePrompt(prompt{req: req, answer: answer}) })
	return <-answer
}

// queuePrompt shows a request's dialog, or queues it behind the one showing.
func (a *App) queuePrompt(p prompt) {
	a.prompts = append(a.prompts, p)
	if len(a.prompts) == 1 {
		a.showPrompt()
	}
}

// showPrompt shows the dialog of the first queued request. A passphrase remembered while
// it waited answers it without one.
func (a *App) showPrompt() {
	for len(a.prompts) > 0 {
		p := a.prompts[0]
		if askpass.IsKeyPassphrase(p.req) {
			if secret, ok := a.passphrases.Recall(p.req); ok {
				p.answer <- askpass.Response{OK: true, Secret: secret}
				a.prompts = a.prompts[1:]
				continue
			}
		}
		a.promptDialog(p, func() {
			a.prompts = a.prompts[1:]
			a.showPrompt()
		})
		return
	}
}

// promptDialog asks the user what ssh asks, and calls done once they've answered.
func (a *App) promptDialog(p prompt, done func()) {
	req := p.req
	confirm := req.Kind == "confirm"
	passphrase := askpass.IsKeyPassphrase(req)

	dlg, err := gtk.DialogNew()
	if err != nil {
		p.answer <- askpass.Response{}
		done()
		return
	}
	title := "SSH"
	if passphrase {
		title = "Unlock SSH Key"
	}
	dlg.SetTitle(title)
	if w := a.activeWindow(); w != nil {
		dlg.SetTransientFor(w.window)
		dlg.SetModal(true)
	}
	if confirm {
		_, _ = dlg.AddButton("_No", gtk.RESPONSE_CANCEL)
		_, _ = dlg.AddButton("_Yes", gtk.RESPONSE_ACCEPT)
	} else {
		_, _ = dlg.AddButton("_Cancel", gtk.RESPONSE_CANCEL)
		_, _ = dlg.AddButton("_Unlock", gtk.RESPONSE_ACCEPT)
	}
	dlg.SetDefaultResponse(gtk.RESPONSE_ACCEPT)
	if btn, err := dlg.GetWidgetForResponse(gtk.RESPONSE_ACCEPT); err == nil {
		addClass(btn.ToWidget(), "suggested-action")
	}

	box, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, prefsSpacing)
	message, _ := gtk.LabelNew(strings.TrimSpace(req.Prompt))
	message.SetLineWrap(true)
	message.SetXAlign(0)
	message.SetMaxWidthChars(60) //nolint:mnd // a readable line
	box.PackStart(message, false, false, 0)

	entry, _ := gtk.EntryNew()
	entry.SetVisibility(false)
	entry.SetActivatesDefault(true)
	entry.SetInputPurpose(gtk.INPUT_PURPOSE_PASSWORD)
	if !confirm {
		box.PackStart(entry, false, false, 0)
	}
	remember, _ := gtk.CheckButtonNewWithLabel("Remember for this session")
	remember.SetActive(true)
	remember.SetTooltipText("Keep the passphrase in memory, not on disk, until " +
		filepath.Base(os.Args[0]) + " quits, so other connections with this key don't ask again.")
	if passphrase {
		box.PackStart(remember, false, false, 0)
	}
	content, _ := dlg.GetContentArea()
	content.PackStart(padded(box), true, true, 0)

	answered := false
	dlg.Connect("response", func(_ *gtk.Dialog, response gtk.ResponseType) {
		if answered {
			return
		}
		answered = true
		resp := askpass.Response{OK: response == gtk.RESPONSE_ACCEPT}
		if resp.OK && !confirm {
			resp.Secret, _ = entry.GetText()
			if passphrase {
				if remember.GetActive() {
					a.passphrases.Remember(req, resp.Secret)
				} else {
					a.passphrases.Given(req)
				}
			}
		}
		p.answer <- resp
		dlg.Destroy()
		done()
	})
	dlg.ShowAll()
	dlg.Present()
	if !confirm {
		entry.GrabFocus()
	}
}
