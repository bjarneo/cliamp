package main

import (
	"reflect"
	"testing"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/external/jellyfin"
	"github.com/bjarneo/cliamp/internal/resume"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

func TestRestoreJellyfinContextRestoresAlbumAndActiveTrack(t *testing.T) {
	prov := jellyfin.NewFromConfig(config.JellyfinConfig{
		URL: "https://jf.example.com", Token: "new-token", UserID: "user-1",
	})
	state := resume.State{
		Path:        "https://jf.example.com/Items/two/Download?api_key=old-token",
		PositionSec: 95,
		Context: []playlist.Track{
			{Path: "https://jf.example.com/Items/one/Download?api_key=old-token", Title: "One"},
			{Path: "https://jf.example.com/Items/two/Download?api_key=old-token", Title: "Two"},
			{Path: "https://jf.example.com/Items/three/Download?api_key=old-token", Title: "Three"},
		},
		ContextIndex: 1,
	}

	tracks, index, activePath, ok := restoreJellyfinContext(state, prov)
	if !ok {
		t.Fatal("restoreJellyfinContext() did not restore saved album")
	}
	if len(tracks) != 3 || index != 1 || tracks[index].Title != "Two" {
		t.Fatalf("restored context = len:%d index:%d tracks:%+v", len(tracks), index, tracks)
	}
	if activePath != tracks[index].Path {
		t.Fatalf("active path = %q, want %q", activePath, tracks[index].Path)
	}
	for _, track := range tracks {
		if track.Meta(provider.MetaJellyfinID) == "" {
			t.Fatalf("restored track is missing Jellyfin metadata: %+v", track)
		}
	}
}

func TestRestoreJellyfinContextRejectsSingularLegacyResume(t *testing.T) {
	prov := jellyfin.NewFromConfig(config.JellyfinConfig{
		URL: "https://jf.example.com", Token: "token", UserID: "user-1",
	})
	state := resume.State{
		Path:        "https://jf.example.com/Items/one/Download?api_key=old-token",
		PositionSec: 95,
	}

	if tracks, _, _, ok := restoreJellyfinContext(state, prov); ok || len(tracks) != 0 {
		t.Fatalf("restoreJellyfinContext() = (%+v, %v), want no singular restore", tracks, ok)
	}
}

func TestRestoreJellyfinContextPreservesMixedPlaylistAndDuplicate(t *testing.T) {
	prov := jellyfin.NewFromConfig(config.JellyfinConfig{
		URL: "http://jf.example.com:8096/media", Token: "new-token", UserID: "user-1",
	})
	path := "http://jf.example.com:8096/media/Items/one/Download?api_key=old-token"
	local := playlist.Track{Path: "/music/local.mp3", Title: "Local"}
	foreign := playlist.Track{Path: "https://other.example/Items/two/Download?api_key=foreign-token", Stream: true}
	state := resume.State{
		Path: path, ContextIndex: 2,
		Context: []playlist.Track{{Path: path}, local, {Path: path}, foreign},
	}
	tracks, index, activePath, ok := restoreJellyfinContext(state, prov)
	if !ok || len(tracks) != 4 || index != 2 {
		t.Fatalf("restore = (%+v, %d, %v), want mixed playlist and second duplicate", tracks, index, ok)
	}
	if !reflect.DeepEqual(tracks[1], local) || !reflect.DeepEqual(tracks[3], foreign) {
		t.Fatal("restoration changed unrelated tracks")
	}
	if activePath != tracks[2].Path || tracks[0].Path != tracks[2].Path || activePath == path {
		t.Fatalf("restoration did not refresh both duplicate URLs: %+v", tracks)
	}
	if state.Context[2].Path != path {
		t.Fatal("restoration mutated saved context")
	}
}

func TestRestoreJellyfinContextValidatesActiveEntry(t *testing.T) {
	prov := jellyfin.NewFromConfig(config.JellyfinConfig{
		URL: "https://jf.example.com", Token: "token", UserID: "user-1",
	})
	path := "https://jf.example.com/Items/one/Download?api_key=token"
	for _, tt := range []struct {
		name  string
		state resume.State
		want  bool
	}{
		{name: "invalid index recovered", state: resume.State{Path: path, ContextIndex: -1, Context: []playlist.Track{{Path: path}}}, want: true},
		{name: "missing active path", state: resume.State{Path: "missing", Context: []playlist.Track{{Path: path}}}},
		{name: "foreign active server", state: resume.State{Path: "/music/local.mp3", Context: []playlist.Track{{Path: "/music/local.mp3"}, {Path: path}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, _, ok := restoreJellyfinContext(tt.state, prov); ok != tt.want {
				t.Fatalf("restored = %v, want %v", ok, tt.want)
			}
		})
	}
}
