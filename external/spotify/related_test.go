package spotify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"testing"

	connectpb "github.com/devgianlu/go-librespot/proto/spotify/connectstate"
	extmetadatapb "github.com/devgianlu/go-librespot/proto/spotify/extendedmetadata"
	metadatapb "github.com/devgianlu/go-librespot/proto/spotify/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/bjarneo/cliamp/playlist"
)

func TestCanRelateOnlySongs(t *testing.T) {
	p := New(nil, "", 320)
	cases := map[string]bool{
		"spotify:track:69kOkLUCkxIZYexIgSG8rq":   true,
		"spotify:episode:512ojhOuo1ktJprKbVcKyQ": false,
		"https://www.youtube.com/watch?v=x":      false,
		"/music/a.mp3":                           false,
	}
	for path, want := range cases {
		if got := p.CanRelate(playlist.Track{Path: path}); got != want {
			t.Errorf("CanRelate(%q) = %v, want %v", path, got, want)
		}
	}
}

func contextTracks(uris ...string) []*connectpb.ContextTrack {
	out := make([]*connectpb.ContextTrack, len(uris))
	for i, u := range uris {
		out[i] = &connectpb.ContextTrack{Uri: u}
	}
	return out
}

func TestStationTrackURIsPagesUntilEnoughSongs(t *testing.T) {
	pages := [][]*connectpb.ContextTrack{
		contextTracks("spotify:track:seed", "spotify:track:a", "spotify:episode:e", "spotify:track:b"),
		contextTracks("spotify:track:a", "spotify:track:c", "spotify:track:d"),
		contextTracks("spotify:track:e"),
	}
	var asked []int
	page := func(_ context.Context, idx int) ([]*connectpb.ContextTrack, error) {
		asked = append(asked, idx)
		if idx >= len(pages) {
			return nil, io.EOF
		}
		return pages[idx], nil
	}
	got, err := stationTrackURIs(context.Background(), page, "spotify:track:seed", 3)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"spotify:track:a", "spotify:track:b", "spotify:track:c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("uris = %v, want %v", got, want)
	}
	if want := []int{0, 1}; !reflect.DeepEqual(asked, want) {
		t.Fatalf("pages fetched = %v, want %v", asked, want)
	}

	asked = nil
	got, err = stationTrackURIs(context.Background(), page, "spotify:track:seed", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 || len(asked) != 4 {
		t.Fatalf("short station: got %v after pages %v; want 5 songs, stopping at EOF", got, asked)
	}
}

func TestStationTrackURIsFailingPage(t *testing.T) {
	boom := errors.New("radio-router 500")
	failSecond := func(_ context.Context, idx int) ([]*connectpb.ContextTrack, error) {
		if idx == 0 {
			return contextTracks("spotify:track:a"), nil
		}
		return nil, boom
	}
	got, err := stationTrackURIs(context.Background(), failSecond, "spotify:track:seed", 10)
	if err != nil || !reflect.DeepEqual(got, []string{"spotify:track:a"}) {
		t.Fatalf("later page failing: got %v, %v; want the first page's songs", got, err)
	}
	failFirst := func(context.Context, int) ([]*connectpb.ContextTrack, error) { return nil, boom }
	if _, err := stationTrackURIs(context.Background(), failFirst, "spotify:track:seed", 10); !errors.Is(err, boom) {
		t.Fatalf("first page failing: err = %v, want %v", err, boom)
	}
}

func metadataResponse(t *testing.T, tracks map[string]*metadatapb.Track, missing ...string) *extmetadatapb.BatchedExtensionResponse {
	t.Helper()
	arr := &extmetadatapb.EntityExtensionDataArray{ExtensionKind: extmetadatapb.ExtensionKind_TRACK_V4}
	for uri, tr := range tracks {
		data, err := anypb.New(tr)
		if err != nil {
			t.Fatal(err)
		}
		arr.ExtensionData = append(arr.ExtensionData, &extmetadatapb.EntityExtensionData{
			Header:        &extmetadatapb.EntityExtensionDataHeader{StatusCode: 200},
			EntityUri:     uri,
			ExtensionData: data,
		})
	}
	// A payload on a failed entry must still be ignored.
	stale, err := anypb.New(&metadatapb.Track{Name: proto.String("stale")})
	if err != nil {
		t.Fatal(err)
	}
	for _, uri := range missing {
		arr.ExtensionData = append(arr.ExtensionData, &extmetadatapb.EntityExtensionData{
			Header:        &extmetadatapb.EntityExtensionDataHeader{StatusCode: 404},
			EntityUri:     uri,
			ExtensionData: stale,
		})
	}
	return &extmetadatapb.BatchedExtensionResponse{ExtendedMetadata: []*extmetadatapb.EntityExtensionDataArray{arr}}
}

func TestTracksMetadataBatchesAndSkipsMissing(t *testing.T) {
	var uris []string
	for i := range metadataBatchSize + 2 {
		uris = append(uris, fmt.Sprintf("spotify:track:%d", i))
	}
	var batches []int
	fetch := func(_ context.Context, req *extmetadatapb.BatchedEntityRequest) (*extmetadatapb.BatchedExtensionResponse, error) {
		batches = append(batches, len(req.EntityRequest))
		found := map[string]*metadatapb.Track{}
		var missing []string
		for _, e := range req.EntityRequest {
			if e.EntityUri == "spotify:track:1" {
				missing = append(missing, e.EntityUri)
				continue
			}
			found[e.EntityUri] = &metadatapb.Track{Name: proto.String("song " + e.EntityUri)}
		}
		return metadataResponse(t, found, missing...), nil
	}
	meta, err := tracksMetadata(context.Background(), fetch, uris)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{metadataBatchSize, 2}; !reflect.DeepEqual(batches, want) {
		t.Fatalf("batch sizes = %v, want %v", batches, want)
	}
	if len(meta) != len(uris)-1 {
		t.Fatalf("got metadata for %d songs, want %d", len(meta), len(uris)-1)
	}
	if _, ok := meta["spotify:track:1"]; ok {
		t.Fatal("song answered with 404 was kept")
	}
	if got := meta["spotify:track:51"].GetName(); got != "song spotify:track:51" {
		t.Fatalf("second batch song name = %q", got)
	}
}

func TestTrackFromMetadata(t *testing.T) {
	m := &metadatapb.Track{
		Name:     proto.String("Instant Crush"),
		Number:   proto.Int32(5),
		Duration: proto.Int32(337560),
		Artist:   []*metadatapb.Artist{{Name: proto.String("Daft Punk")}, {Name: proto.String("Julian Casablancas")}},
		Album: &metadatapb.Album{
			Name: proto.String("Random Access Memories"),
			Date: &metadatapb.Date{Year: proto.Int32(2013)},
			CoverGroup: &metadatapb.ImageGroup{Image: []*metadatapb.Image{
				{FileId: []byte{0x01}, Size: metadatapb.Image_SMALL.Enum()},
				{FileId: []byte{0xab, 0xcd}, Size: metadatapb.Image_DEFAULT.Enum()},
				{FileId: []byte{0x02}, Size: metadatapb.Image_LARGE.Enum()},
			}},
		},
	}
	got := trackFromMetadata("spotify:track:2cGxRwrMyEAp8dEbuZaVv6", m)
	want := playlist.Track{
		Path:         "spotify:track:2cGxRwrMyEAp8dEbuZaVv6",
		Title:        "Instant Crush",
		Artist:       "Daft Punk, Julian Casablancas",
		Album:        "Random Access Memories",
		AlbumArtURL:  "https://i.scdn.co/image/abcd",
		Year:         2013,
		DurationSecs: 337,
		TrackNumber:  5,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("trackFromMetadata =\n%+v\nwant\n%+v", got, want)
	}
}
