package ipc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"cliamp/internal/appdir"
)

// DefaultSocketPath returns the default IPC socket path (~/.config/cliamp/cliamp.sock).
func DefaultSocketPath() string {
	dir, err := appdir.Dir()
	if err != nil {
		return filepath.Join(os.TempDir(), "cliamp.sock")
	}
	return filepath.Join(dir, "cliamp.sock")
}

// Send connects to the IPC socket, sends a request, and returns the response.
// The connection is closed after a single request/response exchange.
func Send(sockPath string, req Request) (Response, error) {
	return SendWithDeadline(sockPath, req, 5*time.Second)
}

// SendWithDeadline is like Send but lets the caller override the exchange
// deadline. Plugin commands can legitimately run for minutes (downloads), so
// the generic 5s cap is too short for them.
func SendWithDeadline(sockPath string, req Request, deadline time.Duration) (Response, error) {
	conn, err := dialSocket(sockPath, 3*time.Second)
	if err != nil {
		if isSocketUnavailable(err) {
			return Response{}, fmt.Errorf("cliamp is not running (no socket at %s)", sockPath)
		}
		return Response{}, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(deadline))

	// Encode and send the request as a single JSON line.
	data, err := json.Marshal(req)
	if err != nil {
		return Response{}, fmt.Errorf("marshal request: %w", err)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return Response{}, fmt.Errorf("write: %w", err)
	}

	// Read the response line.
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return Response{}, fmt.Errorf("read response: %w", err)
		}
		return Response{}, fmt.Errorf("no response from server")
	}

	var resp Response
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return Response{}, fmt.Errorf("unmarshal response: %w", err)
	}
	return resp, nil
}
