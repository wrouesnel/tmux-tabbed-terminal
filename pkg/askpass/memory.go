package askpass

import (
	"strings"
	"sync"
)

// IsKeyPassphrase reports whether a prompt asks for a key's passphrase, as in
// "Enter passphrase for key '/home/u/.ssh/id_ed25519': ", rather than for a password or a
// host key's confirmation. Only passphrases are remembered.
func IsKeyPassphrase(req Request) bool {
	return req.Kind == "" && strings.Contains(strings.ToLower(req.Prompt), "passphrase for")
}

// Memory remembers keys' passphrases, by prompt, so ssh needn't ask the user for one
// again. It holds them only in memory, for as long as the application runs. It's safe
// for concurrent use.
type Memory struct {
	mu      sync.Mutex
	secrets map[string]string
	// usedBy is the ssh process each passphrase was last given to. A second request
	// from it for the same key means the passphrase was wrong.
	usedBy map[string]int
}

// NewMemory returns an empty Memory.
func NewMemory() *Memory {
	return &Memory{secrets: map[string]string{}, usedBy: map[string]int{}}
}

// Recall returns the remembered passphrase for a request, if there is one it hasn't
// already given the same ssh. A repeated request from that ssh forgets the passphrase:
// the key rejected it.
func (m *Memory) Recall(req Request) (string, bool) {
	if !IsKeyPassphrase(req) {
		return "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	secret, ok := m.secrets[req.Prompt]
	if !ok {
		return "", false
	}
	if m.usedBy[req.Prompt] == req.PID {
		delete(m.secrets, req.Prompt)
		delete(m.usedBy, req.Prompt)
		return "", false
	}
	m.usedBy[req.Prompt] = req.PID
	return secret, true
}

// Remember keeps the passphrase the user gave for a request, which ssh is now trying.
func (m *Memory) Remember(req Request, secret string) {
	if !IsKeyPassphrase(req) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.secrets[req.Prompt] = secret
	m.usedBy[req.Prompt] = req.PID
}

// Given notes that the user answered a request without remembering the answer, so a
// passphrase remembered before isn't taken for the one ssh is now trying.
func (m *Memory) Given(req Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.secrets, req.Prompt)
	delete(m.usedBy, req.Prompt)
}

// Forget drops every remembered passphrase.
func (m *Memory) Forget() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.secrets = map[string]string{}
	m.usedBy = map[string]int{}
}
