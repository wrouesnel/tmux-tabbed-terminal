# termwidget

A terminal widget for GTK3 that runs where VTE doesn't, which is Windows. It is a
prototype. It lives in this repository for now, but it's built to be split out into its own
module: nothing in it imports the rest of the application.

## Structure

| Package | What it is | Depends on |
| --- | --- | --- |
| `emulator` | The terminal emulator, without a display. It parses a program's output into a screen of cells and encodes keys, mouse, focus and paste for the program. | [go-vte](https://github.com/danielgatis/go-vte) (an Alacritty-style parser), `x/text` |
| `pty` | Starts a program on a pseudo-terminal. On Windows that's a ConPTY pseudo console, and on Linux a Unix pty, for testing. | `x/sys` |
| `termwidget` | The widget: a `gtk.DrawingArea` that draws the emulator's screen with Cairo and Pango, handles input, and moves bytes between the emulator and the pty. | gotk3, the two above |

`emulator` and `pty` are pure Go, so they build and test on any platform, and cross-compile
for Windows with `CGO_ENABLED=0`. Only the widget needs GTK. The widget adds no cgo of its
own: everything goes through gotk3.

Threading: the emulator and the widget belong to the GTK main thread. A goroutine reads the
pty and hands output over with `glib.IdleAdd`, coalescing whatever arrives before the main
thread runs. Another goroutine writes input to the pty in order, so a program that's slow
to read doesn't block the UI.

In this application, `pkg/vte` wraps the widget behind VTE's API. Windows always uses it.
On Linux, building with `-tags nativeterm` uses it instead of VTE, so it can be tried in
the real application and run under the GUI tests:

```sh
go build -tags nativeterm ./cmd/tmux-tabbed-terminal
go test -tags nativeterm ./...
```

## Scope

The target is a tmux client: tmux keeps the scrollback and history and does the
splitting, so the terminal only has to show one screen faithfully. Anything beyond that is
out of scope until something needs it.

**In:**

* The xterm-256color repertoire tmux and full-screen programs use:
  * cursor movement, erasing, inserting and deleting, scroll regions
  * the alternate screen, DEC line drawing, tabs
  * SGR with 16, 256 and 24-bit colors, and bold, dim, italic, underline, reverse,
    invisible and strike
  * wide and combining characters
  * replies to DA, DSR, DECRQM, the OSC 10 and 11 color queries, and the size report
* OSC 8 hyperlinks and the window title.
* Keys, including modifiers and application cursor keys.
* Mouse reporting in X10, normal, button and any-motion modes, in legacy and SGR encoding.
  Wheel scrolling falls back to cursor keys on the alternate screen.
* Focus events and bracketed paste.
* Selecting with the mouse (Shift+drag when the program has the mouse), copying, and
  pasting from the clipboard or the primary selection.
* Matching link regexes across wrapped lines, as `MatchAt` and `MatchAtCell`, for the
  application's link handling.
* Fonts, zoom, the palette, cursor colors and shapes, bold-is-bright, and the bell.

**Out, for now:**

* Scrollback: tmux keeps the history. `SetScrollbackLines` does nothing.
* Input methods (compose and CJK input). gotk3 has no `GtkIMContext` for arbitrary widgets,
  so this needs a small cgo binding.
* SIXEL and other DCS strings, XTGETTCAP, OSC 52, and the application keypad.
* Double- and triple-click selection, hiding the pointer while typing, the hand pointer
  over links, and blinking.
* Box-drawing and block characters drawn by the widget itself. They come from the font
  for now, so they can fall short of the cell's height.
* Rewrapping lines on resize.
* Accessibility (ATK).

## Windows: open questions

These need a real Windows machine. CI's `windows` job only builds.

* **ConPTY isn't transparent.** It parses the program's output and renders it again, and
  turns input into console input records. tmux from MSYS2 or Cygwin should work through
  it, as it does in Windows Terminal, but mouse reporting and some sequences may not
  survive the trip. If they don't, the fallback is a small helper built for Cygwin or
  MSYS2 that runs tmux on a real Cygwin pty and relays its bytes over pipes, as mintty
  does. The `pty.Pty` interface leaves room for that as another backend.
* **A client's tty.** ConPTY has no device path, so `Start` returns "". The application
  then can't `switch-client` a pane's existing client, and starts a new one instead.
* **Hanging up.** The application detaches a client with SIGHUP, which Windows doesn't
  have. Closing the pty is the equivalent there.
* **Finding tmux.** The application needs the path to the tmux of an installed MSYS2 or
  Cygwin.
