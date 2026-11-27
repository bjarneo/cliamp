package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

type spectrumResponse struct {
	Version int             `json:"version"`
	OK      bool            `json:"ok"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *V2Error        `json:"error,omitempty"`
}

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
	var frameBuf bytes.Buffer

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

		var response spectrumResponse
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
		if response.Version != protocolVersion2 {
			return fmt.Errorf("unexpected protocol version %d", response.Version)
		}
		if !response.OK {
			if response.Error == nil {
				return fmt.Errorf("spectrum request failed")
			}
			return response.Error
		}
		result := bytes.TrimSpace(response.Result)
		if len(result) < 2 || result[0] != '{' || result[len(result)-1] != '}' {
			return fmt.Errorf("decode spectrum: invalid spectrum payload")
		}
		frameBuf.Reset()
		if bytes.IndexByte(result, '\r') >= 0 {
			if err := json.Compact(&frameBuf, result); err != nil {
				return fmt.Errorf("decode spectrum: %w", err)
			}
		} else {
			frameBuf.Write(result)
		}
		frameBuf.WriteByte('\n')
		if _, err := out.Write(frameBuf.Bytes()); err != nil {
			return err
		}
	}
}
