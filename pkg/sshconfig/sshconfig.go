// Package sshconfig lists the hosts named in an OpenSSH client configuration, to offer as
// destinations.
//
// It reads only what's needed for that: Host patterns, Include, and the HostName and User
// of each host. It is not a full ssh_config parser.
package sshconfig

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Source reads the files of a configuration, on this machine or another host. Paths are
// POSIX paths on that host.
type Source interface {
	// ReadFile returns a file's contents, or an error satisfying os.IsNotExist if it
	// doesn't exist.
	ReadFile(path string) ([]byte, error)
	// Glob returns the files matching a pattern.
	Glob(pattern string) ([]string, error)
	// Home is the user's home directory.
	Home() string
}

// localSource reads this machine's files.
type localSource struct{}

func (localSource) ReadFile(p string) ([]byte, error)     { return os.ReadFile(p) }
func (localSource) Glob(pattern string) ([]string, error) { return filepath.Glob(pattern) }
func (localSource) Home() string {
	home, _ := os.UserHomeDir()
	return home
}

// maxIncludeDepth bounds nested Include, as ssh does, so a loop can't recurse forever.
const maxIncludeDepth = 16

// Host is one concrete host alias from the configuration.
type Host struct {
	// Alias is the name after Host, which ssh accepts as a destination.
	Alias string
	// HostName and User are the first values set in the alias's Host block, if any.
	HostName string
	User     string
}

// DefaultPath returns the user's ssh client configuration, ~/.ssh/config.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ssh", "config")
}

// isPattern reports whether a Host argument is a pattern rather than one host: it has
// wildcards, or negates.
func isPattern(s string) bool {
	return strings.ContainsAny(s, "*?") || strings.HasPrefix(s, "!")
}

// parser accumulates hosts across a file and its includes.
type parser struct {
	src    Source
	sshDir string
	hosts  []Host
	index  map[string]int
	// current are the indexes of the hosts the Host block being read applies to.
	current []int
	seen    map[string]bool
}

// Load reads the hosts of the configuration at path, following Include. A missing file
// has no hosts. Relative Include paths are under the directory of path, as ssh resolves
// them under ~/.ssh for the user configuration.
func Load(path string) ([]Host, error) {
	return LoadFrom(localSource{}, path)
}

// LoadFrom reads the hosts of the configuration at path from a source, as Load does.
func LoadFrom(src Source, configPath string) ([]Host, error) {
	p := &parser{src: src, sshDir: path.Dir(configPath), index: map[string]int{}, seen: map[string]bool{}}
	if err := p.file(configPath, 0); err != nil {
		return nil, err
	}
	return p.hosts, nil
}

func (p *parser) file(filePath string, depth int) error {
	if depth > maxIncludeDepth || p.seen[filePath] {
		return nil
	}
	p.seen[filePath] = true
	data, err := p.src.ReadFile(filePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		keyword, args := splitLine(scanner.Text())
		switch strings.ToLower(keyword) {
		case "host":
			p.current = nil
			for _, arg := range args {
				if isPattern(arg) {
					continue
				}
				i, ok := p.index[arg]
				if !ok {
					i = len(p.hosts)
					p.index[arg] = i
					p.hosts = append(p.hosts, Host{Alias: arg})
				}
				p.current = append(p.current, i)
			}
		case "match":
			// A Match block's settings don't belong to the hosts above.
			p.current = nil
		case "hostname":
			p.set(args, func(h *Host, v string) { h.HostName = v }, func(h *Host) string { return h.HostName })
		case "user":
			p.set(args, func(h *Host, v string) { h.User = v }, func(h *Host) string { return h.User })
		case "include":
			// Include in a Host block is conditional on it in ssh. Its hosts are listed all
			// the same: they're still names the user has configured.
			current := p.current
			for _, arg := range args {
				if err := p.include(arg, depth); err != nil {
					return err
				}
			}
			p.current = current
		}
	}
	return scanner.Err()
}

// set gives the hosts of the current block a value, keeping the first one, as ssh does.
func (p *parser) set(args []string, set func(*Host, string), get func(*Host) string) {
	if len(args) == 0 {
		return
	}
	for _, i := range p.current {
		if get(&p.hosts[i]) == "" {
			set(&p.hosts[i], args[0])
		}
	}
}

// include reads the files an Include argument names.
func (p *parser) include(arg string, depth int) error {
	if strings.HasPrefix(arg, "~/") {
		if home := p.src.Home(); home != "" {
			arg = path.Join(home, arg[2:])
		}
	}
	if !path.IsAbs(arg) {
		arg = path.Join(p.sshDir, arg)
	}
	matches, err := p.src.Glob(arg)
	if err != nil {
		return nil //nolint:nilerr // a bad pattern includes nothing, rather than failing the list
	}
	for _, m := range matches {
		if err := p.file(m, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// splitLine returns a configuration line's keyword and arguments. Keywords are separated
// from arguments by space or "=", arguments may be double-quoted, and lines starting with
// "#" are comments.
func splitLine(line string) (string, []string) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", nil
	}
	end := strings.IndexAny(line, " \t=")
	if end < 0 {
		return line, nil
	}
	keyword := line[:end]
	rest := strings.TrimLeft(line[end:], " \t")
	rest = strings.TrimLeft(strings.TrimPrefix(rest, "="), " \t")

	args := []string{}
	for rest != "" {
		var arg string
		if rest[0] == '"' {
			closing := strings.IndexByte(rest[1:], '"')
			if closing < 0 {
				arg, rest = rest[1:], ""
			} else {
				arg, rest = rest[1:closing+1], rest[closing+2:]
			}
		} else {
			end := strings.IndexAny(rest, " \t")
			if end < 0 {
				arg, rest = rest, ""
			} else {
				arg, rest = rest[:end], rest[end:]
			}
		}
		args = append(args, arg)
		rest = strings.TrimLeft(rest, " \t")
	}
	return keyword, args
}
