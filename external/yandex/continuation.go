package yandex

import (
	"fmt"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

var _ provider.TrackExtender = (*Provider)(nil)

// CanExtendPlaylist reports whether the playlist supports radio continuation.
func (p *Provider) CanExtendPlaylist(id string) bool { return id == wavePlaylistID }

// ExtendTracks continues the existing radio session. The offset lets concurrent
// consumers and retries reuse a batch rather than advancing the session twice.
func (p *Provider) ExtendTracks(id string, offset int) ([]playlist.Track, error) {
	if !p.CanExtendPlaylist(id) {
		return nil, fmt.Errorf("yandex: playlist has no continuation")
	}
	p.waveLoadMu.Lock()
	defer p.waveLoadMu.Unlock()
	p.mu.Lock()
	w := p.wave
	if w == nil || offset < 0 || offset > len(w.tracks) {
		p.mu.Unlock()
		return nil, playlist.ErrListChanged
	}
	if offset < len(w.tracks) {
		tracks := append([]playlist.Track(nil), w.tracks[offset:]...)
		p.mu.Unlock()
		return tracks, nil
	}
	if w.exhausted {
		p.mu.Unlock()
		return nil, nil
	}
	sessionID := w.sessionID
	keys := append([]string(nil), w.keys...)
	p.mu.Unlock()
	batch, batchID, err := p.api.rotorWaveTracks(sessionID, nil, keys)
	if err != nil {
		return nil, fmt.Errorf("yandex: continue wave: %w", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.wave != w {
		return nil, playlist.ErrListChanged
	}
	p.appendWaveTracks(w, batch, batchID)
	w.exhausted = len(batch) == 0
	if !w.exhausted && len(w.tracks) == offset {
		// An empty successful result means exhaustion to TrackExtender callers.
		// Let their retry backoff handle batches containing no new playable tracks.
		return nil, fmt.Errorf("yandex: wave batch contains no new playable tracks")
	}
	return append([]playlist.Track(nil), w.tracks[offset:]...), nil
}

// appendWaveTracks runs under mu for a published session. Remember each track's
// original batch so feedback remains correct after later batches are appended.
func (p *Provider) appendWaveTracks(w *waveState, batch []track, batchID string) {
	for _, remote := range batch {
		converted := p.toPlaylistTracks([]track{remote})
		if len(converted) == 0 {
			continue
		}
		id := converted[0].Meta(provider.MetaYandexID)
		if _, exists := w.keysByID[id]; exists {
			continue
		}
		key := trackKeys([]track{remote})[0]
		w.keysByID[id] = key
		w.batchIDs[id] = batchID
		w.tracks = append(w.tracks, converted[0])
		w.keys = append(w.keys, key)
	}
}
