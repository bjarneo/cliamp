package ipc

import (
	"errors"
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSocketUnavailable(tt.err); got != tt.want {
				t.Fatalf("isSocketUnavailable(%q) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}