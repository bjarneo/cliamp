package model

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

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
		name          string
		queued        playlist.Track
		msg           ytdlArtistMsg
		lyricsVisible bool
		lyricsQuery   string
		wantArtist    string
		wantQuery     string
	}{
		{name: "lookup for the playing track", queued: playlist.Track{Path: path}, msg: ytdlArtistMsg{path: path, artist: "meat computer", gen: 1}, wantArtist: "meat computer"},
		{name: "stale lookup after a skip", queued: playlist.Track{Path: path}, msg: ytdlArtistMsg{path: path, artist: "meat computer", gen: 0}},
		{name: "lookup for another track", queued: playlist.Track{Path: path}, msg: ytdlArtistMsg{path: other, artist: "meat computer", gen: 1}},
		{name: "artist set meanwhile is kept", queued: playlist.Track{Path: path, Artist: "kept"}, msg: ytdlArtistMsg{path: path, artist: "meat computer", gen: 1}, wantArtist: "kept"},
		{name: "open lyrics panel without query starts lookup", queued: playlist.Track{Path: path, Title: "something in the way"}, msg: ytdlArtistMsg{path: path, artist: "meat computer", gen: 1}, lyricsVisible: true, wantArtist: "meat computer", wantQuery: "meat computer\nsomething in the way"},
		{name: "open lyrics panel with query is kept", queued: playlist.Track{Path: path, Title: "something in the way"}, msg: ytdlArtistMsg{path: path, artist: "meat computer", gen: 1}, lyricsVisible: true, lyricsQuery: "embedded\n" + path, wantArtist: "meat computer", wantQuery: "embedded\n" + path},
		{name: "closed lyrics panel starts no lookup", queued: playlist.Track{Path: path, Title: "something in the way"}, msg: ytdlArtistMsg{path: path, artist: "meat computer", gen: 1}, wantArtist: "meat computer"},
		{name: "stale lookup starts no lyrics", queued: playlist.Track{Path: path, Title: "something in the way"}, msg: ytdlArtistMsg{path: path, artist: "meat computer", gen: 0}, lyricsVisible: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pl := playlist.New()
			pl.Add(tc.queued)
			pl.SetIndex(0)
			m := Model{playlist: pl, playingTrack: tc.queued, playingTrackActive: true}
			m.requests.ytdlArtist = 1
			m.lyrics.visible = tc.lyricsVisible
			m.lyrics.query = tc.lyricsQuery

			updated, cmd := m.Update(tc.msg)
			m = updated.(Model)

			if m.playingTrack.Artist != tc.wantArtist {
				t.Fatalf("playing artist = %q, want %q", m.playingTrack.Artist, tc.wantArtist)
			}
			if got, _ := m.playlist.Track(0); got.Artist != tc.wantArtist {
				t.Fatalf("queue artist = %q, want %q", got.Artist, tc.wantArtist)
			}
			if m.lyrics.query != tc.wantQuery {
				t.Fatalf("lyrics query = %q, want %q", m.lyrics.query, tc.wantQuery)
			}
			startsLookup := tc.wantQuery != tc.lyricsQuery
			if m.lyrics.loading != startsLookup || (cmd != nil) != startsLookup {
				t.Fatalf("lyrics loading = %v, cmd = %v, want lookup %v", m.lyrics.loading, cmd != nil, startsLookup)
			}
		})
	}
}

func TestYTDLArtistLookupCanceledByNextTrack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake yt-dlp is a shell script")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "yt-dlp"), []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	var m Model
	cmd := m.ytdlArtistCmd(playlist.Track{Path: "https://soundcloud.com/hahaswag/something-in-the-way"})
	if cmd == nil {
		t.Fatal("ytdlArtistCmd = nil, want lookup")
	}
	done := make(chan any, 1)
	go func() { done <- cmd() }()

	if next := m.ytdlArtistCmd(playlist.Track{Path: "/music/song.mp3"}); next != nil {
		t.Fatal("ytdlArtistCmd for a local file = lookup, want nil")
	}
	select {
	case msg := <-done:
		if msg != nil {
			t.Fatalf("canceled lookup = %#v, want nil", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lookup still runs after the next track start")
	}
}
