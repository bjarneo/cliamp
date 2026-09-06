package main

import (
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
