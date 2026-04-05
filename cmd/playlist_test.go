package cmd

import "testing"

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"simple", "'simple'"},
		{"with space", "'with space'"},
		{"it's", `'it'\''s'`},
		{"", "''"},
		{"a'b'c", `'a'\''b'\''c'`},
		{`back\slash`, `'back\slash'`},
		{`"double"`, `'"double"'`},
	}
	for _, tt := range tests {
		got := shellQuote(tt.input)
		if got != tt.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
