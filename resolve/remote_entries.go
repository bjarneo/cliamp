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
// isAllowedRemoteEntry reports whether a remote playlist entry path uses no
// scheme or an http or https scheme.
func isAllowedRemoteEntry(path string) bool {
	switch URIScheme(path) {
	case "", "http", "https":
		return true
	default:
		return false
	}
}

func filterRemoteEntries(tracks []playlist.Track) []playlist.Track {
	filtered := make([]playlist.Track, 0, len(tracks))
	for _, t := range tracks {
		if isAllowedRemoteEntry(t.Path) {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// filterRemoteM3UEntries removes disallowed URI schemes before track conversion.
func filterRemoteM3UEntries(entries []m3uEntry) []m3uEntry {
	filtered := make([]m3uEntry, 0, len(entries))
	for _, e := range entries {
		if isAllowedRemoteEntry(e.Path) {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// filterRemotePLSEntries removes disallowed URI schemes before track conversion.
func filterRemotePLSEntries(entries []plsEntry) []plsEntry {
	filtered := make([]plsEntry, 0, len(entries))
	for _, e := range entries {
		if isAllowedRemoteEntry(e.File) {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// remoteTrackFromPath builds a Track for a remote playlist entry without
// opening local files.
func remoteTrackFromPath(path string) playlist.Track {
	if playlist.IsURL(path) {
		return playlist.TrackFromPath(path)
	}
	scheme := URIScheme(path)
	if scheme == "http" || scheme == "https" {
		t := playlist.TrackFromPath(scheme + path[len(scheme):])
		t.Path = path
		return t
	}
	return playlist.TrackFromFilename(path)
}

// URIScheme returns the lowercased scheme of s, or "" when s does not begin
// with one. Schemes shorter than two characters are reported as absent so a
// Windows drive letter is not mistaken for one.
func URIScheme(s string) string {
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
