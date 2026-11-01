//go:build !unix

package agent

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/agent/agentpb"
)

// Attach needs a Unix pty: the agent runs on the hosts tmux runs on.
func (s *server) Attach(grpc.BidiStreamingServer[agentpb.AttachRequest, agentpb.AttachResponse]) error {
	return status.Error(codes.Unimplemented, "terminals need a Unix host")
}
