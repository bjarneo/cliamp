package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/deeplink"
	"github.com/bjarneo/cliamp/ipc"
)

// sentOperation is one operation that dispatchDeepLink submitted.
type sentOperation struct {
	operation string
	params    ipc.Request
	timeout   time.Duration
}

// A cliamp:// link maps to a fixed set of operations. Albums and playlists
// cannot be queued. A search plays or queues only a top match that a link may
// play.
func TestDispatchDeepLink(t *testing.T) {
	track := ipc.TrackInfo{Title: "Windowlicker", Path: "https://music.example.com/rest/stream?id=7"}
	for _, tt := range []struct {
		name    string
		uri     string
		search  []ipc.TrackInfo
		want    []sentOperation
		wantErr string
	}{
		{
			name: "play url",
			uri:  "cliamp://play?url=https://example.com/stream.mp3",
			want: []sentOperation{{"url.load", ipc.Request{Path: "https://example.com/stream.mp3", Play: true}, ipcLoadWait}},
		},
		{
			name: "queue url",
			uri:  "cliamp://queue?url=https://example.com/stream.mp3",
			want: []sentOperation{{"url.load", ipc.Request{Path: "https://example.com/stream.mp3"}, ipcLoadWait}},
		},
		{
			name: "play album",
			uri:  "cliamp://play?provider=navidrome&album=a1b2c3",
			want: []sentOperation{{"provider.load_album", ipc.Request{Provider: "navidrome", Album: "a1b2c3"}, ipcLoadWait}},
		},
		{name: "queue album", uri: "cliamp://queue?provider=navidrome&album=a1b2c3", wantErr: "queueing a provider album is not supported"},
		{
			name: "play playlist",
			uri:  "cliamp://play?provider=jellyfin&playlist=xyz789",
			want: []sentOperation{{"provider.load", ipc.Request{Provider: "jellyfin", Playlist: "xyz789"}, ipcLoadWait}},
		},
		{name: "queue playlist", uri: "cliamp://queue?provider=jellyfin&playlist=xyz789", wantErr: "queueing a provider playlist is not supported"},
		{
			name:   "play search",
			uri:    "cliamp://play?provider=ytmusic&q=aphex+twin",
			search: []ipc.TrackInfo{track},
			want: []sentOperation{
				{"provider.search", ipc.Request{Provider: "ytmusic", Query: "aphex twin", Limit: 1}, 60 * time.Second},
				{"track.play", ipc.Request{Track: &track}, ipcWait},
			},
		},
		{
			name:   "queue search",
			uri:    "cliamp://queue?provider=ytmusic&q=aphex+twin",
			search: []ipc.TrackInfo{track},
			want: []sentOperation{
				{"provider.search", ipc.Request{Provider: "ytmusic", Query: "aphex twin", Limit: 1}, 60 * time.Second},
				{"track.queue", ipc.Request{Track: &track}, ipcWait},
			},
		},
		{
			name:    "search without a match",
			uri:     "cliamp://play?provider=ytmusic&q=aphex+twin",
			want:    []sentOperation{{"provider.search", ipc.Request{Provider: "ytmusic", Query: "aphex twin", Limit: 1}, 60 * time.Second}},
			wantErr: `ytmusic has no match for "aphex twin"`,
		},
		{
			name:    "search result a link may not play",
			uri:     "cliamp://play?provider=ytmusic&q=aphex+twin",
			search:  []ipc.TrackInfo{{Title: "Local", Path: "/home/user/music/a.mp3"}},
			want:    []sentOperation{{"provider.search", ipc.Request{Provider: "ytmusic", Query: "aphex twin", Limit: 1}, 60 * time.Second}},
			wantErr: "returned a result a cliamp:// link may not play",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			action, err := deeplink.Parse(tt.uri)
			if err != nil {
				t.Fatal(err)
			}
			var sent []sentOperation
			send := func(operation string, params ipc.Request, timeout time.Duration) (ipc.Response, error) {
				sent = append(sent, sentOperation{operation, params, timeout})
				if operation == "provider.search" {
					return ipc.Response{OK: true, Tracks: tt.search}, nil
				}
				return ipc.Response{OK: true}, nil
			}

			err = dispatchDeepLink(action, send)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(sent, tt.want) {
				t.Fatalf("sent = %+v, want %+v", sent, tt.want)
			}
		})
	}
}

// An error from the running cliamp reaches the caller of the link.
func TestDispatchDeepLinkReturnsSendError(t *testing.T) {
	action, err := deeplink.Parse("cliamp://play?url=https://example.com/stream.mp3")
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("url.load did not finish within 5m0s")
	send := func(string, ipc.Request, time.Duration) (ipc.Response, error) { return ipc.Response{}, want }
	if err := dispatchDeepLink(action, send); !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}
