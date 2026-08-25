package tidal

import "sync"

// urlRegistry maps resolved track IDs to their latest signed CDN URL so the
// player's buffered-URL matcher recognizes Tidal streams. Re-resolving a
// track (every play, since resolution happens at play time) replaces its
// previous entry, so the registry stays bounded by the tracks played this
// session instead of growing with every resolution.
type urlRegistry struct {
	mu      sync.Mutex
	byTrack map[string]string
	urls    map[string]struct{}
}

var streamURLs = urlRegistry{
	byTrack: make(map[string]string),
	urls:    make(map[string]struct{}),
}

// register records u as trackID's current stream URL, evicting the track's
// previous URL.
func (r *urlRegistry) register(trackID, u string) {
	if u == "" {
		return
	}
	r.mu.Lock()
	if old, ok := r.byTrack[trackID]; ok {
		delete(r.urls, old)
	}
	r.byTrack[trackID] = u
	r.urls[u] = struct{}{}
	r.mu.Unlock()
}

// IsStreamURL reports whether u is a live Tidal stream URL previously
// resolved by the provider. It is registered with the player's buffered-URL
// matcher in main.go.
func IsStreamURL(u string) bool {
	streamURLs.mu.Lock()
	defer streamURLs.mu.Unlock()
	_, ok := streamURLs.urls[u]
	return ok
}
