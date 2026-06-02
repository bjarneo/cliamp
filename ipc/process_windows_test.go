//go:build windows

package ipc

import (
	"math"
	"os"
	"testing"
)

func TestProcessAlive(t *testing.T) {
	tests := []struct {
		name string
		pid  int
		want bool
	}{
		{name: "negative pid", pid: -1, want: false},
		{name: "zero pid", pid: 0, want: false},
		{name: "current process", pid: os.Getpid(), want: true},
		{name: "unlikely pid", pid: math.MaxInt32, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := processAlive(tt.pid)
			if err != nil {
				t.Fatalf("processAlive(%d) unexpected error: %v", tt.pid, err)
			}
			if got != tt.want {
				t.Fatalf("processAlive(%d) = %v, want %v", tt.pid, got, tt.want)
			}
		})
	}
}
