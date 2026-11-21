package termwidget

/*
#cgo pkg-config: gtk+-3.0
#include <stdint.h>
#include <gtk/gtk.h>

// tttTermDraw and tttTermRelease are drawexport.go's.
extern gboolean tttTermDraw(cairo_t *cr, uintptr_t handle, double x1, double y1, double x2, double y2);
extern void tttTermRelease(uintptr_t handle);

static gboolean ttt_term_draw(GtkWidget *w, cairo_t *cr, gpointer data) {
	double x1, y1, x2, y2;
	cairo_clip_extents(cr, &x1, &y1, &x2, &y2);
	return tttTermDraw(cr, (uintptr_t)data, x1, y1, x2, y2);
}

static void ttt_term_release(gpointer data, GClosure *closure) { tttTermRelease((uintptr_t)data); }

static void ttt_term_connect_draw(uintptr_t widget, uintptr_t handle) {
	g_signal_connect_data((gpointer)widget, "draw", G_CALLBACK(ttt_term_draw), (gpointer)handle, ttt_term_release, 0);
}
*/
import "C"

import (
	"runtime/cgo"
)

// connectDraw has the widget's draw signal call t.draw. It's connected through cgo,
// rather than gotk3's Connect: gotk3 calls back through reflection, wrapping the widget
// and the cairo context each time, and the screen is drawn many times a second. The clip
// extents come from the C side, as gotk3's ClipExtents allocates too.
func (t *Terminal) connectDraw() {
	h := cgo.NewHandle(t)
	C.ttt_term_connect_draw(C.uintptr_t(t.Native()), C.uintptr_t(h))
}
