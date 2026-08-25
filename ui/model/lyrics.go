package model

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
)

// lyricsArtistTitle resolves the best artist and title for a lyrics lookup.
// For streams with ICY metadata ("Artist - Song"), it parses the stream title.
// For regular tracks, it uses the track's metadata fields.
func (m *Model) lyricsArtistTitle() (artist, title string) {
	track, idx := m.currentPlaybackTrack()
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

func lyricsLookupKey(track playlist.Track, artist, title string) string {
	if artist != "" && title != "" {
		return artist + "\n" + title
	}
	if track.EmbeddedLyrics == "" {
		return ""
	}
	if track.Path != "" {
		return "embedded\n" + track.Path
	}
	if track.Title != "" {
		return "embedded\n" + track.Title
	}
	return "embedded"
}

func (m *Model) retryLyrics() tea.Cmd {
	if m.lyrics.loading {
		return nil
	}
	track, _ := m.currentPlaybackTrack()
	artist, title := m.lyricsArtistTitle()
	q := lyricsLookupKey(track, artist, title)
	if q == "" {
		return nil
	}
	m.lyrics.query = q
	m.lyrics.loading = true
	m.lyrics.lines = nil
	m.lyrics.err = nil
	return m.fetchLyricsForTrack(track, artist, title)
}

// lyricsSyncable reports whether synced lyrics can track the current playback
// position. This is true for local files, Navidrome streams (which have
// accurate position tracking), and yt-dlp tracks (whose ytdlPipeStreamer
// reports position from decoded PCM frames). It is false for live radio (ICY —
// position is from stream start, not song start) and for live streams with no
// finite duration, where the position doesn't map to song time.
func (m *Model) lyricsSyncable() bool {
	track, idx := m.currentPlaybackTrack()
	if idx < 0 {
		return false
	}
	// yt-dlp pipe streams track position from decoded frames, so synced lyrics
	// can follow them. Exclude streams without a known duration (e.g. YouTube
	// Live), where the position is not relative to the song.
	if playlist.IsYTDL(track.Path) {
		return track.DurationSecs > 0
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
