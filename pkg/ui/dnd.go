package ui

import (
	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/gtk"
)

// dragTarget is the drag type of a session dragged from the list. Drops only come from
// this application, which knows the session being dragged, so no data is transferred.
const dragTarget = "application/x-tmux-tabbed-terminal-session"

// dropZone is where in a pane a dragged session lands.
type dropZone int

const (
	// zoneCenter shows the session in the pane.
	zoneCenter dropZone = iota
	// The edge zones split the pane and show the session on that side.
	zoneLeft
	zoneRight
	zoneTop
	zoneBottom
)

// dropEdge is how near an edge, as a fraction of the pane, a drop splits that side.
const dropEdge = 0.25

// zoneAt returns the drop zone at x, y in a pane of size w, h: the nearest edge if it's
// within dropEdge, otherwise the center.
func zoneAt(x, y, w, h int) dropZone {
	if w <= 0 || h <= 0 {
		return zoneCenter
	}
	fx, fy := float64(x)/float64(w), float64(y)/float64(h)
	zone, nearest := zoneCenter, dropEdge
	for _, edge := range []struct {
		zone dropZone
		dist float64
	}{{zoneLeft, fx}, {zoneRight, 1 - fx}, {zoneTop, fy}, {zoneBottom, 1 - fy}} {
		if edge.dist < nearest {
			zone, nearest = edge.zone, edge.dist
		}
	}
	return zone
}

// split returns how a zone splits a pane: the orientation, and whether the new pane goes
// before (left of or above) the old one.
func (z dropZone) split() (gtk.Orientation, bool) {
	switch z {
	case zoneLeft:
		return gtk.ORIENTATION_HORIZONTAL, true
	case zoneTop:
		return gtk.ORIENTATION_VERTICAL, true
	case zoneBottom:
		return gtk.ORIENTATION_VERTICAL, false
	default:
		return gtk.ORIENTATION_HORIZONTAL, false
	}
}

// dragTargets are the drag types sessions are dragged and dropped as.
func dragTargets() []gtk.TargetEntry {
	te, err := gtk.TargetEntryNew(dragTarget, gtk.TARGET_SAME_APP, 0)
	if err != nil {
		return nil
	}
	return []gtk.TargetEntry{*te}
}

// dragResultNoTarget is GTK_DRAG_RESULT_NO_TARGET: a drag released where nothing took it.
const dragResultNoTarget = 1

// enumValue reads an enum signal argument, which gotk3 passes as one of several integer
// types.
func enumValue(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case uint:
		return int(n) //nolint:gosec // GTK enums are small
	case uint32:
		return int(n)
	default:
		return -1
	}
}

// pointerPosition returns the pointer's position on the screen.
func pointerPosition() (int, int, bool) {
	display, err := gdk.DisplayGetDefault()
	if err != nil {
		return 0, 0, false
	}
	seat, err := display.GetDefaultSeat()
	if err != nil {
		return 0, 0, false
	}
	pointer, err := seat.GetPointer()
	if err != nil {
		return 0, 0, false
	}
	var x, y int
	if err := pointer.GetPosition(nil, &x, &y); err != nil {
		return 0, 0, false
	}
	return x, y, true
}

// contains reports whether a screen position is inside the window.
func (w *Window) contains(x, y int) bool {
	wx, wy := w.window.GetPosition()
	width, height := w.window.GetSize()
	return x >= wx && x < wx+width && y >= wy && y < wy+height
}

// dropAction is the drag action sessions use.
const dropAction = gdk.ACTION_COPY
