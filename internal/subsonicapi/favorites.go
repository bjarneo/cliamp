package subsonicapi

import (
	"context"
	"fmt"
	"net/url"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

var _ provider.TrackFavoriter = (*Client)(nil)

// CanFavoriteTrack reports whether track came from the Subsonic server and
// the dialect stars songs. Implements provider.TrackFavoriter.
func (c *Client) CanFavoriteTrack(track playlist.Track) bool {
	return c.d.starSupported() && track.Meta(c.d.metaKey()) != ""
}

// SetTrackFavorite stars or unstars track on the server.
// Implements provider.TrackFavoriter.
func (c *Client) SetTrackFavorite(ctx context.Context, track playlist.Track, favorite bool) error {
	if !c.d.starSupported() {
		return fmt.Errorf("%s: favorites are not supported", c.prefix())
	}
	id := track.Meta(c.d.metaKey())
	if id == "" {
		return fmt.Errorf("%s: %q has no song ID", c.prefix(), track.Path)
	}
	endpoint := "star"
	if !favorite {
		endpoint = "unstar"
	}
	var result struct{}
	return c.subsonicGet(ctx, endpoint, url.Values{"id": {id}}, &result)
}
