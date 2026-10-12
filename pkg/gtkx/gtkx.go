// Package gtkx binds the few GTK3 functions this program needs which gotk3 lacks.
package gtkx

/*
#cgo pkg-config: gtk+-3.0
#include <stdlib.h>
#include <gtk/gtk.h>

static GdkDragContext *to_drag_context(void *p) { return GDK_DRAG_CONTEXT(p); }
static GtkWindow *to_window(void *p) { return GTK_WINDOW(p); }

// ttt_fit_size scales an image's size down to fit a box, keeping its proportions.
static void ttt_fit_size(int *w, int *h, const int *box) {
	double s = MIN(1.0, MIN((double)box[0] / *w, (double)box[1] / *h));
	*w = MAX(1, (int)(*w * s));
	*h = MAX(1, (int)(*h * s));
}

static void ttt_size_prepared(GdkPixbufLoader *loader, int w, int h, gpointer box) {
	int fw = w, fh = h;
	ttt_fit_size(&fw, &fh, box);
	if (fw != w || fh != h) {
		gdk_pixbuf_loader_set_size(loader, fw, fh);
	}
}

// ttt_pixbuf_fit decodes an image, decoding it at a smaller size if it's bigger than the
// box, and turns it as its EXIF orientation says.
static GdkPixbuf *ttt_pixbuf_fit(const guchar *data, gsize len, int bw, int bh, GError **err) {
	int box[2] = {bw, bh};
	GdkPixbufLoader *loader = gdk_pixbuf_loader_new();
	g_signal_connect(loader, "size-prepared", G_CALLBACK(ttt_size_prepared), box);
	gboolean ok = gdk_pixbuf_loader_write(loader, data, len, err);
	if (!gdk_pixbuf_loader_close(loader, ok ? err : NULL)) {
		ok = FALSE;
	}
	GdkPixbuf *pb = NULL;
	if (ok && gdk_pixbuf_loader_get_pixbuf(loader) != NULL) {
		pb = gdk_pixbuf_apply_embedded_orientation(gdk_pixbuf_loader_get_pixbuf(loader));
	}
	g_object_unref(loader);
	if (pb != NULL) {
		// Turning it may have swapped its width and height.
		int w = gdk_pixbuf_get_width(pb), h = gdk_pixbuf_get_height(pb);
		ttt_fit_size(&w, &h, box);
		if (w != gdk_pixbuf_get_width(pb) || h != gdk_pixbuf_get_height(pb)) {
			GdkPixbuf *scaled = gdk_pixbuf_scale_simple(pb, w, h, GDK_INTERP_BILINEAR);
			g_object_unref(pb);
			pb = scaled;
		}
	}
	return pb;
}
*/
import "C"

import (
	"errors"
	"unsafe"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
)

// DragContext is a drag context as a signal handler receives it: a *gdk.DragContext, or
// a *glib.Object when gotk3 has no wrapper registered for it.
type DragContext interface{}

func dragContext(ctx DragContext) *C.GdkDragContext {
	var obj *glib.Object
	switch c := ctx.(type) {
	case *gdk.DragContext:
		obj = c.Object
	case *glib.Object:
		obj = c
	}
	if obj == nil {
		return nil
	}
	return C.to_drag_context(unsafe.Pointer(obj.GObject))
}

// DragStatus tells the drag source whether a drop here would be accepted, and how. An
// action of 0 refuses it.
func DragStatus(ctx DragContext, action gdk.DragAction, time uint) {
	C.gdk_drag_status(dragContext(ctx), C.GdkDragAction(action), C.guint32(time))
}

// DragFinish ends a drop.
func DragFinish(ctx DragContext, success bool, time uint) {
	ok := C.gboolean(C.FALSE)
	if success {
		ok = C.TRUE
	}
	C.gtk_drag_finish(dragContext(ctx), ok, C.FALSE, C.guint32(time))
}

// DragSetIconName sets the icon shown under the pointer during a drag.
func DragSetIconName(ctx DragContext, name string) {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	C.gtk_drag_set_icon_name(dragContext(ctx), cname, 0, 0)
}

// ShowURI opens a URI, such as a file:// folder, in the user's default application, as a
// child of window.
func ShowURI(window *gtk.Window, uri string) error {
	curi := C.CString(uri)
	defer C.free(unsafe.Pointer(curi))
	var gerr *C.GError
	if C.gtk_show_uri_on_window(C.to_window(unsafe.Pointer(window.GObject)), curi, C.GDK_CURRENT_TIME, &gerr) == 0 {
		defer C.g_error_free(gerr)
		return errors.New(C.GoString(gerr.message))
	}
	return nil
}

// PixbufFit decodes an image, scaled down to fit width by height if it's bigger, keeping
// its proportions, and turned as its EXIF orientation says. Being free of widgets, it
// may run off the main thread.
func PixbufFit(data []byte, width, height int) (*gdk.Pixbuf, error) {
	if len(data) == 0 {
		return nil, errors.New("no image data")
	}
	var gerr *C.GError
	pb := C.ttt_pixbuf_fit((*C.guchar)(unsafe.Pointer(&data[0])), C.gsize(len(data)), C.int(width), C.int(height), &gerr)
	if gerr != nil {
		defer C.g_error_free(gerr)
		if pb != nil {
			C.g_object_unref(C.gpointer(pb))
		}
		return nil, errors.New(C.GoString(gerr.message))
	}
	if pb == nil {
		return nil, errors.New("not an image")
	}
	return &gdk.Pixbuf{Object: glib.Take(unsafe.Pointer(pb))}, nil
}
