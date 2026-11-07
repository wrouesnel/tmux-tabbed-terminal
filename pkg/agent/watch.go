package agent

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/agent/agentpb"
	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/filewatch"
)

// watchPath is where a Watch request's path is: in the home directory if it starts with
// ~/, and relative to the agent's directory, which is the home directory, if it's not
// absolute.
func watchPath(p string) (string, error) {
	if p == "" {
		return "", status.Error(codes.InvalidArgument, "no file to watch")
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", status.Error(codes.FailedPrecondition, err.Error())
		}
		return filepath.Join(home, rest), nil
	}
	return p, nil
}

func (s *server) Watch(req *agentpb.WatchRequest, stream grpc.ServerStreamingServer[agentpb.WatchResponse]) error {
	path, err := watchPath(req.GetPath())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	// Sends come from the watch's goroutine only, one at a time; a failed one ends it.
	var sendErr error
	send := func(resp *agentpb.WatchResponse) {
		if sendErr == nil {
			if sendErr = stream.Send(resp); sendErr != nil {
				cancel()
			}
		}
	}
	err = filewatch.Watch(ctx, path,
		func() {
			send(&agentpb.WatchResponse{Event: &agentpb.WatchResponse_Started{Started: &agentpb.WatchStarted{}}})
		},
		func() {
			send(&agentpb.WatchResponse{Event: &agentpb.WatchResponse_Changed{Changed: &agentpb.WatchChanged{}}})
		})
	switch {
	case sendErr != nil:
		return sendErr
	case err != nil:
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	return nil
}

// Watch watches a file on the agent's host, calling started once it's watching and
// changed whenever the file changes, until ctx is done or the watch fails. It returns nil
// once ctx is done.
func (c *Client) Watch(ctx context.Context, path string, started, changed func()) error {
	stream, err := c.agent.Watch(ctx, &agentpb.WatchRequest{Path: path})
	if err != nil {
		return err
	}
	for {
		resp, err := stream.Recv()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, io.EOF) {
				return errors.New("the agent stopped watching")
			}
			return err
		}
		switch resp.GetEvent().(type) {
		case *agentpb.WatchResponse_Started:
			started()
		case *agentpb.WatchResponse_Changed:
			changed()
		}
	}
}
