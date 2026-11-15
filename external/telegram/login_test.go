package telegram

import "testing"

func TestNormalizePhone(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"+5491100000000", "+5491100000000"},
		{"+54 9 11 1234-5678", "+5491112345678"},
		{"  +54 (9) 11-1234-5678 ", "+5491112345678"},
		{"5491100000000", "5491100000000"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := normalizePhone(tt.in); got != tt.want {
			t.Errorf("normalizePhone(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
