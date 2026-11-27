package ipc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bjarneo/cliamp/internal/appdir"
)

// V2Client sends version 2 requests over one persistent Unix socket connection.
type V2Client struct {
	mu        sync.Mutex
	conn      net.Conn
	scanner   *bufio.Scanner
	closed    atomic.Bool
	closeOnce sync.Once
	closeErr  error
}

// DialV2 connects to the IPC socket at sockPath and returns a reusable client.
func DialV2(sockPath string) (*V2Client, error) {
	conn, err := dialSocket(sockPath, 3*time.Second)
	if err != nil {
		if isSocketUnavailable(err) {
			return nil, fmt.Errorf("no socket at %s: %w", sockPath, ErrNotRunning)
		}
		return nil, fmt.Errorf("connect: %w", err)
	}
	return &V2Client{
		conn:    conn,
		scanner: newFrameScanner(conn),
	}, nil
}

// Send writes one version 2 request and returns the correlated response.
func (c *V2Client) Send(request V2Request) (V2Response, error) {
	return c.SendWithDeadline(request, 5*time.Second)
}

// SendWithDeadline writes one version 2 request with a caller-selected deadline.
func (c *V2Client) SendWithDeadline(request V2Request, deadline time.Duration) (V2Response, error) {
	if c == nil || c.conn == nil {
		return V2Response{}, fmt.Errorf("client is closed: %w", net.ErrClosed)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() {
		return V2Response{}, fmt.Errorf("client is closed: %w", net.ErrClosed)
	}
	if err := c.conn.SetDeadline(time.Now().Add(deadline)); err != nil {
		return V2Response{}, fmt.Errorf("set deadline: %w", err)
	}
	data, err := json.Marshal(request)
	if err != nil {
		return V2Response{}, fmt.Errorf("marshal request: %w", err)
	}
	if _, err := c.conn.Write(append(data, '\n')); err != nil {
		return V2Response{}, fmt.Errorf("write: %w", err)
	}
	if !c.scanner.Scan() {
		if err := c.scanner.Err(); err != nil {
			return V2Response{}, fmt.Errorf("read response: %w", err)
		}
		return V2Response{}, fmt.Errorf("no response from server: %w", io.EOF)
	}
	var response V2Response
	if err := json.Unmarshal(c.scanner.Bytes(), &response); err != nil {
		return V2Response{}, fmt.Errorf("unmarshal response: %w", err)
	}
	if response.Version != protocolVersion2 {
		return V2Response{}, fmt.Errorf("unexpected protocol version %d", response.Version)
	}
	if !bytes.Equal(bytes.TrimSpace(request.ID), bytes.TrimSpace(response.ID)) {
		return V2Response{}, fmt.Errorf("response ID does not match request")
	}
	return response, nil
}

// Close closes the underlying socket connection.
func (c *V2Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	c.closed.Store(true)
	c.closeOnce.Do(func() {
		c.closeErr = c.conn.Close()
	})
	return c.closeErr
}

// DefaultSocketPath returns the default IPC socket path.
func DefaultSocketPath() string {
	return defaultSocketPath(appdir.Dir, os.Getuid())
}

func defaultSocketPath(dirFn func() (string, error), uid int) string {
	dir, err := dirFn()
	if err != nil {
		return filepath.Join(fallbackSocketDir(uid), "cliamp.sock")
	}
	return filepath.Join(dir, "cliamp.sock")
}

// fallbackSocketDir returns a per-user directory under os.TempDir.
func fallbackSocketDir(uid int) string {
	if uid >= 0 {
		return filepath.Join(os.TempDir(), fmt.Sprintf("cliamp-%d", uid))
	}
	if u, err := user.Current(); err == nil && u.Username != "" {
		if name := sanitizeDirComponent(u.Username); name != "" {
			return filepath.Join(os.TempDir(), "cliamp-"+name)
		}
	}
	return filepath.Join(os.TempDir(), "cliamp-user")
}

func sanitizeDirComponent(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		}
	}
	name := strings.Trim(b.String(), ".")
	if name == "" {
		return ""
	}
	return name
}
