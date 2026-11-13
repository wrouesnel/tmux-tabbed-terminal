package ui

import (
	"fmt"
	"math"
	"time"

	"github.com/gotk3/gotk3/cairo"
	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/gtkx"
)

// The diagram window's zoom: the least and most, the step a wheel notch or button takes,
// and the most a diagram is enlarged to fit the window.
const (
	diagramMinZoom = 0.1
	diagramMaxZoom = 8.0
	diagramZoomStep = 1.2
	diagramMaxFit  = 2.0
	// diagramMaxPixels is the longest side a diagram is drawn at. Beyond it, it's drawn at
	// that and enlarged, rather than held in memory at full size.
	diagramMaxPixels = 8192
	// diagramPad is the space around a diagram fitted to the window.
	diagramPad = 24
	// diagramRedraw is how long the zoom rests before the diagram is drawn sharp at it.
	diagramRedraw = 150 * time.Millisecond
	// The window's default size, at most, and its least.
	diagramWinWidth  = 1200
	diagramWinHeight = 800
	diagramWinMin    = 400
)

// diagramWindow shows a Mermaid diagram on its own, zoomed by the wheel and buttons and
// moved by dragging it. It draws the diagram again, sharp, at each zoom it rests at, and
// in the theme's colors when the theme changes between light and dark.
type diagramWindow struct {
	win  *gtk.Window
	area *gtk.DrawingArea
	// zoomButton shows the zoom, and sets it to 100%.
	zoomButton *gtk.Button
	code       string
	d          mermaidDiagram
	dark       bool
	// zoom is the size it's shown at, as a share of its own, and x, y where its top left
	// corner is in the area. fitted is set while it fits the window, which it keeps
	// doing as the window's resized until it's zoomed or moved.
	zoom   float64
	x, y   float64
	fitted bool
	// pb is the diagram drawn at pbZoom. It's scaled to the zoom until it's drawn again.
	pb     *gdk.Pixbuf
	pbZoom float64
	// gen counts the drawings started, so one finished after another started is dropped.
	gen   int
	timer glib.SourceHandle
	// The drag under way: where the pointer and the diagram started.
	dragging       bool
	fromX, fromY   float64
	startX, startY float64
}

// openDiagram opens a window showing a Mermaid diagram from a document named name, in the
// colors of the theme from has.
func openDiagram(from gtk.IWidget, name, code string) {
	dw := &diagramWindow{code: code, fitted: true, dark: darkText(from.ToWidget())}
	d, ok := mermaidSVG(code, dw.dark)
	if !ok {
		return
	}
	dw.d = d
	dw.win, _ = gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	title := "Diagram"
	if name != "" {
		title += " — " + name
	}
	dw.win.SetTitle(title)
	dw.win.SetDefaultSize(
		max(diagramWinMin, min(diagramWinWidth, int(d.width)+2*diagramPad)),
		max(diagramWinMin, min(diagramWinHeight, int(d.height)+2*diagramPad)))
	dw.win.SetTitlebar(dw.headerBar(title))

	dw.area, _ = gtk.DrawingAreaNew()
	dw.area.SetCanFocus(true)
	dw.area.AddEvents(int(gdk.BUTTON_PRESS_MASK | gdk.BUTTON_RELEASE_MASK | gdk.POINTER_MOTION_MASK |
		gdk.SCROLL_MASK | gdk.SMOOTH_SCROLL_MASK))
	addClass(dw.area, "ttt-markdown")
	dw.area.Connect("draw", func(_ *gtk.DrawingArea, cr *cairo.Context) bool {
		dw.draw(cr)
		return true
	})
	dw.area.Connect("size-allocate", func() {
		if dw.fitted {
			dw.fit()
		} else {
			dw.clamp()
		}
	})
	dw.area.Connect("realize", func() { setCursor(&dw.area.Widget, "grab") })
	dw.area.Connect("style-updated", func() {
		if dark := darkText(dw.area); dark != dw.dark {
			if d, ok := mermaidSVG(dw.code, dark); ok {
				dw.dark, dw.d, dw.pbZoom = dark, d, 0
				dw.render()
			}
		}
	})
	dw.area.Connect("scroll-event", func(_ interface{}, ev *gdk.Event) bool {
		dw.onScroll(gdk.EventScrollNewFromEvent(ev))
		return true
	})
	dw.area.Connect("button-press-event", func(_ interface{}, ev *gdk.Event) bool {
		return dw.onPress(gdk.EventButtonNewFromEvent(ev))
	})
	dw.area.Connect("motion-notify-event", func(_ interface{}, ev *gdk.Event) bool {
		if !dw.dragging {
			return false
		}
		x, y := gdk.EventMotionNewFromEvent(ev).MotionValRoot()
		dw.x, dw.y, dw.fitted = dw.startX+x-dw.fromX, dw.startY+y-dw.fromY, false
		dw.clamp()
		dw.area.QueueDraw()
		return true
	})
	dw.area.Connect("button-release-event", func(_ interface{}, ev *gdk.Event) bool {
		if gdk.EventButtonNewFromEvent(ev).Button() != gdk.BUTTON_PRIMARY || !dw.dragging {
			return false
		}
		dw.dragging = false
		setCursor(&dw.area.Widget, "grab")
		return true
	})
	dw.win.Connect("key-press-event", func(_ interface{}, ev *gdk.Event) bool {
		return dw.onKey(gdk.EventKeyNewFromEvent(ev))
	})
	dw.win.Connect("destroy", func() {
		if dw.timer != 0 {
			glib.SourceRemove(dw.timer)
			dw.timer = 0
		}
		dw.gen++
	})
	dw.win.Add(dw.area)
	dw.win.ShowAll()
	dw.area.GrabFocus()
}

// headerBar makes the window's title bar, with buttons to zoom out, show it at 100%, zoom
// in and fit it to the window.
func (dw *diagramWindow) headerBar(title string) *gtk.HeaderBar {
	bar, _ := gtk.HeaderBarNew()
	bar.SetShowCloseButton(true)
	bar.SetTitle(title)
	bar.SetSubtitle("Scroll to zoom, drag to move")
	box, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	addClass(box, "linked")
	button := func(icon, tip string, do func()) *gtk.Button {
		b, _ := gtk.ButtonNewFromIconName(icon, gtk.ICON_SIZE_BUTTON)
		b.SetTooltipText(tip)
		b.SetCanFocus(false)
		b.Connect("clicked", do)
		box.PackStart(b, false, false, 0)
		return b
	}
	button("zoom-out-symbolic", "Zoom out (-)", func() { dw.zoomCentered(1 / diagramZoomStep) })
	dw.zoomButton, _ = gtk.ButtonNewWithLabel("100%")
	dw.zoomButton.SetTooltipText("Show it at its own size (0)")
	dw.zoomButton.SetCanFocus(false)
	dw.zoomButton.Connect("clicked", func() { dw.zoomCentered(1 / dw.zoom) })
	box.PackStart(dw.zoomButton, false, false, 0)
	button("zoom-in-symbolic", "Zoom in (+)", func() { dw.zoomCentered(diagramZoomStep) })
	bar.PackStart(box)
	fit, _ := gtk.ButtonNewFromIconName("zoom-fit-best-symbolic", gtk.ICON_SIZE_BUTTON)
	fit.SetTooltipText("Fit it to the window (F)")
	fit.SetCanFocus(false)
	fit.Connect("clicked", func() { dw.fit() })
	bar.PackStart(fit)
	return bar
}

// draw paints the page's background, as the markdown view's, and the diagram on it, as
// last drawn, scaled to the zoom.
func (dw *diagramWindow) draw(cr *cairo.Context) {
	if sc, err := dw.area.GetStyleContext(); err == nil {
		gtk.RenderBackground(sc, cr, 0, 0, float64(dw.area.GetAllocatedWidth()), float64(dw.area.GetAllocatedHeight()))
	}
	if dw.pb == nil || dw.pbZoom <= 0 {
		return
	}
	cr.Save()
	cr.Translate(dw.x, dw.y)
	s := dw.zoom / dw.pbZoom
	cr.Scale(s, s)
	gtkx.CairoSetSourcePixbuf(cr, dw.pb, 0, 0)
	cr.Paint()
	cr.Restore()
}

// fit shows the whole diagram, centered, as large as the window has room for, up to
// diagramMaxFit of its own size.
func (dw *diagramWindow) fit() {
	w, h := float64(dw.area.GetAllocatedWidth()), float64(dw.area.GetAllocatedHeight())
	if w <= 1 || h <= 1 {
		return
	}
	z := min((w-2*diagramPad)/dw.d.width, (h-2*diagramPad)/dw.d.height, diagramMaxFit)
	dw.setZoom(max(z, diagramMinZoom))
	dw.fitted = true
	dw.clamp()
	dw.area.QueueDraw()
}

// zoomCentered zooms by a factor about the middle of the window.
func (dw *diagramWindow) zoomCentered(by float64) {
	dw.zoomAt(by, float64(dw.area.GetAllocatedWidth())/2, float64(dw.area.GetAllocatedHeight())/2)
}

// zoomAt zooms by a factor, keeping the point at x, y of the area where it is.
func (dw *diagramWindow) zoomAt(by, x, y float64) {
	old := dw.zoom
	if old <= 0 {
		return
	}
	dw.setZoom(max(diagramMinZoom, min(old*by, diagramMaxZoom)))
	dw.x = x - (x-dw.x)*dw.zoom/old
	dw.y = y - (y-dw.y)*dw.zoom/old
	dw.fitted = false
	dw.clamp()
	dw.area.QueueDraw()
}

// setZoom sets the zoom, shows it, and draws the diagram sharp at it once it rests.
func (dw *diagramWindow) setZoom(z float64) {
	if z == dw.zoom {
		return
	}
	dw.zoom = z
	dw.zoomButton.SetLabel(formatPercent(z))
	if dw.timer != 0 {
		glib.SourceRemove(dw.timer)
	}
	if dw.pb == nil {
		dw.timer = 0
		dw.render()
		return
	}
	dw.timer = glib.TimeoutAdd(uint(diagramRedraw/time.Millisecond), func() bool {
		dw.timer = 0
		dw.render()
		return false
	})
}

// formatPercent shows a zoom as a percentage.
func formatPercent(z float64) string {
	return fmt.Sprintf("%d%%", int(math.Round(z*100))) //nolint:mnd // percent
}

// render draws the diagram at the zoom, or as near as diagramMaxPixels allows, in the
// background.
func (dw *diagramWindow) render() {
	z := min(dw.zoom, diagramMaxPixels/max(dw.d.width, dw.d.height))
	if z <= 0 || (dw.pb != nil && z == dw.pbZoom) {
		return
	}
	dw.gen++
	gen, d := dw.gen, dw.d
	go func() {
		pb, err := drawSVG(d.svg, d.width, max(int(d.width*z), 1))
		glib.IdleAdd(func() {
			if gen != dw.gen || err != nil {
				return
			}
			dw.pb, dw.pbZoom = pb, z
			dw.area.QueueDraw()
		})
	}()
}

// clamp keeps the diagram in view: centered along a side it's smaller than the window
// along, and otherwise with no more than diagramPad of space beyond either edge.
func (dw *diagramWindow) clamp() {
	clampAxis := func(pos *float64, size, view float64) {
		if size+2*diagramPad <= view {
			*pos = (view - size) / 2
			return
		}
		*pos = max(view-size-diagramPad, min(*pos, diagramPad))
	}
	clampAxis(&dw.x, dw.d.width*dw.zoom, float64(dw.area.GetAllocatedWidth()))
	clampAxis(&dw.y, dw.d.height*dw.zoom, float64(dw.area.GetAllocatedHeight()))
}

// onScroll zooms about the pointer with the wheel, and moves sideways with a sideways
// scroll.
func (dw *diagramWindow) onScroll(ev *gdk.EventScroll) {
	var by, dx float64
	switch ev.Direction() {
	case gdk.SCROLL_UP:
		by = diagramZoomStep
	case gdk.SCROLL_DOWN:
		by = 1 / diagramZoomStep
	case gdk.SCROLL_LEFT:
		dx = 1
	case gdk.SCROLL_RIGHT:
		dx = -1
	case gdk.SCROLL_SMOOTH:
		by = math.Pow(diagramZoomStep, -ev.DeltaY())
		dx = -ev.DeltaX()
	}
	if dx != 0 {
		dw.x += dx * 40 //nolint:mnd // pixels a notch
		dw.fitted = false
		dw.clamp()
		dw.area.QueueDraw()
	}
	if by != 0 && by != 1 {
		dw.zoomAt(by, ev.X(), ev.Y())
	}
}

// onPress starts a drag, and a double click switches between fitting the window and
// 100% about the pointer.
func (dw *diagramWindow) onPress(ev *gdk.EventButton) bool {
	if ev.Button() != gdk.BUTTON_PRIMARY {
		return false
	}
	dw.area.GrabFocus()
	if ev.Type() == gdk.EVENT_2BUTTON_PRESS {
		dw.dragging = false
		if dw.fitted && dw.zoom != 1 {
			dw.zoomAt(1/dw.zoom, ev.X(), ev.Y())
		} else {
			dw.fit()
		}
		return true
	}
	if ev.Type() != gdk.EVENT_BUTTON_PRESS {
		return true
	}
	dw.dragging = true
	dw.fromX, dw.fromY = ev.XRoot(), ev.YRoot()
	dw.startX, dw.startY = dw.x, dw.y
	setCursor(&dw.area.Widget, "grabbing")
	return true
}

// onKey zooms with + and -, shows 100% with 0, fits with F, and closes with Escape.
func (dw *diagramWindow) onKey(ev *gdk.EventKey) bool {
	switch ev.KeyVal() {
	case gdk.KEY_plus, gdk.KEY_equal, gdk.KEY_KP_Add:
		dw.zoomCentered(diagramZoomStep)
	case gdk.KEY_minus, gdk.KEY_KP_Subtract:
		dw.zoomCentered(1 / diagramZoomStep)
	case gdk.KEY_0, gdk.KEY_KP_0:
		dw.zoomCentered(1 / dw.zoom)
	case gdk.KEY_f, gdk.KEY_F:
		dw.fit()
	case gdk.KEY_Escape:
		dw.win.Destroy()
	default:
		return false
	}
	return true
}
