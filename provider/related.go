package provider

import (
	"context"

	"github.com/bjarneo/cliamp/playlist"
)

// RelaterFor returns the first relater that can find songs related to track.
func RelaterFor(track playlist.Track, relaters ...Relater) (Relater, bool) {
	for _, r := range relaters {
		if r.CanRelate(track) {
			return r, true
		}
	}
	return nil, false
}

// Related asks r for up to n songs related to seed. The seed and repeated
// songs are dropped, so fewer than n may come back. An n below 1 asks for
// nothing.
func Related(ctx context.Context, r Relater, seed playlist.Track, n int) ([]playlist.Track, error) {
	if n < 1 {
		return nil, nil
	}
	tracks, err := r.RelatedTracks(ctx, seed, n)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{seed.Path: true}
	out := make([]playlist.Track, 0, min(len(tracks), n))
	for _, t := range tracks {
		if seen[t.Path] {
			continue
		}
		seen[t.Path] = true
		out = append(out, t)
		if len(out) == n {
			break
		}
	}
	return out, nil
}
