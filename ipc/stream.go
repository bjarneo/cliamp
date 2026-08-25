package ipc

import (
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
			return fmt.Errorf("no socket at %s: %w", sockPath, ErrNotRunning)
		}
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()

	reqLine, err := json.Marshal(V2Request{ID: json.RawMessage(`"visstream"`), Method: "spectrum.get"})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	reqLine = append(reqLine, '\n')

	scanner := newFrameScanner(conn)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

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

		var response V2Response
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
		if !response.OK {
			if response.Error == nil {
				return fmt.Errorf("spectrum request failed")
			}
			return response.Error
		}
		var bands Response
		if err := json.Unmarshal(response.Result, &bands); err != nil {
			return fmt.Errorf("decode spectrum: %w", err)
		}
		frame, err := json.Marshal(bands)
		if err != nil {
			return fmt.Errorf("marshal spectrum: %w", err)
		}
		frame = append(frame, '\n')
		if _, err := out.Write(frame); err != nil {
			return err
		}
	}
}
