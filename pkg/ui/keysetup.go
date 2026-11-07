package ui

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gotk3/gotk3/gtk"
	"github.com/pkg/errors"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

// defaultKeyFiles are the private keys ssh tries when none is configured, in ~/.ssh.
var defaultKeyFiles = []string{ //nolint:gochecknoglobals // ssh's list
	"id_rsa", "id_ecdsa", "id_ecdsa_sk", "id_ed25519", "id_ed25519_sk", "id_dsa",
}

// keyCheckTimeout is how long asking the ssh agent for its keys may take.
const keyCheckTimeout = 5 * time.Second

// keyPlan is how a host is to log in with a key: the key, made if it doesn't exist, and,
// for a key of the host's own, the ssh configuration which points ssh at it.
type keyPlan struct {
	// key is the private key's path.
	key string
	// configFile and configHost, if set, are the ssh configuration file to add a Host
	// block to, and the host it's for.
	configFile, configHost string
}

// planKey decides which key a host is to log in with: ssh's default key, if this
// computer has no key ssh would try, or else a key for the host alone.
func (a *App) planKey(ctx context.Context, h *Host) (keyPlan, error) {
	dir, err := sshDir()
	if err != nil {
		return keyPlan{}, err
	}
	if !hasLocalKey(ctx, dir) {
		return keyPlan{key: filepath.Join(dir, "id_ed25519")}, nil
	}
	host := destinationHost(h.Config.Destination)
	return keyPlan{
		key:        filepath.Join(dir, "id_ed25519_"+keySlug(host)),
		configFile: a.sshHostsFile(),
		configHost: host,
	}, nil
}

// sshDir is ~/.ssh, where ssh looks for keys.
func sshDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "finding the home directory")
	}
	return filepath.Join(home, ".ssh"), nil
}

// hasLocalKey reports whether this computer has a key ssh would try: one of its default
// keys in dir, or a key in the ssh agent.
func hasLocalKey(ctx context.Context, dir string) bool {
	for _, name := range defaultKeyFiles {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	ctx, cancel := context.WithTimeout(ctx, keyCheckTimeout)
	defer cancel()
	out, err := tmux.Command(ctx, []string{sshTool("ssh-add"), "-L"}).Output()
	return err == nil && strings.Contains(string(out), "ssh-")
}

// sshTool is one of ssh's programs, such as ssh-keygen: beside the ssh hosts are
// reached with, where that's a path, as Windows' own.
func sshTool(name string) string {
	if ssh := systemSSH(); ssh != "" {
		return filepath.Join(filepath.Dir(ssh), name+filepath.Ext(ssh))
	}
	return name
}

// destinationHost is the host of an ssh destination, as ssh config's Host matches it:
// user@host gives host, and ssh://user@host:port too.
func destinationHost(dest string) string {
	uri := strings.HasPrefix(dest, "ssh://")
	dest = strings.TrimPrefix(dest, "ssh://")
	if i := strings.LastIndex(dest, "@"); i >= 0 {
		dest = dest[i+1:]
	}
	if uri {
		if i := strings.LastIndex(dest, ":"); i >= 0 && !strings.HasSuffix(dest, "]") {
			dest = dest[:i]
		}
		dest = strings.Trim(dest, "[]")
	}
	return dest
}

// keySlug makes a host into part of a file name.
func keySlug(host string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' {
			return r
		}
		return '_'
	}, host)
}

// withHostIdentity returns an ssh configuration with a Host block added at the end,
// which has ssh try identity for host. IdentityFile adds to the keys tried, so other
// settings for the host still apply.
func withHostIdentity(config, host, identity string) string {
	if config != "" && !strings.HasSuffix(config, "\n") {
		config += "\n"
	}
	if config != "" {
		config += "\n"
	}
	return config + "# Added by tmux-tabbed-terminal: the key " + filepath.Base(identity) +
		" logs in to " + host + ".\nHost " + host + "\n    IdentityFile " + identity + "\n"
}

// tildeSSHPath writes a path in ~/.ssh as ssh config does, with ~, which works on
// Windows too.
func tildeSSHPath(path string) string {
	return "~/.ssh/" + filepath.Base(path)
}

// setUpKey makes a host log in with a key: it makes the key, if it doesn't exist, adds
// it to the host's authorized keys over the host's connection, points the ssh
// configuration at it if it's the host's own, and checks ssh logs in with it.
func (a *App) setUpKey(ctx context.Context, h *Host, plan keyPlan) error {
	if _, err := os.Stat(plan.key); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(plan.key), 0o700); err != nil { //nolint:mnd // owner only
			return errors.Wrap(err, "making ~/.ssh")
		}
		argv := []string{sshTool("ssh-keygen"), "-q", "-t", "ed25519", "-N", "", "-C", keyComment(), "-f", plan.key}
		if out, err := tmux.Command(ctx, argv).CombinedOutput(); err != nil {
			return errors.Wrapf(err, "making the key: %s", strings.TrimSpace(string(out)))
		}
	}
	public, err := os.ReadFile(plan.key + ".pub")
	if err != nil {
		return errors.Wrap(err, "reading the public key")
	}
	if err := h.client.SSH.AuthorizeKey(ctx, string(public)); err != nil {
		return err
	}
	if plan.configHost != "" {
		if err := addHostIdentity(plan.configFile, plan.configHost, tildeSSHPath(plan.key)); err != nil {
			return err
		}
	}
	return h.client.SSH.CheckKeyLogin(ctx)
}

// addHostIdentity adds a Host block for host's key to an ssh configuration file, unless
// the file already has it.
func addHostIdentity(file, host, identity string) error {
	data, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.Wrap(err, "reading the ssh configuration")
	}
	if strings.Contains(string(data), "IdentityFile "+identity+"\n") {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil { //nolint:mnd // owner only
		return errors.Wrap(err, "making the ssh configuration's directory")
	}
	mode := os.FileMode(0o600) //nolint:mnd // owner only, as ssh wants
	if info, err := os.Stat(file); err == nil {
		mode = info.Mode().Perm()
	}
	return errors.Wrap(os.WriteFile(file, []byte(withHostIdentity(string(data), host, identity)), mode),
		"writing the ssh configuration")
}

// keyComment names a new key's owner, as ssh-keygen does, and what made it.
func keyComment() string {
	name := "user"
	if u, err := user.Current(); err == nil {
		name = u.Username
		if i := strings.LastIndexAny(name, `\`); i >= 0 {
			name = name[i+1:]
		}
	}
	host, _ := os.Hostname()
	return name + "@" + host + " (tmux-tabbed-terminal)"
}

// offerKeySetupOnce offers a key to a host ssh just logged in to with a password, unless
// the user said they'd rather keep the password.
func (a *App) offerKeySetupOnce(h *Host) {
	if !a.passwordUsed[h.Name] || slices.Contains(a.state.KeySetupDeclined, h.Name) {
		return
	}
	w := a.activeWindow()
	if w == nil {
		return
	}
	delete(a.passwordUsed, h.Name)
	a.offerKeySetup(w, h, true)
}

// offerKeySetup asks whether to set up a key for a host reached from this computer, and
// does it if the user agrees. afterPassword says ssh just asked for the host's password.
func (a *App) offerKeySetup(w *Window, h *Host, afterPassword bool) {
	runAsync(a, func(ctx context.Context) (keyPlan, error) {
		return a.planKey(ctx, h)
	}, func(plan keyPlan, err error) {
		if err != nil {
			w.showError("Could not set up a key for "+h.Name, err.Error())
			return
		}
		switch w.askKeySetup(h.Name, plan, afterPassword) {
		case gtk.RESPONSE_ACCEPT:
		case gtk.RESPONSE_REJECT:
			if !slices.Contains(a.state.KeySetupDeclined, h.Name) {
				a.state.KeySetupDeclined = append(a.state.KeySetupDeclined, h.Name)
				a.saveState()
			}
			return
		default:
			return
		}
		w.setStatus("Setting up a key for " + h.Name + "…")
		runAsync(a, func(ctx context.Context) (struct{}, error) {
			return struct{}{}, a.setUpKey(ctx, h, plan)
		}, func(_ struct{}, err error) {
			w.setStatus("")
			if err != nil {
				w.showError("Could not set up a key for "+h.Name, err.Error())
				return
			}
			a.state.KeySetupDeclined = slices.DeleteFunc(a.state.KeySetupDeclined, func(n string) bool { return n == h.Name })
			a.saveState()
			w.showInfo(h.Name+" now logs you in with a key", keyDoneText(plan))
		})
	})
}

// askKeySetup asks whether to set up a key, returning ACCEPT to set it up, REJECT to
// keep using a password and not ask again, or anything else for not now.
func (w *Window) askKeySetup(name string, plan keyPlan, afterPassword bool) gtk.ResponseType {
	primary := "Log in to " + name + " with a key?"
	text := ""
	if afterPassword {
		text = "ssh asked for your password to log in to " + name + ". "
	}
	if plan.configHost == "" {
		text += "An ssh key lets this computer log in by itself, without a password. This " +
			"computer has none yet: one can be made for you, in " + tildePath(plan.key) + ", and added " +
			"to the keys " + name + " accepts."
	} else {
		text += "An ssh key lets this computer log in by itself, without a password. " + name +
			" doesn't accept your keys yet: a key just for it can be made, in " + tildePath(plan.key) +
			", added to the keys it accepts, and set for " + plan.configHost + " in " +
			tildePath(plan.configFile) + "."
	}
	text += "\n\nAnyone who can read the key's file can log in as you, as with a saved password."

	dlg := gtk.MessageDialogNew(w.window, gtk.DIALOG_MODAL|gtk.DIALOG_DESTROY_WITH_PARENT,
		gtk.MESSAGE_QUESTION, gtk.BUTTONS_NONE, "%s", primary)
	defer dlg.Destroy()
	dlg.FormatSecondaryText("%s", text)
	_, _ = dlg.AddButton("_Keep Using a Password", gtk.RESPONSE_REJECT)
	if btn, err := dlg.AddButton("_Set Up Key", gtk.RESPONSE_ACCEPT); err == nil {
		addClass(btn, "suggested-action")
	}
	dlg.SetDefaultResponse(gtk.RESPONSE_ACCEPT)
	return dlg.Run()
}

// keyDoneText says what setting up a key did.
func keyDoneText(plan keyPlan) string {
	text := "The key is " + tildePath(plan.key) + ", and its public half is in ~/.ssh/authorized_keys on the host."
	if plan.configHost != "" {
		text += " " + tildePath(plan.configFile) + " has ssh use it for " + plan.configHost + "."
	}
	return text
}

// setUpHostKey offers to set up a key for a listed host, from its menu.
func (w *Window) setUpHostKey(name string) {
	h := w.app.host(name)
	if h == nil || h.Local() {
		return
	}
	if h.Config.Via != "" {
		w.showInfo("Keys are set up from this computer",
			name+" is reached through "+h.Config.Via+", whose ssh logs in with its own keys, or yours "+
				"forwarded. Set up keys there.")
		return
	}
	if h.err != nil {
		w.showError("Could not set up a key for "+name, "It can't be reached: "+h.err.Error())
		return
	}
	w.app.offerKeySetup(w, h, false)
}

// showInfo shows a message.
func (w *Window) showInfo(primary, secondary string) {
	dlg := gtk.MessageDialogNew(w.window, gtk.DIALOG_MODAL|gtk.DIALOG_DESTROY_WITH_PARENT,
		gtk.MESSAGE_INFO, gtk.BUTTONS_CLOSE, "%s", primary)
	defer dlg.Destroy()
	dlg.FormatSecondaryText("%s", secondary)
	dlg.Run()
}
