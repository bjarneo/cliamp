package model

import (
	"strings"

	"cliamp/playlist"
)

// lyricsArtistTitle resolves the best artist and title for a lyrics lookup.
// For streams with ICY metadata ("Artist - Song"), it parses the stream title.
// For regular tracks, it uses the track's metadata fields.
func (m *Model) lyricsArtistTitle() (artist, title string) {
	track, idx := m.playlist.Current()
	if idx < 0 {
		return "", ""
	}
	// For streams, prefer the live ICY stream title which updates per-song.
	if m.streamTitle != "" && track.Stream {
		if a, t, ok := strings.Cut(m.streamTitle, " - "); ok {
			return strings.TrimSpace(a), strings.TrimSpace(t)
		}
	}
	return track.Artist, track.Title
}

// lyricsSyncable reports whether synced lyrics can track the current playback
// position. This is true for local files and Navidrome streams (which have
// accurate position tracking), but false for live radio (ICY — position is
// from stream start, not song start) and yt-dlp pipe streams (position is 0).
func (m *Model) lyricsSyncable() bool {
	track, idx := m.playlist.Current()
	if idx < 0 {
		return false
	}
	// YouTube/yt-dlp pipe streams report position 0.
	if playlist.IsYouTubeURL(track.Path) || playlist.IsYTDL(track.Path) {
		return false
	}
	// ICY radio streams: position counts from stream connect, not song start.
	// Provider streams with metadata (e.g. Navidrome) track position correctly.
	if track.Stream && len(track.ProviderMeta) == 0 {
		return false
	}
	return true
}

// lyricsHaveTimestamps reports whether the loaded lyrics have meaningful
// timestamps (i.e., not all lines at 0).
func (m *Model) lyricsHaveTimestamps() bool {
	for _, l := range m.lyrics.lines {
		if l.Start > 0 {
			return true
		}
	}
	return false
}
