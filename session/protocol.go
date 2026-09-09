// Package session hosts the cliamp TUI on a virtual terminal. The player runs
// detached from any terminal -- under systemd, an autostart entry, or a shell
// job -- and a client attaches to it later over the existing IPC socket to
// borrow its own terminal to the running program.
//
// A session is one bidirectional byte stream. The client opens it with a
// regular V2 "attach" request, the host acknowledges with a V2 response, and
// from then on both sides speak the frame protocol below rather than NDJSON.
// The client must wait for the acknowledgment before sending its first frame:
// the host reads the request through a buffered scanner, so bytes sent early
// can be swallowed with the request line.
package session

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Frame kinds. Input and resize travel client to host, output travels host to
// client, and detach travels either way to end the session cleanly.
const (
	kindInput  byte = 1
	kindOutput byte = 2
	kindResize byte = 3
	kindDetach byte = 4
)

// Detach reasons travel as the (optional) payload of a detach frame so a
// client that did not ask to leave can say why it did.
const (
	reasonTakenOver   = "another client attached to this session"
	reasonHostStopped = "the cliamp session is shutting down"
)

// maxFramePayload bounds one frame. Terminal output frames are a repaint at
// most; input frames are keystrokes.
const maxFramePayload = 1 << 20

// errFrameTooLarge reports a payload length beyond maxFramePayload. It is a
// protocol violation rather than a transport failure, so the peer is dropped.
var errFrameTooLarge = errors.New("session: frame payload too large")

// AttachParams are the client's handshake parameters, sent as the params of
// the V2 attach request.
type AttachParams struct {
	// Width and Height are the client terminal's size in cells.
	Width  int `json:"width"`
	Height int `json:"height"`
	// Client identifies the attaching build, for the host log.
	Client string `json:"client,omitempty"`
}

// AttachResult is the host's handshake reply, carried in the V2 response.
type AttachResult struct {
	// Attached is always true on a successful handshake.
	Attached bool `json:"attached"`
	// TookOver reports that another client was attached and has been detached
	// to make room for this one.
	TookOver bool `json:"took_over,omitempty"`
}

// writeFrame writes one frame. Callers that share a writer must serialize
// their writes.
func writeFrame(w io.Writer, kind byte, payload []byte) error {
	if len(payload) > maxFramePayload {
		return errFrameTooLarge
	}
	var header [5]byte
	header[0] = kind
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return fmt.Errorf("session: write frame header: %w", err)
	}
	if len(payload) == 0 {
		return nil
	}
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("session: write frame payload: %w", err)
	}
	return nil
}

// readFrame reads one frame. It returns io.EOF when the peer closed cleanly
// between frames.
func readFrame(r io.Reader) (kind byte, payload []byte, err error) {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return 0, nil, io.ErrUnexpectedEOF
		}
		return 0, nil, err
	}
	length := binary.BigEndian.Uint32(header[1:])
	if length > maxFramePayload {
		return 0, nil, errFrameTooLarge
	}
	if length == 0 {
		return header[0], nil, nil
	}
	payload = make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, fmt.Errorf("session: read frame payload: %w", err)
	}
	return header[0], payload, nil
}

// encodeSize encodes a terminal size for a resize frame.
func encodeSize(width, height int) []byte {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint16(payload[0:], uint16(clampSize(width)))
	binary.BigEndian.PutUint16(payload[2:], uint16(clampSize(height)))
	return payload
}

// decodeSize decodes a resize frame payload.
func decodeSize(payload []byte) (width, height int, ok bool) {
	if len(payload) != 4 {
		return 0, 0, false
	}
	return int(binary.BigEndian.Uint16(payload[0:])), int(binary.BigEndian.Uint16(payload[2:])), true
}

func clampSize(v int) int {
	if v < 0 {
		return 0
	}
	if v > 0xFFFF {
		return 0xFFFF
	}
	return v
}
