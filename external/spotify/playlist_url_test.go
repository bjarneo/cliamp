package spotify

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	librespot "github.com/devgianlu/go-librespot"
	extmetadatapb "github.com/devgianlu/go-librespot/proto/spotify/extendedmetadata"
	metadatapb "github.com/devgianlu/go-librespot/proto/spotify/metadata"
	"github.com/devgianlu/go-librespot/spclient"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestPlaylistURLID(t *testing.T) {
	const id = "37i9dQZEVXcJZyENOWUFoY"
	for _, tt := range []struct {
		name string
		raw  string
		ok   bool
	}{
		{"URI", "spotify:playlist:" + id, true},
		{"URL with share query", "https://open.spotify.com/playlist/" + id + "?si=share", true},
		{"lookalike host", "https://open.spotify.com.evil/playlist/" + id, false},
		{"track URL", "https://open.spotify.com/track/" + id, false},
		{"extra path", "https://open.spotify.com/playlist/" + id + "/extra", false},
		{"bad URI", "spotify:playlist:../bad", false},
		{"extra URI", "spotify:playlist:" + id + ":extra", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := playlistURLID(tt.raw)
			if ok != tt.ok || ok && got != id {
				t.Fatalf("playlistURLID(%q) = %q, %v", tt.raw, got, ok)
			}
		})
	}
}

func TestResolveContextTracks(t *testing.T) {
	const uri = "spotify:playlist:37i9dQZEVXcJZyENOWUFoY"
	const track = "spotify:track:2FY7b99s15jUprqC0M5NCT"
	for _, tt := range []struct {
		name string
		body string
		want int
		fail bool
	}{
		{"paged", `{"pages":[{"tracks":[{"uri":"` + track + `","metadata":{"title":"Song","artist_name":"Artist","duration":"123000"}}],"next_page_url":"hm://playlist/page2"}]}`, 2, false},
		{"metadata unavailable", `{"pages":[{"tracks":[{"uri":"` + track + `"}]}]}`, 1, false},
		{"missing pages", `{}`, 0, true},
		{"null page", `{"pages":[null]}`, 0, true},
		{"loading", `{"loading":true,"pages":[{}]}`, 0, true},
		{"malformed", `oops`, 0, true},
		{"unavailable tracks", `{"pages":[{"tracks":[{"uri":"spotify:local:bad"},null]}]}`, 0, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("missing playback authentication")
				}
				switch r.URL.Path {
				case "/context-resolve/v1/" + uri:
					_, _ = w.Write([]byte(tt.body))
				case "/playlist/page2":
					_, _ = w.Write([]byte(`{"tracks":[{"uri":"` + track + `"}]}`))
				case "/extended-metadata/v0/extended-metadata":
					if tt.name == "metadata unavailable" {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					body, _ := io.ReadAll(r.Body)
					var request extmetadatapb.BatchedEntityRequest
					if err := proto.Unmarshal(body, &request); err != nil {
						t.Error(err)
					}
					if len(request.EntityRequest) != 1 || request.EntityRequest[0].EntityUri != track {
						t.Errorf("metadata request = %v", &request)
					}
					data, _ := anypb.New(&metadatapb.Track{Name: proto.String("Resolved song"), Artist: []*metadatapb.Artist{{Name: proto.String("Resolved artist")}}, Duration: proto.Int32(200000)})
					response, _ := proto.Marshal(&extmetadatapb.BatchedExtensionResponse{ExtendedMetadata: []*extmetadatapb.EntityExtensionDataArray{{ExtensionKind: extmetadatapb.ExtensionKind_TRACK_V4, ExtensionData: []*extmetadatapb.EntityExtensionData{{EntityUri: track, Header: &extmetadatapb.EntityExtensionDataHeader{StatusCode: 200}, ExtensionData: data}}}}})
					_, _ = w.Write(response)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			sp, err := spclient.NewSpclient(t.Context(), &librespot.NullLogger{}, server.Client(), func(context.Context) string { return strings.TrimPrefix(server.URL, "https://") }, func(context.Context, bool) (string, error) { return "test-token", nil }, "device", "client")
			if err != nil {
				t.Fatal(err)
			}
			tracks, err := resolveContextTracks(t.Context(), sp, uri)
			if (err != nil) != tt.fail {
				t.Fatalf("error = %v", err)
			}
			if len(tracks) != tt.want {
				t.Fatalf("tracks = %v", tracks)
			}
			if len(tracks) == 2 && (tracks[1].Title != "Resolved song" || tracks[1].Artist != "Resolved artist" || tracks[1].DurationSecs != 200) {
				t.Fatalf("resolved metadata = %+v", tracks[1])
			}
			if tt.name == "metadata unavailable" && tracks[0].Title != track {
				t.Fatalf("fallback metadata = %+v", tracks[0])
			}
			if tt.name == "paged" && (tracks[0].Title != "Song" || tracks[0].Artist != "Artist" || tracks[0].DurationSecs != 123 || tracks[0].Stream) {
				t.Fatalf("metadata = %+v", tracks[0])
			}
		})
	}
}
