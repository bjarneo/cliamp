package spotify

import "testing"

// The switch has broken before. web must never reach the client protocol, and
// client must never fall back to the Web API, or the escape hatch the docs
// promise is not the one the code provides.
func TestResolveAPIMode(t *testing.T) {
	for _, tc := range []struct {
		env        string
		want       apiMode
		usesClient bool
		skipsWeb   bool
	}{
		{"", apiModeAuto, true, false},
		{"auto", apiModeAuto, true, false},
		{"client", apiModeClient, true, true},
		{"CLIENT", apiModeClient, true, true},
		{"  client  ", apiModeClient, true, true},
		{"web", apiModeWeb, false, false},
		{"WEB", apiModeWeb, false, false},
		{"nonsense", apiModeAuto, true, false},
	} {
		t.Setenv(apiModeEnv, tc.env)
		got := resolveAPIMode()
		if got != tc.want {
			t.Errorf("%q: mode = %v, want %v", tc.env, got, tc.want)
		}
		if got.usesClient() != tc.usesClient {
			t.Errorf("%q: usesClient = %v, want %v", tc.env, got.usesClient(), tc.usesClient)
		}
		if got.skipsWeb() != tc.skipsWeb {
			t.Errorf("%q: skipsWeb = %v, want %v", tc.env, got.skipsWeb(), tc.skipsWeb)
		}
	}
}
