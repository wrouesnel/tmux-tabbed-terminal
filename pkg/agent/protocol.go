// Package agent is the program the application runs on a remote host, and the
// application's side of talking to it. The agent serves calls over its standard input and
// output, at the far end of a plain ssh connection, so one connection carries every
// command, file and terminal, with no ssh connection sharing, which Windows' ssh doesn't
// support.
//
// The two ends run a stdiomux link over the pipes: each call is one yamux stream, of
// stdiomux frames. The application sends a request frame saying what to do, and the agent
// answers with a reply frame, saying whether it could. What follows depends on the call:
//
//   - hello: nothing. The reply has the agent's version and platform.
//   - run: runs a program, without a shell. The application sends input frames, the
//     program's standard input, then an end frame. The agent sends output frames, its
//     standard output, then an exit frame with how it ended and its standard error.
//   - attach: runs a program on a new pseudo-terminal, named in the reply. The
//     application sends input frames, typing into it, and resize frames. The agent sends
//     output frames, what it writes, then an exit frame.
//   - watch: watches a file. The agent replies once it's watching, then sends a changed
//     frame each time the file changes.
//
// The stream ending, or being reset, before the call is done cancels it: a program is
// killed, and a watch stops. A reply frame after the first says the call failed.
package agent

import (
	"github.com/pkg/errors"
)

// Frame types.
const (
	frameRequest = 'q'
	frameReply   = 'r'
	frameInput   = 'i'
	frameEnd     = 'n'
	frameResize  = 'z'
	frameOutput  = 'o'
	frameExit    = 'e'
	frameChanged = 'c'
)

// resizeSize is a resize frame's size: the columns, then the rows, as big-endian uint32s.
const resizeSize = 8

// Calls.
const (
	callHello  = "hello"
	callRun    = "run"
	callAttach = "attach"
	callWatch  = "watch"
)

// request starts a call.
type request struct {
	Call string `json:"call"`
	// Version is the application's, for hello.
	Version string `json:"version,omitempty"`
	// Argv is the program to run, for run and attach: argv[0] is looked up in the
	// agent's PATH, which is the PATH of the ssh session that started it.
	Argv []string `json:"argv,omitempty"`
	// Cols, Rows and Env are an attached terminal's size, and environment variables to
	// set beyond the agent's own, such as TERM.
	Cols int               `json:"cols,omitempty"`
	Rows int               `json:"rows,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
	// Path is the file to watch: absolute, in the home directory if it starts with ~/,
	// or relative to the directory the agent runs in, which is the home directory.
	Path string `json:"path,omitempty"`
}

// reply answers a request. A call that failed has Error and Code.
type reply struct {
	Error string `json:"error,omitempty"`
	Code  string `json:"code,omitempty"`

	// Hello's.
	Version string `json:"version,omitempty"`
	OS      string `json:"os,omitempty"`
	Arch    string `json:"arch,omitempty"`
	Home    string `json:"home,omitempty"`
	// TTY is the terminal device an attached program runs on, such as /dev/pts/3.
	TTY string `json:"tty,omitempty"`
}

// exit is how a program ended.
type exit struct {
	// Code is the exit status, or -1 if a signal killed it.
	Code int `json:"code"`
	// Signal is the signal that killed it, such as "killed", if one did.
	Signal string `json:"signal,omitempty"`
	// Stderr is the start of its standard error, for run.
	Stderr []byte `json:"stderr,omitempty"`
}

// Error codes.
const (
	CodeUnimplemented      = "unimplemented"
	CodeNotFound           = "not_found"
	CodeInvalidArgument    = "invalid_argument"
	CodeFailedPrecondition = "failed_precondition"
	CodeInternal           = "internal"
)

// Error is a call the agent couldn't do, such as a program that couldn't start.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

// IsUnimplemented reports whether err says the agent doesn't do what was asked, as an
// older agent, or one on a host without terminals, doesn't.
func IsUnimplemented(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == CodeUnimplemented
}

// ConnError is a call that failed because the connection to the agent did.
type ConnError struct {
	Err error
}

func (e *ConnError) Error() string { return "talking to the agent: " + e.Err.Error() }
func (e *ConnError) Unwrap() error { return e.Err }

// IsConnError reports whether err is a call failing because the connection to the agent
// did, so the agent should be started again.
func IsConnError(err error) bool {
	var e *ConnError
	return errors.As(err, &e)
}
