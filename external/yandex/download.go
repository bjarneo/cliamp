package yandex

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/internal/download"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

var _ provider.Downloader = (*Provider)(nil)

func (p *Provider) CanDownload(track playlist.Track) bool {
	id, ok := strings.CutPrefix(track.Path, TrackURIPrefix)
	return ok && id != "" && !strings.ContainsAny(id, "/?#")
}

// DownloadTrack uses the same cached URL resolver and quality selection as
// playback. Service-provided previews stay previews; no decryption is performed.
func (p *Provider) DownloadTrack(ctx context.Context, track playlist.Track, directory string) (string, error) {
	if !p.CanDownload(track) || track.Unplayable {
		return "", fmt.Errorf("yandex: track unavailable for download")
	}
	streamURL, err := p.resolveStreamURLContext(ctx, strings.TrimPrefix(track.Path, TrackURIPrefix), false)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		// Resolver errors may include self-authorizing URLs or API response bodies.
		return "", fmt.Errorf("yandex: could not resolve audio stream")
	}
	u, err := url.Parse(streamURL)
	if err != nil {
		return "", fmt.Errorf("yandex: invalid audio stream")
	}
	codec := strings.Split(strings.TrimPrefix(u.Path, "/get-"), "/")[0]
	name := track.Title
	if track.Artist != "" {
		name = track.Artist + " - " + name
	}
	return download.Save(ctx, &http.Client{Timeout: 30 * time.Minute}, streamURL, directory, name, "."+codec)
}
