package ui

import (
	"context"
	"strings"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/gtk"
	"go.uber.org/zap"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/tmux"
)

// maxClipboardBytes is the largest tmux buffer copied to the desktop clipboard. Bigger
// ones, as from copying a whole history, would be slow to fetch from a remote host.
const maxClipboardBytes = 8 << 20

// syncClipboard puts a host's newest tmux paste buffer on the desktop clipboard when it's
// newer than the last one seen, so text copied in tmux, as with copy mode, can be pasted
// outside it. The first snapshot of a host only notes its newest buffer, so starting
// doesn't replace the clipboard with an old copy.
func (a *App) syncClipboard(h *Host, newest *tmux.Buffer) {
	if !h.buffersKnown {
		h.buffersKnown, h.lastBuffer = true, newest
		return
	}
	if newest == nil || !newest.NewerThan(h.lastBuffer) {
		// None, the same one, or an older one left after the newest was deleted.
		if newest != nil {
			h.lastBuffer = newest
		}
		return
	}
	h.lastBuffer = newest
	if !a.cfg.Clipboard.FromTmux {
		return
	}
	if newest.Size > maxClipboardBytes {
		a.log.Info("Not copying a large tmux buffer to the clipboard",
			zap.String("host", h.Name), zap.String("buffer", newest.Name), zap.Int("bytes", newest.Size))
		return
	}

	name := newest.Name
	runAsync(a, func(ctx context.Context) (string, error) {
		return h.client.ShowBuffer(ctx, name)
	}, func(text string, err error) {
		if err != nil {
			// Most likely the buffer was deleted since the snapshot.
			a.log.Warn("Could not read tmux buffer",
				zap.String("host", h.Name), zap.String("buffer", name), zap.Error(err))
			return
		}
		clip, err := gtk.ClipboardGet(gdk.SELECTION_CLIPBOARD)
		if err != nil {
			a.log.Warn("Could not get the clipboard", zap.Error(err))
			return
		}
		// The clipboard holds text: a buffer of something else is shown as best it can.
		clip.SetText(strings.ToValidUTF8(text, "�"))
		a.log.Debug("Copied tmux buffer to the clipboard",
			zap.String("host", h.Name), zap.String("buffer", name), zap.Int("bytes", len(text)))
	})
}
