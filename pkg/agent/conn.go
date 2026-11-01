// Package agent is the program the application runs on a remote host, and the
// application's side of talking to it. The agent serves the gRPC service in
// proto/tmuxtabbedterminal/agent/v1 over its standard input and output, at the far end of a
// plain ssh connection, so one connection carries every command, file and terminal, with no
// ssh connection sharing, which Windows' ssh doesn't support.
package agent

import (
	"io"
	"net"
	"sync"
	"time"

	"github.com/pkg/errors"
)

// pipeConn is a net.Conn over a pair of pipes, such as a program's standard input and
// output, for gRPC's HTTP/2 to run over. Deadlines are ignored: gRPC only sets them around
// its handshake, and the pipes have none.
type pipeConn struct {
	r       io.Reader
	w       io.Writer
	closers []io.Closer
	once    sync.Once
	closed  chan struct{}
}

func newPipeConn(r io.Reader, w io.Writer, closers ...io.Closer) *pipeConn {
	return &pipeConn{r: r, w: w, closers: closers, closed: make(chan struct{})}
}

func (c *pipeConn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c *pipeConn) Write(p []byte) (int, error) { return c.w.Write(p) }

// Close closes the pipes, once.
func (c *pipeConn) Close() error {
	var err error
	c.once.Do(func() {
		for _, cl := range c.closers {
			if cerr := cl.Close(); cerr != nil && err == nil {
				err = cerr
			}
		}
		close(c.closed)
	})
	return err
}

func (c *pipeConn) LocalAddr() net.Addr              { return pipeAddr{} }
func (c *pipeConn) RemoteAddr() net.Addr             { return pipeAddr{} }
func (c *pipeConn) SetDeadline(time.Time) error      { return nil }
func (c *pipeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *pipeConn) SetWriteDeadline(time.Time) error { return nil }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

// errListenerClosed is Accept's error once its one connection has been taken and the
// listener is closed.
var errListenerClosed = errors.New("listener closed")

// oneConnListener is a net.Listener which accepts one connection, for a gRPC server to
// serve, and then waits until it's closed.
type oneConnListener struct {
	conns  chan net.Conn
	once   sync.Once
	closed chan struct{}
}

func newOneConnListener(conn net.Conn) *oneConnListener {
	l := &oneConnListener{conns: make(chan net.Conn, 1), closed: make(chan struct{})}
	l.conns <- conn
	return l
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.closed:
		return nil, errListenerClosed
	}
}

func (l *oneConnListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *oneConnListener) Addr() net.Addr { return pipeAddr{} }
