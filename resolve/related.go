package resolve

import (
	"context"
	"net/url"
	"strings"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// YouTubeRelater finds songs related to a YouTube or YouTube Music video from
// the video's Mix playlist, which yt-dlp can list. YouTube URLs belong to no
// provider, so this covers them for provider.RelaterFor.
type YouTubeRelater struct{}

var _ provider.Relater = YouTubeRelater{}

// CanRelate reports whether track is a single YouTube or YouTube Music video.
func (YouTubeRelater) CanRelate(track playlist.Track) bool {
	_, _, ok := youTubeMix(track.Path)
	return ok
}

// RelatedTracks returns about n songs from the seed video's Mix: it lists n+1
// entries because a Mix starts with the seed, and drops the seed. A Mix
// differs on every call.
func (YouTubeRelater) RelatedTracks(ctx context.Context, seed playlist.Track, n int) ([]playlist.Track, error) {
	mixURL, seedID, ok := youTubeMix(seed.Path)
	if !ok {
		return nil, nil
	}
	tracks, err := resolveYTDLRangeContext(ctx, mixURL, 0, n+1)
	if err != nil {
		return nil, err
	}
	out := tracks[:0]
	for _, t := range tracks {
		if id, _ := youTubeVideoID(t.Path); id == seedID {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

// youTubeMix returns the Mix playlist URL for a single-video YouTube or
// YouTube Music URL, and the video's ID.
func youTubeMix(path string) (mixURL, id string, ok bool) {
	music := playlist.IsYouTubeMusicURL(path)
	if !music && !playlist.IsYouTubeURL(path) {
		return "", "", false
	}
	id, ok = youTubeVideoID(path)
	if !ok {
		return "", "", false
	}
	if music {
		return "https://music.youtube.com/watch?v=" + id + "&list=RDAMVM" + id, id, true
	}
	return "https://www.youtube.com/watch?v=" + id + "&list=RD" + id, id, true
}

// youTubeVideoID extracts the video ID from watch, youtu.be, shorts, live and
// embed URLs. Playlist, channel and search URLs have none.
func youTubeVideoID(path string) (string, bool) {
	u, err := url.Parse(path)
	if err != nil {
		return "", false
	}
	var id string
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch {
	case strings.EqualFold(strings.TrimPrefix(u.Hostname(), "www."), "youtu.be"):
		id = segments[0]
	case u.Path == "/watch":
		id = u.Query().Get("v")
	case len(segments) == 2 && (segments[0] == "shorts" || segments[0] == "live" || segments[0] == "embed"):
		id = segments[1]
	}
	if len(id) != 11 || strings.Trim(id, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") != "" {
		return "", false
	}
	return id, true
}
