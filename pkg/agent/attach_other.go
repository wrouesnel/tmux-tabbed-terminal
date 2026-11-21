//go:build !unix

package agent

// attach needs a Unix pty: the agent runs on the hosts tmux runs on.
func (c *call) attach(*request) error {
	return &Error{Code: CodeUnimplemented, Message: "terminals need a Unix host"}
}
