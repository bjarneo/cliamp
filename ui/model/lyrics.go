package model

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/lyrics"
	"github.com/bjarneo/cliamp/playlist"
)

// trackLyricsSource is a provider that has synced lyrics for its own tracks,
// as the Spotify provider has. It returns lyrics.ErrNotFound at once for a
// track that is not its own.
type trackLyricsSource interface {
	TrackLyrics(ctx context.Context, track playlist.Track) ([]lyrics.Line, error)
}

// trackLyricsSources returns the registered providers that have synced
// lyrics, in provider order.
func (m Model) trackLyricsSources() []trackLyricsSource {
	var sources []trackLyricsSource
	for _, entry := range m.providers {
		if s, ok := entry.Provider.(trackLyricsSource); ok {
			sources = append(sources, s)
		}
	}
	return sources
}

// lyricsLookups returns the lyrics lookups for track: the .lrc file next to
// a local audio file first, then sources in their order.
func lyricsLookups(track playlist.Track, sources []trackLyricsSource) []lyrics.Source {
	lookups := make([]lyrics.Source, 0, len(sources)+1)
	if localAudioFile(track.Path) {
		lookups = append(lookups, lyrics.LRCFile(track.Path))
	}
	for _, source := range sources {
		lookups = append(lookups, func(ctx context.Context) ([]lyrics.Line, error) {
			return source.TrackLyrics(ctx, track)
		})
	}
	return lookups
}

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
		if a, t, ok := splitStreamTitle(m.streamTitle); ok {
			return a, t
		}
	}
	return track.Artist, track.Title
}

// lyricsSearch returns the artist and title for the LRCLIB and NetEase
// search. Without both it returns neither, and the track gets only its own
// lyrics, embedded or in an .lrc file: a search for the title alone could
// find another song.
func lyricsSearch(artist, title string) (string, string) {
	if artist == "" || title == "" {
		return "", ""
	}
	return artist, title
}

// localAudioFile reports whether path names a local audio file, which may
// have an .lrc file next to it.
func localAudioFile(path string) bool {
	return plainLocalPath(path) && playlist.IsAudioFile(path)
}

func lyricsLookupKey(track playlist.Track, artist, title string) string {
	if artist != "" && title != "" {
		return artist + "\n" + title
	}
	// Lyrics in the tags of a local file or in an .lrc file next to it need
	// no artist and title.
	if track.EmbeddedLyrics == "" && !localAudioFile(track.Path) {
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
	// Live-stream position is not song-relative, regardless of provider metadata.
	if m.currentPlaybackIsLive(track) {
		return false
	}
	// yt-dlp pipe streams track position from decoded frames, so synced lyrics
	// can follow them. Exclude streams without a known duration (e.g. YouTube
	// Live), where the position is not relative to the song.
	if playlist.IsYTDL(track.Path) {
		return track.DurationSecs > 0
	}
	// For unclassified streams, retain the conservative fallback: bare HTTP
	// streams may be radio, while on-demand providers supply track metadata.
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

// maxLyricsOffset bounds the user-adjustable synced-lyrics drift correction.
const maxLyricsOffset = 10 * time.Second

// lyricsPlaybackPosition returns the playback position adjusted by the
// user's synced-lyrics offset. A positive offset advances the position, so
// a later lyric line becomes active sooner.
func (m Model) lyricsPlaybackPosition() time.Duration {
	if m.clockFresh {
		return m.cachedPos + m.lyrics.offset
	}
	return m.player.Position() + m.lyrics.offset
}

// SetLyricsOffset loads a persisted lyric timestamp offset (ms) at startup.
func (m *Model) SetLyricsOffset(ms int) {
	d := time.Duration(ms) * time.Millisecond
	if d > maxLyricsOffset {
		d = maxLyricsOffset
	}
	if d < -maxLyricsOffset {
		d = -maxLyricsOffset
	}
	m.lyrics.offset = d
}

// nudgeLyricsOffset shifts the synced-lyrics position by delta and persists
// the result. A positive offset advances the active lyric line, correcting
// timestamps that run late; a negative offset delays it. Spotify and
// Musixmatch timestamps are often offset from the master by a constant
// amount per track.
func (m *Model) nudgeLyricsOffset(delta time.Duration) tea.Cmd {
	offset := m.lyrics.offset + delta
	if offset > maxLyricsOffset {
		offset = maxLyricsOffset
	}
	if offset < -maxLyricsOffset {
		offset = -maxLyricsOffset
	}
	m.lyrics.offset = offset
	m.status.Warningf(statusTTLDefault, "Lyrics offset: %s", formatLyricsOffset(offset))
	_ = m.saveConfigFloat("lyrics_offset_ms", float64(offset.Milliseconds()), 0)
	return nil
}

// formatLyricsOffset renders a lyric offset with an explicit sign, e.g. "+0.5s".
func formatLyricsOffset(d time.Duration) string {
	ms := d.Milliseconds()
	sign := "+"
	if ms < 0 {
		sign = "-"
		ms = -ms
	}
	return fmt.Sprintf("%s%.1fs", sign, float64(ms)/1000)
}
