package httpclient

import (
	"errors"
	"io"
	"strings"
	"testing"
)

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func TestReadJSON(t *testing.T) {
	readErr := errors.New("connection reset")
	tests := []struct {
		name    string
		body    io.Reader
		limit   int64
		want    string
		wantErr error
		anyErr  bool
	}{
		{name: "under limit", body: strings.NewReader(`{"name":"a"}`), limit: 64, want: "a"},
		{name: "exactly at limit", body: strings.NewReader(`{"name":"a"}`), limit: 12, want: "a"},
		{name: "one byte over limit", body: strings.NewReader(`{"name":"ab"}`), limit: 12, wantErr: ErrTooLarge},
		{name: "far over limit", body: strings.NewReader(`{"name":"` + strings.Repeat("x", 4096) + `"}`), limit: 100, wantErr: ErrTooLarge},
		{name: "invalid JSON", body: strings.NewReader(`{"name":`), limit: 64, anyErr: true},
		{name: "read error", body: failingReader{err: readErr}, limit: 64, wantErr: readErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got struct {
				Name string `json:"name"`
			}
			err := ReadJSON(tt.body, tt.limit, &got)
			switch {
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ReadJSON() error = %v, want %v", err, tt.wantErr)
				}
			case tt.anyErr:
				if err == nil || errors.Is(err, ErrTooLarge) {
					t.Fatalf("ReadJSON() error = %v, want a decode error", err)
				}
			default:
				if err != nil {
					t.Fatalf("ReadJSON() error = %v", err)
				}
				if got.Name != tt.want {
					t.Fatalf("name = %q, want %q", got.Name, tt.want)
				}
			}
		})
	}
}

func TestReadBodySizeErrorNamesLimit(t *testing.T) {
	_, err := ReadBody(strings.NewReader("12345"), 4)
	if err == nil || err.Error() != "response too large: exceeds 4 bytes" {
		t.Fatalf("ReadBody() error = %v, want the limit in the message", err)
	}
}
