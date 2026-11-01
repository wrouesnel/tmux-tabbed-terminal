// Command ttt-agent is the agent tmux-tabbed-terminal runs on a remote host: it serves the
// application's requests over its standard input and output, at the far end of an ssh
// connection. It isn't meant to be run by hand. See package agent.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/agent"
	"github.com/wrouesnel/tmux-tabbed-terminal/version"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "version" {
		fmt.Println(version.Version) //nolint:forbidigo // the command's output
		return
	}
	if len(os.Args) != 2 || os.Args[1] != "serve" {
		fmt.Fprintln(os.Stderr, "usage: ttt-agent serve|version")
		os.Exit(2) //nolint:mnd // usage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	if err := agent.Serve(ctx, os.Stdin, os.Stdout, version.Version); err != nil {
		fmt.Fprintln(os.Stderr, "ttt-agent:", err)
		os.Exit(1)
	}
}
