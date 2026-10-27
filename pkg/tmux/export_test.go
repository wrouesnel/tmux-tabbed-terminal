package tmux

import "io"

// ProgramName exposes programName to the tests.
var ProgramName = programName

// MasterArgs exposes masterArgs to the tests.
func (s *SSH) MasterArgs() []string { return s.masterArgs() }

// CheckArgs exposes checkArgs to the tests.
func (s *SSH) CheckArgs() []string { return s.checkArgs() }

// NewSizeWriter exposes sizeWriter to the tests.
func NewSizeWriter(w io.Writer, size func(int64)) io.Writer { return &sizeWriter{w: w, size: size} }
