package spotify

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	librespot "github.com/devgianlu/go-librespot"
	connectpb "github.com/devgianlu/go-librespot/proto/spotify/connectstate"
	extmetadatapb "github.com/devgianlu/go-librespot/proto/spotify/extendedmetadata"
	metadatapb "github.com/devgianlu/go-librespot/proto/spotify/metadata"
	playerpb "github.com/devgianlu/go-librespot/proto/spotify/player"
	"github.com/devgianlu/go-librespot/spclient"
	"google.golang.org/protobuf/proto"

	"github.com/bjarneo/cliamp/playlist"
)

// maxStationPages bounds how far RelatedTracks pages through a station, which
// Spotify serves 50 songs at a time.
const maxStationPages = 10

// metadataBatchSize is how many songs one extended-metadata request looks up.
const metadataBatchSize = 50

// CanRelate reports whether track is a Spotify song. Podcast episodes have no
// song radio. Implements provider.Relater.
func (p *SpotifyProvider) CanRelate(track playlist.Track) bool {
	return strings.HasPrefix(track.Path, trackURIPrefix)
}

// RelatedTracks returns up to n songs from the seed's song radio, the station
// Spotify plays after a song ends. Station entries are bare URIs, so their
// titles, artists and albums are looked up before returning; songs Spotify
// has no metadata for are left out. Implements provider.Relater.
func (p *SpotifyProvider) RelatedTracks(ctx context.Context, seed playlist.Track, n int) ([]playlist.Track, error) {
	if !p.CanRelate(seed) || n < 1 {
		return nil, nil
	}
	if err := p.ensureSession(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	sess := p.session
	p.mu.Unlock()
	return sess.relatedTracks(ctx, seed.Path, n)
}

// relatedTracks makes its requests without holding s.mu, so a reconnect or
// Close does not wait for the lookup. Closing a session closes its AP, dealer
// and event connections but not its spclient, so a lookup in flight finishes
// on the session it started with.
func (s *Session) relatedTracks(ctx context.Context, seedURI string, n int) ([]playlist.Track, error) {
	s.mu.RLock()
	sess := s.sess
	s.mu.RUnlock()
	if sess == nil {
		return nil, fmt.Errorf("spotify: session closed")
	}
	sp := sess.Spclient()

	station, err := sp.ContextResolveAutoplay(ctx, &playerpb.AutoplayContextRequest{
		ContextUri:     proto.String(seedURI),
		RecentTrackUri: []string{seedURI},
	})
	if err != nil {
		return nil, fmt.Errorf("spotify: song radio: %w", err)
	}
	resolver, err := spclient.NewContextResolver(ctx, &librespot.NullLogger{}, sp, station)
	if err != nil {
		return nil, fmt.Errorf("spotify: song radio: %w", err)
	}
	uris, err := stationTrackURIs(ctx, resolver.Page, seedURI, n)
	if err != nil {
		return nil, fmt.Errorf("spotify: song radio: %w", err)
	}
	meta, err := tracksMetadata(ctx, sp.ExtendedMetadata, uris)
	if err != nil {
		return nil, fmt.Errorf("spotify: song radio metadata: %w", err)
	}
	tracks := make([]playlist.Track, 0, len(uris))
	for _, uri := range uris {
		if m, ok := meta[uri]; ok {
			tracks = append(tracks, trackFromMetadata(uri, m))
		}
	}
	return tracks, nil
}

// stationTrackURIs collects up to n song URIs from a station's pages, skipping
// the seed, repeats and anything that is not a song.
func stationTrackURIs(ctx context.Context, page func(context.Context, int) ([]*connectpb.ContextTrack, error), seedURI string, n int) ([]string, error) {
	seen := map[string]bool{seedURI: true}
	var uris []string
	for idx := 0; idx < maxStationPages && len(uris) < n; idx++ {
		tracks, err := page(ctx, idx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		for _, t := range tracks {
			if !strings.HasPrefix(t.Uri, trackURIPrefix) || seen[t.Uri] {
				continue
			}
			seen[t.Uri] = true
			uris = append(uris, t.Uri)
			if len(uris) == n {
				break
			}
		}
	}
	return uris, nil
}

// tracksMetadata looks up song metadata for uris in batches and returns it by
// URI. Songs the service answers with a non-200 status are left out.
func tracksMetadata(ctx context.Context, fetch func(context.Context, *extmetadatapb.BatchedEntityRequest) (*extmetadatapb.BatchedExtensionResponse, error), uris []string) (map[string]*metadatapb.Track, error) {
	meta := make(map[string]*metadatapb.Track, len(uris))
	for start := 0; start < len(uris); start += metadataBatchSize {
		req := &extmetadatapb.BatchedEntityRequest{}
		for _, uri := range uris[start:min(start+metadataBatchSize, len(uris))] {
			req.EntityRequest = append(req.EntityRequest, &extmetadatapb.EntityRequest{
				EntityUri: uri,
				Query:     []*extmetadatapb.ExtensionQuery{{ExtensionKind: extmetadatapb.ExtensionKind_TRACK_V4}},
			})
		}
		resp, err := fetch(ctx, req)
		if err != nil {
			return nil, err
		}
		for _, item := range resp.GetExtendedMetadata() {
			if item.GetExtensionKind() != extmetadatapb.ExtensionKind_TRACK_V4 {
				continue
			}
			for _, data := range item.GetExtensionData() {
				if data.GetHeader().GetStatusCode() != 200 {
					continue
				}
				var t metadatapb.Track
				if err := data.GetExtensionData().UnmarshalTo(&t); err != nil {
					continue
				}
				meta[data.GetEntityUri()] = &t
			}
		}
	}
	return meta, nil
}

// trackFromMetadata converts extended metadata into a playlist track, filling
// the same fields as trackFromItem does from the Web API except Unplayable:
// go-librespot plays a restricted song through an unrestricted alternative,
// so the original's restrictions don't mean it won't play.
func trackFromMetadata(uri string, t *metadatapb.Track) playlist.Track {
	artists := make([]string, 0, len(t.GetArtist()))
	for _, a := range t.GetArtist() {
		artists = append(artists, a.GetName())
	}
	return playlist.Track{
		Path:         uri,
		Title:        t.GetName(),
		Artist:       strings.Join(artists, ", "),
		Album:        t.GetAlbum().GetName(),
		AlbumArtURL:  coverImageURL(t.GetAlbum().GetCoverGroup().GetImage()),
		Year:         int(t.GetAlbum().GetDate().GetYear()),
		DurationSecs: int(t.GetDuration() / 1000),
		TrackNumber:  int(t.GetNumber()),
	}
}

// coverImageURL picks the 300 px cover, which is what pickCoverImage aims for,
// or the first cover when that size is missing.
func coverImageURL(images []*metadatapb.Image) string {
	var pick *metadatapb.Image
	for _, img := range images {
		if len(img.GetFileId()) == 0 {
			continue
		}
		if pick == nil || img.GetSize() == metadatapb.Image_DEFAULT {
			pick = img
		}
	}
	if pick == nil {
		return ""
	}
	return "https://i.scdn.co/image/" + hex.EncodeToString(pick.GetFileId())
}
