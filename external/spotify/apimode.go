package spotify

import (
	"os"
	"strings"
)

// apiMode selects which of the two ways of reading Spotify cliamp uses. The Web
// API is documented and stable but will not serve another user's playlist, a
// Spotify-owned mix, or folders at all. The client protocol -- what librespot
// already speaks for playback -- serves all of those, but is undocumented and
// can change without notice.
type apiMode int

const (
	// apiModeAuto prefers the client protocol and falls back to the Web API, so
	// an endpoint disappearing costs the extra reach rather than the feature.
	apiModeAuto apiMode = iota
	// apiModeClient bypasses the Web API, so a broken internal endpoint
	// surfaces instead of being masked by a fallback. Intended for testing.
	apiModeClient
	// apiModeWeb never uses the client protocol, reproducing the behaviour
	// cliamp had before it was introduced. An escape hatch as well as a test
	// mode.
	apiModeWeb
)

// apiModeEnv names the variable that overrides the mode.
const apiModeEnv = "CLIAMP_SPOTIFY_API"

// resolveAPIMode reads the override. Anything unrecognised means auto, so a
// typo degrades to the default rather than disabling a working path.
func resolveAPIMode() apiMode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(apiModeEnv))) {
	case "client":
		return apiModeClient
	case "web":
		return apiModeWeb
	default:
		return apiModeAuto
	}
}

// usesClient reports whether the client protocol may be used at all.
func (m apiMode) usesClient() bool { return m != apiModeWeb }

// skipsWeb reports whether the Web API should be bypassed entirely, so that a
// broken internal endpoint surfaces instead of being masked by a fallback.
func (m apiMode) skipsWeb() bool { return m == apiModeClient }
