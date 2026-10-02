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

// dropAction is the drag action sessions use.
const dropAction = gdk.ACTION_COPY
