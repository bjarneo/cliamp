package spotify

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	extmetapb "github.com/devgianlu/go-librespot/proto/spotify/extendedmetadata"
	metadatapb "github.com/devgianlu/go-librespot/proto/spotify/metadata"
	playerpb "github.com/devgianlu/go-librespot/proto/spotify/player"

	"github.com/bjarneo/cliamp/playlist"
)

// Reading a playlist through Spotify's client protocol takes two steps: the
// context resolves to an ordered list of track URIs, then metadata for those
// URIs is fetched in batches. It is the only way to read playlists the Web API
// refuses -- another user's list, or a Spotify-owned mix -- and it is not
// subject to the Web API's quota.

// contextTrackURIs resolves every track URI in a playlist, in order. The result
// is not cached anywhere shared: it is a snapshot of the playlist at this
// instant, and the read that asked for it owns it for as long as that read
// lasts. Two reads of the same list each get their own, so neither can slice
// the other's.
func (p *SpotifyProvider) contextTrackURIs(ctx context.Context, playlistID string) ([]string, error) {
	sess := p.session
	if sess == nil || sess.sess == nil {
		return nil, fmt.Errorf("spotify: context resolve: no session")
	}
	content, err := sess.sess.Spclient().ContextResolve(ctx, contextURIFor(playlistID))
	if err != nil {
		return nil, fmt.Errorf("spotify: context resolve %q: %w", playlistID, err)
	}

	// A resolve answers with every track inline: probed against this account's
	// largest lists, a 6078-track collection and a 1693-track playlist each came
	// back as one page with no continuation. The page type can carry one
	// (next_page_url), though, and because the list length below becomes the
	// total, a truncated resolve would look like a short list rather than an
	// error. Refuse it instead and let the Web API serve the list.
	var uris []string
	for _, page := range content.GetPages() {
		if page.GetNextPageUrl() != "" {
			return nil, fmt.Errorf("spotify: context resolve %q: paged response", playlistID)
		}
		for _, tr := range page.GetTracks() {
			// Podcast episodes and the user's own local files ride in the same
			// list; neither is playable from here, so only tracks are kept.
			if uri := tr.GetUri(); strings.HasPrefix(uri, "spotify:track:") {
				uris = append(uris, uri)
			}
		}
	}
	return uris, nil
}

// contextURIFor names the context a list resolves through. Liked Songs is not
// a playlist and has no playlist URI, but it is addressable as the saved-track
// collection, which resolves like any other context.
func contextURIFor(playlistID string) string {
	if playlistID == savedTracksPlaylistID {
		return "spotify:collection:tracks"
	}
	return "spotify:playlist:" + playlistID
}

// trackMetadata resolves a batch of track URIs to full tracks in one request.
// Entries Spotify declines to describe are skipped rather than failing the
// batch, so one unavailable track cannot cost the whole page.
func (p *SpotifyProvider) trackMetadata(ctx context.Context, uris []string) ([]playlist.Track, error) {
	if len(uris) == 0 {
		return nil, nil
	}
	sess := p.session
	if sess == nil || sess.sess == nil {
		return nil, fmt.Errorf("spotify: track metadata: no session")
	}

	reqs := make([]*extmetapb.EntityRequest, 0, len(uris))
	for _, uri := range uris {
		reqs = append(reqs, &extmetapb.EntityRequest{
			EntityUri: uri,
			Query:     []*extmetapb.ExtensionQuery{{ExtensionKind: extmetapb.ExtensionKind_TRACK_V4}},
		})
	}
	res, err := sess.sess.Spclient().ExtendedMetadata(ctx, &extmetapb.BatchedEntityRequest{EntityRequest: reqs})
	if err != nil {
		return nil, fmt.Errorf("spotify: track metadata: %w", err)
	}

	// The response is not ordered like the request, so index it and rebuild the
	// page in the order the playlist actually holds.
	byURI := make(map[string]playlist.Track, len(uris))
	for _, ext := range res.GetExtendedMetadata() {
		for _, d := range ext.GetExtensionData() {
			var tr metadatapb.Track
			if err := d.GetExtensionData().UnmarshalTo(&tr); err != nil {
				continue
			}
			byURI[d.GetEntityUri()] = trackFromMetadata(d.GetEntityUri(), &tr)
		}
	}

	out := make([]playlist.Track, 0, len(uris))
	for _, uri := range uris {
		if t, ok := byURI[uri]; ok {
			out = append(out, t)
		}
	}
	return out, nil
}

// trackFromMetadata converts Spotify's internal track message into a playlist
// entry, populating the same fields trackFromItem does from the Web API's
// shape. Duration arrives in milliseconds, artists as a list, and the year on
// the album rather than as a release-date string.
// spotifyImageHost serves cover art by file id. The Web API's image URLs are
// this host plus the same id in hex, which is how metadata names an image.
const spotifyImageHost = "https://i.scdn.co/image/"

// metadataImageWidth is the width Spotify renders each metadata size class at,
// for images that do not state their own.
var metadataImageWidth = map[metadatapb.Image_Size]int{
	metadatapb.Image_SMALL:   64,
	metadatapb.Image_DEFAULT: 300,
	metadatapb.Image_LARGE:   640,
	metadatapb.Image_XLARGE:  1280,
}

// coverFromMetadata picks a track's album art from metadata the same way the
// Web API path picks it from images, so a track shows the same cover whichever
// path read it.
func coverFromMetadata(album *metadatapb.Album) string {
	sources := album.GetCoverGroup().GetImage()
	if len(sources) == 0 {
		sources = album.GetCover()
	}
	images := make([]spotifyImage, 0, len(sources))
	for _, img := range sources {
		if len(img.GetFileId()) == 0 {
			continue
		}
		width := int(img.GetWidth())
		if width == 0 {
			width = metadataImageWidth[img.GetSize()]
		}
		images = append(images, spotifyImage{
			URL:    spotifyImageHost + hex.EncodeToString(img.GetFileId()),
			Width:  width,
			Height: int(img.GetHeight()),
		})
	}
	return pickCoverImage(images)
}

func trackFromMetadata(uri string, tr *metadatapb.Track) playlist.Track {
	names := make([]string, 0, len(tr.GetArtist()))
	for _, a := range tr.GetArtist() {
		if n := a.GetName(); n != "" {
			names = append(names, n)
		}
	}
	return playlist.Track{
		Path:         uri,
		Title:        tr.GetName(),
		Artist:       strings.Join(names, ", "),
		Album:        tr.GetAlbum().GetName(),
		Year:         int(tr.GetAlbum().GetDate().GetYear()),
		AlbumArtURL:  coverFromMetadata(tr.GetAlbum()),
		DurationSecs: int(tr.GetDuration()) / 1000,
		TrackNumber:  int(tr.GetNumber()),
		Stream:       false,
		// Unplayable is deliberately left false, which matches what the Web API
		// path produces: it derives the flag from is_playable and restrictions,
		// and Spotify only populates those when a market is supplied, which
		// cliamp never does. The client protocol does carry real per-country
		// restrictions, so this could be made accurate here -- but only once
		// the account's country is known, and doing it on one path alone would
		// make the two disagree.
	}
}

// contextTracksPage serves one page of a playlist through the client protocol.
// The caller passes the resolve its read is working from, or nil to take a
// fresh one, which is then returned so the read can keep it for its remaining
// pages. The page size is not the Web API's: this path resolves the whole list
// up front and then batches metadata, so a page is one metadata request rather
// than one list request, and is sized to match.
func (p *SpotifyProvider) contextTracksPage(ctx context.Context, playlistID string, offset int, uris []string) (tracksPage, error) {
	if uris == nil {
		var err error
		if uris, err = p.contextTrackURIs(ctx, playlistID); err != nil {
			return tracksPage{}, err
		}
	}
	page := tracksPage{total: len(uris), pageSize: spotifyMetadataBatch, uris: uris}
	if offset >= len(uris) {
		return page, nil
	}
	end := min(offset+spotifyMetadataBatch, len(uris))
	tracks, err := p.trackMetadata(ctx, uris[offset:end])
	if err != nil {
		return tracksPage{}, err
	}
	page.tracks = tracks
	return page, nil
}

// TrackRadio builds the station Spotify generates from a track -- what its own
// clients call song radio. The station resolves to track URIs like any other
// context, so the metadata is filled in the same way.
//
// Implements provider.RadioStarter.
func (p *SpotifyProvider) TrackRadio(ctx context.Context, trackPath string) ([]playlist.Track, error) {
	if err := p.ensureSession(); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(trackPath, "spotify:track:") {
		return nil, fmt.Errorf("spotify: radio: %q is not a spotify track", trackPath)
	}
	sess := p.session
	if sess == nil || sess.sess == nil {
		return nil, fmt.Errorf("spotify: radio: no session")
	}

	uri := trackPath
	station, err := sess.sess.Spclient().ContextResolveAutoplay(ctx, &playerpb.AutoplayContextRequest{ContextUri: &uri})
	if err != nil {
		return nil, fmt.Errorf("spotify: radio for %q: %w", trackPath, err)
	}

	var uris []string
	for _, page := range station.GetPages() {
		for _, tr := range page.GetTracks() {
			if u := tr.GetUri(); strings.HasPrefix(u, "spotify:track:") {
				uris = append(uris, u)
			}
		}
	}
	if len(uris) == 0 {
		return nil, fmt.Errorf("spotify: radio for %q: station is empty", trackPath)
	}

	// Stations come back around fifty tracks long, which is one metadata batch.
	// Chunk anyway so a longer one cannot build a single oversized request.
	tracks := make([]playlist.Track, 0, len(uris))
	for start := 0; start < len(uris); start += spotifyMetadataBatch {
		end := min(start+spotifyMetadataBatch, len(uris))
		batch, err := p.trackMetadata(ctx, uris[start:end])
		if err != nil {
			return nil, err
		}
		tracks = append(tracks, batch...)
	}
	return tracks, nil
}
