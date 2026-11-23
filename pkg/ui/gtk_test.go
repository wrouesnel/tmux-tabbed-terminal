package ui

import (
	"sync"

	"github.com/gotk3/gotk3/gtk"
)

//nolint:gochecknoglobals // GTK is set up once for the test binary
var testGTK struct {
	once sync.Once
	err  error
}

// initTestGTK sets GTK up for a test as the application does: gotk3 releases objects on
// the main loop, not from the finalizer goroutine, where unreffing a widget while the
// test's thread works with GTK corrupts it. The thread should be locked to the calling
// goroutine.
func initTestGTK() error {
	testGTK.once.Do(func() {
		if testGTK.err = gtk.InitCheck(nil); testGTK.err == nil {
			installFinalizerQueue()
		}
	})
	return testGTK.err
}
