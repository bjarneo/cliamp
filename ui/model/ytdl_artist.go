package model

import (
	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/resolve"
)

// ytdlArtistMsg carries the uploader that yt-dlp reports for the page URL of
// a track that started without an artist.
type ytdlArtistMsg struct {
	path   string
	artist string
	gen    uint64
}

// needsYTDLArtist reports whether track is a yt-dlp page that has no artist.
// yt-dlp lists a SoundCloud profile's tracks, likes and reposts without an
// uploader, so the media controls and scrobblers would get no artist.
func needsYTDLArtist(track playlist.Track) bool {
	return track.Artist == "" && !track.IsLive() && playlist.IsYTDL(track.Path)
}

// ytdlArtistCmd starts a lookup of the uploader of track when it needs one.
// It advances the request generation on every track start, so a lookup for
// a track the listener has already left is dropped.
func (m *Model) ytdlArtistCmd(track playlist.Track) tea.Cmd {
	gen := nextRequest(&m.requests.ytdlArtist)
	if !needsYTDLArtist(track) {
		return nil
	}
	path := track.Path
	return func() tea.Msg {
		// A single page URL gives the full entry, uploader included.
		tracks, err := resolve.ResolveYTDLBatch(path, 0, 1, "")
		if err != nil || len(tracks) == 0 || tracks[0].Artist == "" {
			return nil
		}
		return ytdlArtistMsg{path: path, artist: tracks[0].Artist, gen: gen}
	}
}

// handleYTDLArtist sets the looked-up artist on the playing track and on its
// queue entry. notifyPlaybackChange then sends it to the media controls.
func (m *Model) handleYTDLArtist(msg ytdlArtistMsg) {
	if msg.gen != m.requests.ytdlArtist {
		return
	}
	if m.playingTrackActive && m.playingTrack.Path == msg.path && m.playingTrack.Artist == "" {
		m.playingTrack.Artist = msg.artist
	}
	if m.playlist == nil {
		return
	}
	idx := m.playlist.Index()
	if t, ok := m.playlist.Track(idx); ok && t.Path == msg.path && t.Artist == "" {
		t.Artist = msg.artist
		m.playlist.SetTrack(idx, t)
	}
}
