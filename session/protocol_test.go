package session

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		kind    byte
		payload []byte
	}{
		{name: "input keystrokes", kind: kindInput, payload: []byte("jjk ")},
		{name: "output repaint", kind: kindOutput, payload: []byte("\x1b[2J\x1b[Hcliamp")},
		{name: "resize", kind: kindResize, payload: encodeSize(120, 40)},
		{name: "detach carries no payload", kind: kindDetach},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := writeFrame(&buf, tt.kind, tt.payload); err != nil {
				t.Fatalf("writeFrame: %v", err)
			}
			kind, payload, err := readFrame(&buf)
			if err != nil {
				t.Fatalf("readFrame: %v", err)
			}
			if kind != tt.kind {
				t.Errorf("kind = %d, want %d", kind, tt.kind)
			}
			if !bytes.Equal(payload, tt.payload) {
				t.Errorf("payload = %q, want %q", payload, tt.payload)
			}
			if buf.Len() != 0 {
				t.Errorf("%d bytes left after the frame", buf.Len())
			}
		})
	}
}

func TestFramesStreamBackToBack(t *testing.T) {
	var buf bytes.Buffer
	for _, payload := range []string{"a", "bb", "ccc"} {
		if err := writeFrame(&buf, kindInput, []byte(payload)); err != nil {
			t.Fatalf("writeFrame: %v", err)
		}
	}
	for _, want := range []string{"a", "bb", "ccc"} {
		_, payload, err := readFrame(&buf)
		if err != nil {
			t.Fatalf("readFrame: %v", err)
		}
		if string(payload) != want {
			t.Errorf("payload = %q, want %q", payload, want)
		}
	}
	if _, _, err := readFrame(&buf); !errors.Is(err, io.EOF) {
		t.Errorf("err after last frame = %v, want EOF", err)
	}
}

func TestWriteFrameRejectsOversizedPayload(t *testing.T) {
	var buf bytes.Buffer
	if err := writeFrame(&buf, kindOutput, make([]byte, maxFramePayload+1)); !errors.Is(err, errFrameTooLarge) {
		t.Fatalf("err = %v, want errFrameTooLarge", err)
	}
	if buf.Len() != 0 {
		t.Errorf("wrote %d bytes for a rejected frame", buf.Len())
	}
}

// A peer announcing a payload beyond the limit is a protocol violation, not a
// buffer to allocate.
func TestReadFrameRejectsOversizedLength(t *testing.T) {
	header := []byte{kindOutput, 0xFF, 0xFF, 0xFF, 0xFF}
	if _, _, err := readFrame(bytes.NewReader(header)); !errors.Is(err, errFrameTooLarge) {
		t.Fatalf("err = %v, want errFrameTooLarge", err)
	}
}

func TestReadFrameReportsTruncatedFrame(t *testing.T) {
	var buf bytes.Buffer
	if err := writeFrame(&buf, kindOutput, []byte("full payload")); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}
	truncated := buf.Bytes()[:buf.Len()-3]
	if _, _, err := readFrame(bytes.NewReader(truncated)); err == nil {
		t.Fatal("readFrame accepted a truncated frame")
	}
}

func TestSizeCodec(t *testing.T) {
	tests := []struct {
		name                  string
		width, height         int
		wantWidth, wantHeight int
	}{
		{name: "typical", width: 120, height: 40, wantWidth: 120, wantHeight: 40},
		{name: "negative clamps to zero", width: -1, height: -9, wantWidth: 0, wantHeight: 0},
		{name: "huge clamps to the field width", width: 100000, height: 70000, wantWidth: 0xFFFF, wantHeight: 0xFFFF},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			width, height, ok := decodeSize(encodeSize(tt.width, tt.height))
			if !ok {
				t.Fatal("decodeSize rejected an encoded size")
			}
			if width != tt.wantWidth || height != tt.wantHeight {
				t.Errorf("size = %dx%d, want %dx%d", width, height, tt.wantWidth, tt.wantHeight)
			}
		})
	}

	if _, _, ok := decodeSize([]byte{1, 2, 3}); ok {
		t.Error("decodeSize accepted a short payload")
	}
}
