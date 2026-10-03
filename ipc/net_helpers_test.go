package ipc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestIsSocketUnavailable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "windows dead network AF_UNIX error",
			err:  errors.New("connect: A socket operation encountered a dead network"),
			want: true,
		},
		{
			name: "actively refused",
			err:  errors.New("connect: No connection could be made because the target machine actively refused it"),
			want: true,
		},
		{
			name: "unrelated network error",
			err:  errors.New("connect: some other error"),
			want: false,
		},
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "wrapped not-exist",
			err:  fmt.Errorf("dial: %w", os.ErrNotExist),
			want: true,
		},
		{
			name: "wrapped ECONNREFUSED",
			err:  fmt.Errorf("dial: %w", syscall.ECONNREFUSED),
			want: true,
		},
		{
			name: "wrapped ENOTSOCK",
			err:  fmt.Errorf("dial: %w", syscall.ENOTSOCK),
			want: true,
		},
		{
			name: "WSAECONNREFUSED error",
			err:  syscall.Errno(10061),
			want: true,
		},
		{
			name: "wrapped WSAECONNREFUSED error",
			err:  fmt.Errorf("dial: %w", syscall.Errno(10061)),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSocketUnavailable(tt.err); got != tt.want {
				t.Fatalf("isSocketUnavailable(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// A socket path that does not fit in sun_path gives an error that says so,
// from the server, the probe and the client. The longest path that fits
// still works.
func TestSocketPathTooLong(t *testing.T) {
	dir := shortTempDir(t)
	pathOfLen := func(n int) string {
		prefix := dir + string(filepath.Separator)
		return prefix + strings.Repeat("s", n-len(prefix))
	}
	long := pathOfLen(maxSocketPathLen + 1)
	for _, tt := range []struct {
		name string
		run  func() error
	}{
		{name: "server", run: func() error {
			server, err := NewServer(long)
			if err == nil {
				_ = server.Close()
			}
			return err
		}},
		{name: "probe", run: func() error {
			_, err := Listening(long)
			return err
		}},
		{name: "client", run: func() error {
			_, err := SendV2(long, V2Request{Method: "state.get"})
			return err
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if !errors.Is(err, errSocketPathTooLong) {
				t.Fatalf("error = %v, want errSocketPathTooLong", err)
			}
			if !strings.Contains(err.Error(), "CLIAMP_CONFIG_DIR") {
				t.Fatalf("error = %q, want the CLIAMP_CONFIG_DIR hint", err)
			}
		})
	}

	t.Run("longest path", func(t *testing.T) {
		sock := pathOfLen(maxSocketPathLen)
		server, err := NewServer(sock)
		if err != nil {
			t.Fatalf("NewServer with %d bytes: %v", len(sock), err)
		}
		_ = server.Close()
	})
}
