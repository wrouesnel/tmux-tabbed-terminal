// Package gtkx binds the few GTK3 functions this program needs which gotk3 lacks.
package gtkx

/*
#cgo pkg-config: gtk+-3.0
#include <stdlib.h>
#include <gtk/gtk.h>

static GdkDragContext *to_drag_context(void *p) { return GDK_DRAG_CONTEXT(p); }
*/
import "C"

import (
	"unsafe"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
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
