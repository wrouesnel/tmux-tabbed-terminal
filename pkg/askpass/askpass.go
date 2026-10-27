// Package askpass lets ssh, run in the background by the application, ask the user for a
// key's passphrase or anything else it needs to know.
//
// ssh runs the program in SSH_ASKPASS with its prompt when it has no terminal to ask on.
// The application points that at its own executable with ModeEnv set, which makes the
// executable run Main instead of opening a window. Main passes the prompt to the
// application's Server over a unix socket in a private directory, and prints the answer
// for ssh to read. Each request carries a random token too, so only the ssh commands the
// application started can ask.
package askpass

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pkg/errors"
)

// Environment variables the application gives ssh, and so Main.
const (
	// ModeEnv makes the executable run Main.
	ModeEnv = "TTT_ASKPASS"
	// SocketEnv is the path of the Server's socket.
	SocketEnv = "TTT_ASKPASS_SOCKET"
	// TokenEnv is the token requests must carry.
	TokenEnv = "TTT_ASKPASS_TOKEN"
)

// Request is what ssh asks.
type Request struct {
	Token string `json:"token"`
	// Prompt is ssh's prompt, such as "Enter passphrase for key '/home/u/.ssh/id_ed25519': ".
	Prompt string `json:"prompt"`
	// Kind is ssh's SSH_ASKPASS_PROMPT: "" for a question answered with text, "confirm"
	// for one answered yes or no, or "none" for a notice, such as to touch a security key.
	Kind string `json:"kind"`
	// PID is the process ID of the ssh asking, so a second request from it for the same
	// key shows the first answer was wrong.
	PID int `json:"pid"`
}

// Response is the user's answer.
type Response struct {
	// OK is false when the user cancelled.
	OK     bool   `json:"ok"`
	Secret string `json:"secret"`
}

// Handler answers a request. It may take as long as the user does.
type Handler func(Request) Response

// Server answers ssh's requests with a Handler.
type Server struct {
	listener net.Listener
	path     string
	token    string
	handler  Handler
}

// Listen starts a server with a socket in dir, which should be private to the user. It
// answers requests with handler, each on its own goroutine, until Close.
func Listen(dir string, handler Handler) (*Server, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil { //nolint:mnd // owner only
		return nil, errors.Wrap(err, "creating the askpass directory")
	}
	token, err := randomHex(16) //nolint:mnd // 128 bits
	if err != nil {
		return nil, err
	}
	name, err := randomHex(4) //nolint:mnd // a name no other instance has
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "askpass-"+name+".sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, errors.Wrap(err, "listening for askpass requests")
	}
	// Windows has no modes to set: there the private directory protects the socket.
	if err := os.Chmod(path, 0o600); err != nil && runtime.GOOS != "windows" { //nolint:mnd // owner only
		_ = listener.Close()
		return nil, errors.Wrap(err, "securing the askpass socket")
	}
	s := &Server{listener: listener, path: path, token: token, handler: handler}
	go s.serve()
	return s, nil
}

// Env returns the environment which makes ssh ask the server, running executable for
// its SSH_ASKPASS.
func (s *Server) Env(executable string) []string {
	env := []string{
		"SSH_ASKPASS=" + executable,
		// OpenSSH 8.4 and later: use SSH_ASKPASS even with a terminal or no display.
		"SSH_ASKPASS_REQUIRE=force",
		ModeEnv + "=1",
		SocketEnv + "=" + s.path,
		TokenEnv + "=" + s.token,
	}
	// Older ssh only uses SSH_ASKPASS when DISPLAY is set, as it isn't on Wayland without
	// Xwayland. It isn't used for anything else here: X11 forwarding isn't asked for.
	if os.Getenv("DISPLAY") == "" {
		env = append(env, "DISPLAY=:0")
	}
	return env
}

// Close stops the server and removes its socket.
func (s *Server) Close() error {
	err := s.listener.Close()
	_ = os.Remove(s.path)
	return err
}

func (s *Server) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.answer(conn)
	}
}

// answer reads one request from a connection and writes the handler's response.
func (s *Server) answer(conn net.Conn) {
	defer conn.Close()
	var req Request
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
		return
	}
	if subtle.ConstantTimeCompare([]byte(req.Token), []byte(s.token)) != 1 {
		return
	}
	req.Token = ""
	_ = json.NewEncoder(conn).Encode(s.handler(req))
}

// Main is the executable run as SSH_ASKPASS: it asks the server whose socket and token
// are in the environment, with the prompt in args, and writes the answer to stdout. It
// returns the exit status for ssh: 0 with an answer, 1 if the user cancelled or the
// server can't be asked.
func Main(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	req := Request{
		Token:  os.Getenv(TokenEnv),
		Prompt: strings.Join(args, " "),
		Kind:   os.Getenv("SSH_ASKPASS_PROMPT"),
		PID:    os.Getppid(),
	}
	resp, err := Ask(ctx, os.Getenv(SocketEnv), req)
	if err != nil {
		fmt.Fprintf(stderr, "askpass: %v\n", err)
		return 1
	}
	if !resp.OK {
		return 1
	}
	if req.Kind == "" {
		fmt.Fprintln(stdout, resp.Secret)
	}
	return 0
}

// Ask sends a request to the server at socket and waits for the answer.
func Ask(ctx context.Context, socket string, req Request) (Response, error) {
	if socket == "" {
		return Response{}, errors.New("no askpass socket in the environment")
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", socket)
	if err != nil {
		return Response{}, errors.Wrap(err, "reaching the application")
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Response{}, errors.Wrap(err, "asking the application")
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return Response{}, errors.Wrap(err, "reading the answer")
	}
	return resp, nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", errors.Wrap(err, "reading random bytes")
	}
	return hex.EncodeToString(b), nil
}
