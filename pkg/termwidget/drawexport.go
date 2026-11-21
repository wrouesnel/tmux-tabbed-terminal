package termwidget

/*
#cgo pkg-config: gtk+-3.0
#include <stdint.h>
#include <gtk/gtk.h>
*/
import "C"

import (
	"runtime/cgo"
	"unsafe"

	"github.com/gotk3/gotk3/cairo"
)

//export tttTermDraw
func tttTermDraw(cr *C.cairo_t, handle C.uintptr_t, x1, y1, x2, y2 C.double) C.gboolean {
	_, _ = x1, x2 // rows are drawn whole
	t, ok := cgo.Handle(handle).Value().(*Terminal)
	if !ok {
		return C.FALSE
	}
	t.draw(cairo.WrapContext(uintptr(unsafe.Pointer(cr))), float64(y1), float64(y2))
	return C.FALSE
}

//export tttTermRelease
func tttTermRelease(handle C.uintptr_t) {
	cgo.Handle(handle).Delete()
}
