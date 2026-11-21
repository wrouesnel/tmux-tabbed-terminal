package agent

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/filewatch"
)

// watchPath is where a watch request's path is: in the home directory if it starts with
// ~/, and relative to the agent's directory, which is the home directory, if it's not
// absolute.
func watchPath(p string) (string, error) {
	if p == "" {
		return "", &Error{Code: CodeInvalidArgument, Message: "no file to watch"}
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", &Error{Code: CodeFailedPrecondition, Message: err.Error()}
		}
		return filepath.Join(home, rest), nil
	}
	return p, nil
}

// watch watches a file, replying once it's watching and sending a changed frame each time
// it changes, until the application ends the call.
func (c *call) watch(req *request) error {
	path, err := watchPath(req.Path)
	if err != nil {
		return err
	}
	go c.readInput(func(byte, []byte) {})
	// Sends come from the watch's goroutine only, one at a time; a failed one ends it.
	var sendErr error
	send := func(f func() error) {
		if sendErr == nil {
			if sendErr = f(); sendErr != nil {
				c.cancel()
			}
		}
	}
	err = filewatch.Watch(c.ctx, path,
		func() { send(func() error { return c.reply(&reply{}) }) },
		func() { send(func() error { return c.w.Write(frameChanged, nil) }) })
	switch {
	case sendErr != nil:
		return sendErr
	case err != nil:
		return &Error{Code: CodeFailedPrecondition, Message: err.Error()}
	}
	return nil
}

// Watch watches a file on the agent's host, calling started once it's watching and
// changed whenever the file changes, until ctx is done or the watch fails. It returns nil
// once ctx is done.
func (c *Client) Watch(ctx context.Context, path string, started, changed func()) error {
	cc, _, err := c.open(ctx, &request{Call: callWatch, Path: path})
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	defer cc.close()
	started()
	for {
		typ, payload, err := cc.r.Next()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, io.EOF) {
				return errors.New("the agent stopped watching")
			}
			return cc.fail(err)
		}
		switch typ {
		case frameChanged:
			changed()
		case frameReply:
			return cc.fail(replyError(payload))
		}
	}
}
