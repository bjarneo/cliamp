package spotify

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/bjarneo/cliamp/playlist"
)

// AlbumTracks returns every track of a Spotify album, in disc and track order.
// Implements provider.AlbumTrackLoader, so an album placeholder from
// SearchTracks can be expanded into a playable list.
//
// /v1/albums/{id}/tracks returns simplified track objects that omit the album
// they belong to, so the album's own name, artist and release year are fetched
// once and filled in on every track for display.
func (p *SpotifyProvider) AlbumTracks(albumID string) ([]playlist.Track, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return p.AlbumTracksContext(ctx, albumID)
}

// AlbumTracksContext returns every track of a Spotify album with caller-controlled cancellation.
func (p *SpotifyProvider) AlbumTracksContext(ctx context.Context, albumID string) ([]playlist.Track, error) {
	if err := p.ensureSession(); err != nil {
		return nil, err
	}
	album, err := p.album(ctx, albumID)
	if err != nil {
		return nil, err
	}
	placeholder := albumFromItem(album)

	var tracks []playlist.Track
	for offset := 0; ; offset += spotifyTrackPageSize {
		page, err := p.albumTracksPage(ctx, albumID, offset)
		if err != nil {
			return nil, err
		}
		for _, item := range page {
			if item == nil || item.ID == "" {
				continue // skip null/unavailable results
			}
			track := trackFromItem(item)
			track.Album = placeholder.Album
			track.Year = placeholder.Year
			if track.Artist == "" {
				track.Artist = placeholder.Artist
			}
			tracks = append(tracks, track)
		}
		if len(page) < spotifyTrackPageSize {
			break
		}
	}
	return tracks, nil
}

// album fetches an album's own metadata.
func (p *SpotifyProvider) album(ctx context.Context, albumID string) (*spotifyAlbumItem, error) {
	resp, err := p.webAPI(ctx, "GET", "/v1/albums/"+url.PathEscape(albumID), nil)
	if err != nil {
		return nil, fmt.Errorf("spotify: album %s: %w", albumID, err)
	}
	var album spotifyAlbumItem
	if err := decodeBody(resp, &album); err != nil {
		return nil, fmt.Errorf("spotify: parse album %s: %w", albumID, err)
	}
	return &album, nil
}

// albumTracksPage fetches one page of an album's track list.
func (p *SpotifyProvider) albumTracksPage(ctx context.Context, albumID string, offset int) ([]*spotifyItem, error) {
	q := url.Values{
		"limit": {strconv.Itoa(spotifyTrackPageSize)},
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}

	resp, err := p.webAPI(ctx, "GET", "/v1/albums/"+url.PathEscape(albumID)+"/tracks", q)
	if err != nil {
		return nil, fmt.Errorf("spotify: album %s tracks: %w", albumID, err)
	}
	var page struct {
		Items []*spotifyItem `json:"items"`
	}
	if err := decodeBody(resp, &page); err != nil {
		return nil, fmt.Errorf("spotify: parse album %s tracks: %w", albumID, err)
	}
	return page.Items, nil
}
