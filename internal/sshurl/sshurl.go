// Package sshurl parses ssh:// URLs into components for use with the ssh binary.
package sshurl

import (
	"fmt"
	"net"
	"net/url"
)

// Parsed holds the components of an ssh:// URL.
type Parsed struct {
	Host string // hostname or user@hostname
	Port string // port number, or "" for default
	Path string // absolute remote path (starts with /)
}

// SSHArgs returns the ssh command arguments for connecting to this host.
// If a port is specified, -p is included.
func (p Parsed) SSHArgs() []string {
	args := []string{"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=5"}
	if p.Port != "" {
		args = append(args, "-p", p.Port)
	}
	args = append(args, p.Host)
	return args
}

// Parse parses an ssh:// URL into host, port, and path components.
// Accepts formats like ssh://host/path, ssh://user@host/path, ssh://host:2222/path.
func Parse(raw string) (Parsed, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Parsed{}, fmt.Errorf("invalid ssh URL %q: %w", raw, err)
	}
	if u.Scheme != "ssh" {
		return Parsed{}, fmt.Errorf("expected ssh:// scheme, got %q", u.Scheme)
	}
	if u.Path == "" {
		return Parsed{}, fmt.Errorf("ssh URL missing path: %s", raw)
	}

	host := u.Hostname()
	if u.User != nil {
		host = u.User.Username() + "@" + host
	}

	port := u.Port()
	// net/url splits host:port correctly; verify with SplitHostPort for edge cases.
	if port == "" && u.Host != host {
		if _, p, err := net.SplitHostPort(u.Host); err == nil {
			port = p
		}
	}

	return Parsed{
		Host: host,
		Port: port,
		Path: u.Path,
	}, nil
}
