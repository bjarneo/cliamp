package ipc

import (
	"errors"
	"fmt"
	"os"
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
