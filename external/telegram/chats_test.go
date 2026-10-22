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
