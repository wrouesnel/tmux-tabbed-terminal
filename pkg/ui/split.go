package ui

import (
	"github.com/gotk3/gotk3/gtk"
)

// node is a pane or a split in a window's layout tree.
type node interface {
	widget() gtk.IWidget
	getParent() *split
	setParent(s *split)
	leaves() []*Pane
	firstLeaf() *Pane
}

// split shows two nodes side by side or one above the other.
type split struct {
	paned  *gtk.Paned
	first  node
	second node
	parent *split
}

func (s *split) widget() gtk.IWidget { return s.paned }
func (s *split) getParent() *split   { return s.parent }
func (s *split) setParent(p *split)  { s.parent = p }
func (s *split) leaves() []*Pane     { return append(s.first.leaves(), s.second.leaves()...) }
func (s *split) firstLeaf() *Pane    { return s.first.firstLeaf() }

// sibling returns the other child of the split.
func (s *split) sibling(n node) node {
	if s.first == n {
		return s.second
	}
	return s.first
}

// setChild puts n in the slot old occupied.
func (s *split) setChild(old, n node) {
	if s.first == old {
		s.first = n
		s.paned.Pack1(n.widget(), true, false)
	} else {
		s.second = n
		s.paned.Pack2(n.widget(), true, false)
	}
	n.setParent(s)
}

// layout is the tree of panes in a window's terminal area.
type layout struct {
	area *gtk.Box
	root node
}

func newLayout(first *Pane) *layout {
	area, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	area.PackStart(first.widget(), true, true, 0)
	return &layout{area: area, root: first}
}

// detach removes n's widget from its container.
func (l *layout) detach(n node) {
	if parent := n.getParent(); parent != nil {
		parent.paned.Remove(n.widget())
	} else {
		l.area.Remove(n.widget())
	}
}

// replace puts n where old was. old must already be detached.
func (l *layout) replace(old node, parent *split, n node) {
	if parent != nil {
		parent.setChild(old, n)
		return
	}
	l.root = n
	n.setParent(nil)
	l.area.PackStart(n.widget(), true, true, 0)
}

// Split puts added next to target: to the right for horizontal, below for vertical, or to
// the left or above if before is set.
func (l *layout) Split(target *Pane, added *Pane, orientation gtk.Orientation, before bool) {
	size := target.root.GetAllocatedWidth()
	if orientation == gtk.ORIENTATION_VERTICAL {
		size = target.root.GetAllocatedHeight()
	}

	parent := target.getParent()
	l.detach(target)

	paned, _ := gtk.PanedNew(orientation)
	paned.SetWideHandle(true)
	s := &split{paned: paned, first: target, second: added}
	if before {
		s.first, s.second = added, target
	}
	paned.Pack1(s.first.widget(), true, false)
	paned.Pack2(s.second.widget(), true, false)
	target.setParent(s)
	added.setParent(s)
	if size > 1 {
		paned.SetPosition(size / 2) //nolint:mnd // halfway
	}

	l.replace(target, parent, s)
	paned.ShowAll()
}

// Remove takes a pane out of the layout, giving its space to its sibling. It returns the
// sibling node, or nil if the pane was the only one.
func (l *layout) Remove(p *Pane) node {
	s := p.getParent()
	if s == nil {
		return nil
	}
	sibling := s.sibling(p)
	s.paned.Remove(p.widget())
	s.paned.Remove(sibling.widget())

	grandparent := s.getParent()
	l.detach(s)
	l.replace(s, grandparent, sibling)
	p.setParent(nil)
	return sibling
}

// Panes returns every pane, left to right and top to bottom.
func (l *layout) Panes() []*Pane {
	return l.root.leaves()
}
