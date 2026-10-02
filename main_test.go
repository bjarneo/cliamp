package main

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/external/emby"
	"github.com/bjarneo/cliamp/internal/embyapi"
	"github.com/bjarneo/cliamp/internal/resume"
	"github.com/bjarneo/cliamp/playlist"
)

// cliamp search and cliamp search-sc play the first match of the query. Any
// other arguments pass through as they are.
func TestSearchArgs(t *testing.T) {
	for _, tt := range []struct {
		name    string
		args    []string
		want    []string
		wantErr string
	}{
		{name: "no arguments"},
		{name: "files", args: []string{"a.mp3", "search"}, want: []string{"a.mp3", "search"}},
		{name: "youtube", args: []string{"search", "never", "gonna"}, want: []string{"ytsearch1:never gonna"}},
		{name: "soundcloud", args: []string{"search-sc", "lofi beats"}, want: []string{"scsearch1:lofi beats"}},
		{name: "no query", args: []string{"search"}, wantErr: "search requires a query string"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := searchArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("searchArgs(%q) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}

// At exit, a Jellyfin or Emby stream saves the list it played from when such
// a server is the default provider. Any other track saves only its position,
// and a track with no position saves nothing.
func TestSaveExitResume(t *testing.T) {
	server := emby.NewFromConfig(config.EmbyConfig{
		URL: "https://emby.example.com", Token: "token", UserID: "user-1",
	})
	stream := "https://emby.example.com/Items/two/Download?api_key=token"
	tracks := []playlist.Track{
		{Path: "https://emby.example.com/Items/one/Download?api_key=token", Title: "One"},
		{Path: stream, Title: "Two"},
	}
	for _, tt := range []struct {
		name       string
		path       string
		secs       int
		server     *embyapi.Provider
		want       resume.State
		useContext bool
	}{
		{
			name: "server stream", path: stream, secs: 95, server: server.Provider, useContext: true,
			want: resume.State{Path: stream, PositionSec: 95, Playlist: "Mix", Context: tracks, ContextIndex: 1},
		},
		{
			name: "stream without server", path: stream, secs: 95,
			want: resume.State{Path: stream, PositionSec: 95, Playlist: "Mix"},
		},
		{
			name: "local file with server", path: "/music/two.mp3", secs: 95, server: server.Provider,
			want: resume.State{Path: "/music/two.mp3", PositionSec: 95, Playlist: "Mix"},
		},
		{name: "no position", path: stream, secs: 0, server: server.Provider},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
			usedContext := false
			resumeContext := func() ([]playlist.Track, int) {
				usedContext = true
				return tracks, 1
			}
			saveExitResume(tt.path, tt.secs, "Mix", resumeContext, tt.server)
			if got := resume.Load(); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("saved state = %+v, want %+v", got, tt.want)
			}
			if usedContext != tt.useContext {
				t.Fatalf("context read = %v, want %v", usedContext, tt.useContext)
			}
		})
	}
}
