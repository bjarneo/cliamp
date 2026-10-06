package navidrome

import (
	"context"
	"fmt"
	"net/url"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

var _ provider.TrackFavoriter = (*NavidromeClient)(nil)

// CanFavoriteTrack reports whether track came from the Subsonic server.
// Implements provider.TrackFavoriter.
func (c *NavidromeClient) CanFavoriteTrack(track playlist.Track) bool {
	return track.Meta(provider.MetaNavidromeID) != ""
}

// SetTrackFavorite stars or unstars track on the server.
// Implements provider.TrackFavoriter.
func (c *NavidromeClient) SetTrackFavorite(ctx context.Context, track playlist.Track, favorite bool) error {
	id := track.Meta(provider.MetaNavidromeID)
	if id == "" {
		return fmt.Errorf("navidrome: %q has no song ID", track.Path)
	}
	endpoint := "star"
	if !favorite {
		endpoint = "unstar"
	}
	var result struct{}
	return c.subsonicGetContext(ctx, endpoint, url.Values{"id": {id}}, &result)
}
