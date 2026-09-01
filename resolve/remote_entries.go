package resolve

import (
	"strings"

	"github.com/bjarneo/cliamp/playlist"
)

// filterRemoteEntries drops tracks from a network-fetched playlist whose path
// names a transport other than http or https.
//
// The entry strings in a remote M3U or PLS are chosen by whoever serves it,
// not by the user, and they become track paths that playback dispatches on.
// An ssh:// entry reaches exec.Command("ssh", ...) against a server-named
// host; file:// and data: entries are not playable but are worth refusing on
// the same principle. Fetching a playlist should never let its author pick
// which program cliamp runs.
//
// Entries with no scheme are left exactly as they were. They are relative or
// local paths whose handling predates this check, they cannot name a
// transport, and quietly dropping them would change what users see in their
// playlists. A single-letter prefix is treated as a Windows drive rather than
// a scheme, so "C:\music\a.mp3" survives.
func filterRemoteEntries(tracks []playlist.Track) []playlist.Track {
	filtered := make([]playlist.Track, 0, len(tracks))
	for _, t := range tracks {
		switch uriScheme(t.Path) {
		case "", "http", "https":
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// uriScheme returns the lowercased scheme of s, or "" when s does not begin
// with one. Schemes shorter than two characters are reported as absent so a
// Windows drive letter is not mistaken for one.
func uriScheme(s string) string {
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			continue
		case r >= '0' && r <= '9', r == '+', r == '-', r == '.':
			if i == 0 {
				return ""
			}
		case r == ':':
			if i < 2 {
				return ""
			}
			return strings.ToLower(s[:i])
		default:
			return ""
		}
	}
	return ""
}
