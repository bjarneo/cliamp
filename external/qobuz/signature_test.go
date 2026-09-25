package qobuz

import (
	"testing"
)

func TestMD5Hex(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", "d41d8cd98f00b204e9800998ecf8427e"},
		{"abc", "900150983cd24fb0d6963f7d28e17f72"},
	}
	for _, tt := range tests {
		if got := md5hex(tt.in); got != tt.want {
			t.Errorf("md5hex(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
