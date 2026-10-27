package tmux

// ProgramName exposes programName to the tests.
var ProgramName = programName

// MasterArgs exposes masterArgs to the tests.
func (s *SSH) MasterArgs() []string { return s.masterArgs() }

// CheckArgs exposes checkArgs to the tests.
func (s *SSH) CheckArgs() []string { return s.checkArgs() }
