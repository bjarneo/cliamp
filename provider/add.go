package provider

import (
	"context"
	"errors"

	"github.com/bjarneo/cliamp/playlist"
)

var errAddUnsupported = errors.New("provider does not support adding tracks")

// AddTracks adds tracks to the saved playlist playlistID of p. It makes one
// batch write when p is a PlaylistBatchWriter. Otherwise it adds the tracks
// one at a time through PlaylistWriter and stops at the first error. That
// path reports no skipped duplicates, because PlaylistWriter does not.
func AddTracks(ctx context.Context, p playlist.Provider, playlistID string, tracks []playlist.Track) (added, skipped int, err error) {
	if bw, ok := p.(PlaylistBatchWriter); ok {
		return bw.AddTracksToPlaylist(ctx, playlistID, tracks)
	}
	w, ok := p.(PlaylistWriter)
	if !ok {
		return 0, 0, errAddUnsupported
	}
	for _, track := range tracks {
		if err := w.AddTrackToPlaylist(ctx, playlistID, track); err != nil {
			return added, 0, err
		}
		added++
	}
	return added, 0, nil
}
