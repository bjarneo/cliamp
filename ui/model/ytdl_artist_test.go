package model

import (
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

func TestNeedsYTDLArtist(t *testing.T) {
	const sc = "https://soundcloud.com/hahaswag/something-in-the-way"
	for _, tc := range []struct {
		name  string
		track playlist.Track
		want  bool
	}{
		{name: "soundcloud profile entry without uploader", track: playlist.Track{Path: sc, Stream: true}, want: true},
		{name: "artist already known", track: playlist.Track{Path: sc, Artist: "meat computer"}},
		{name: "live stream", track: playlist.Track{Path: sc, Realtime: true}},
		{name: "local file", track: playlist.Track{Path: "/music/song.mp3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := needsYTDLArtist(tc.track); got != tc.want {
				t.Fatalf("needsYTDLArtist = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestYTDLArtistFillsPlayingTrackAndQueue(t *testing.T) {
	const (
		path  = "https://soundcloud.com/hahaswag/something-in-the-way"
		other = "https://soundcloud.com/hahaswag/leave-me-alone"
	)
	for _, tc := range []struct {
		name       string
		queued     playlist.Track
		msg        ytdlArtistMsg
		wantArtist string
	}{
		{name: "lookup for the playing track", queued: playlist.Track{Path: path}, msg: ytdlArtistMsg{path: path, artist: "meat computer", gen: 1}, wantArtist: "meat computer"},
		{name: "stale lookup after a skip", queued: playlist.Track{Path: path}, msg: ytdlArtistMsg{path: path, artist: "meat computer", gen: 0}},
		{name: "lookup for another track", queued: playlist.Track{Path: path}, msg: ytdlArtistMsg{path: other, artist: "meat computer", gen: 1}},
		{name: "artist set meanwhile is kept", queued: playlist.Track{Path: path, Artist: "kept"}, msg: ytdlArtistMsg{path: path, artist: "meat computer", gen: 1}, wantArtist: "kept"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pl := playlist.New()
			pl.Add(tc.queued)
			pl.SetIndex(0)
			m := Model{playlist: pl, playingTrack: tc.queued, playingTrackActive: true}
			m.requests.ytdlArtist = 1

			updated, _ := m.Update(tc.msg)
			m = updated.(Model)

			if m.playingTrack.Artist != tc.wantArtist {
				t.Fatalf("playing artist = %q, want %q", m.playingTrack.Artist, tc.wantArtist)
			}
			if got, _ := m.playlist.Track(0); got.Artist != tc.wantArtist {
				t.Fatalf("queue artist = %q, want %q", got.Artist, tc.wantArtist)
			}
		})
	}
}
