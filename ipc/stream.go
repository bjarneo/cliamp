package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// StreamBands holds one IPC connection open and writes one NDJSON line per
// tick containing the current visualizer bands and mode. It exits cleanly
// when ctx is cancelled or the server closes the socket.
func StreamBands(ctx context.Context, sockPath string, interval time.Duration, out io.Writer) error {
	if interval <= 0 {
		interval = 33 * time.Millisecond
	}

	conn, err := dialSocket(sockPath, 3*time.Second)
	if err != nil {
		if isSocketUnavailable(err) {
			return fmt.Errorf("cliamp is not running (no socket at %s)", sockPath)
		}
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()

	reqLine, err := json.Marshal(Request{Cmd: "bands"})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	reqLine = append(reqLine, '\n')

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Reused per-frame buffer so we make one out.Write call per line.
	frame := make([]byte, 0, 512)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if _, err := conn.Write(reqLine); err != nil {
			return fmt.Errorf("write: %w", err)
		}
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return fmt.Errorf("read: %w", err)
			}
			return nil
		}

		// Pass the response through as-is; it is already NDJSON.
		frame = append(frame[:0], scanner.Bytes()...)
		frame = append(frame, '\n')
		if _, err := out.Write(frame); err != nil {
			return err
		}
	}
}
