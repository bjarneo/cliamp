package spotify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/playlist"
	librespot "github.com/devgianlu/go-librespot"
	extmetadatapb "github.com/devgianlu/go-librespot/proto/spotify/extendedmetadata"
	metadatapb "github.com/devgianlu/go-librespot/proto/spotify/metadata"
	"github.com/devgianlu/go-librespot/spclient"
)

func playlistURLID(raw string) (string, bool) {
	id, ok := strings.CutPrefix(raw, "spotify:playlist:")
	if !ok {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Host != "open.spotify.com" || u.User != nil {
			return "", false
		}
		id, ok = strings.CutPrefix(u.Path, "/playlist/")
	}
	if !ok || len(id) != 22 {
		return "", false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return "", false
		}
	}
	return id, true
}

func (p *SpotifyProvider) CanResolveURL(raw string) bool {
	_, ok := playlistURLID(raw)
	return ok
}

func (p *SpotifyProvider) ResolveURL(ctx context.Context, raw string) ([]playlist.Track, error) {
	id, ok := playlistURLID(raw)
	if !ok {
		return nil, fmt.Errorf("spotify: invalid playlist URL or URI")
	}
	if err := p.ensureSession(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	p.mu.Lock()
	sess := p.session
	p.mu.Unlock()
	if sess == nil {
		return nil, playlist.ErrNeedsAuth
	}
	sess.mu.RLock()
	defer sess.mu.RUnlock()
	if sess.sess == nil {
		return nil, fmt.Errorf("spotify: playback session is closed")
	}
	tracks, err := resolveContextTracks(ctx, sess.sess.Spclient(), "spotify:playlist:"+id)
	if err != nil {
		return nil, fmt.Errorf("spotify: resolve playlist context: %w", err)
	}
	return tracks, nil
}

func resolveContextTracks(ctx context.Context, sp *spclient.Spclient, uri string) ([]playlist.Track, error) {
	spotCtx, err := sp.ContextResolve(ctx, uri)
	if err != nil {
		return nil, err
	}
	if spotCtx.Loading || len(spotCtx.Pages) == 0 {
		return nil, fmt.Errorf("playlist context has no ready pages")
	}
	for _, page := range spotCtx.Pages {
		if page == nil {
			return nil, fmt.Errorf("playlist context contains a missing page")
		}
	}
	spotCtx.Uri = uri
	resolver, err := spclient.NewContextResolver(ctx, &librespot.NullLogger{}, sp, spotCtx)
	if err != nil {
		return nil, err
	}
	var tracks []playlist.Track
	for page := 0; ; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		items, err := resolver.Page(ctx, page)
		if errors.Is(err, io.EOF) {
			return tracks, nil
		}
		if err != nil {
			return nil, err
		}
		if page >= 100 {
			return nil, fmt.Errorf("playlist context exceeded 100 pages")
		}
		for _, item := range items {
			if item == nil {
				continue
			}
			path := item.Uri
			if path == "" && len(item.Gid) == 16 {
				path = librespot.SpotifyIdFromGid(librespot.SpotifyIdTypeTrack, item.Gid).Uri()
			}
			id, err := librespot.SpotifyIdFromUri(path)
			if err != nil || id.Type() != librespot.SpotifyIdTypeTrack {
				continue
			}
			title := item.Metadata["title"]
			if title == "" {
				title = path
			}
			duration, _ := strconv.Atoi(item.Metadata["duration"])
			track := playlist.Track{Path: id.Uri(), Title: title, Artist: item.Metadata["artist_name"], Album: item.Metadata["album_title"], DurationSecs: duration / 1000}
			if title == path {
				fillContextTrackMetadata(ctx, sp, *id, &track)
			}
			tracks = append(tracks, track)
		}
	}
}

func fillContextTrackMetadata(ctx context.Context, sp *spclient.Spclient, id librespot.SpotifyId, track *playlist.Track) {
	var meta metadatapb.Track
	if err := sp.ExtendedMetadataSimple(ctx, id, extmetadatapb.ExtensionKind_TRACK_V4, &meta); err == nil && meta.GetName() != "" {
		track.Title = meta.GetName()
		var artists []string
		for _, artist := range meta.GetArtist() {
			artists = append(artists, artist.GetName())
		}
		track.Artist = strings.Join(artists, ", ")
		track.Album = meta.GetAlbum().GetName()
		track.DurationSecs = int(meta.GetDuration()) / 1000
		track.TrackNumber = int(meta.GetNumber())
	}
}
