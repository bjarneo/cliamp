package telegram

import "testing"

func TestUsernameOf(t *testing.T) {
	tests := map[string]string{
		"deezload2bot":         "deezload2bot",
		"@deezload2bot":        "deezload2bot",
		" https://t.me/durov ": "durov",
		"t.me/durov":           "durov",
		"@":                    "",
	}
	for in, want := range tests {
		if got := usernameOf(in); got != want {
			t.Errorf("usernameOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPeerKey(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"u:123:456", "u:123", true},
		{"ch:-100:99", "ch:-100", true},
		{"c:42", "c:42", true},
		{"c:42:9", "", false},
		{"u:123", "", false},
		{"u:x:1", "", false},
		{"deezload2bot", "", false},
	}
	for _, tt := range tests {
		_, got, ok := peerKey(tt.in)
		if ok != tt.ok || got != tt.want {
			t.Errorf("peerKey(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}
