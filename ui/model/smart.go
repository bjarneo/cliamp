package model

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// smart.go implements Smart Shuffle: the Z-key mode toggle, the [Smart] header
// chip and ✚ row marker rendering, and the prefetch loop that keeps a cushion
// of provider recommendations mixed into the upcoming playback order.

// smartRetryBackoff pauses prefetch dispatches after a failed or empty
// recommendation batch so a persistently failing (or exhausted) provider is
// not re-queried on every tick. It also rate-limits the error toast to one
// showing per backoff window.
const smartRetryBackoff = 30 * time.Second

// smartRecommendsMsg carries one Smart Shuffle recommendation batch. gen,
// providerName, and the queue-identity pair (queueLen/tailPath) snapshot the
// dispatch-time state; a completion that no longer matches is dropped.
type smartRecommendsMsg struct {
	tracks       []playlist.Track
	err          error
	providerName string
	gen          uint64
	queueLen     int    // playlist length at dispatch time
	tailPath     string // last queue track's path at dispatch time
}

// smartRecommendsCmd fetches recommendations for a queue snapshot. seed must
// be a copy: the command runs off the UI loop while the playlist mutates.
func smartRecommendsCmd(ctx context.Context, rec provider.Recommender, providerName string, seed []playlist.Track, limit, queueLen int, tailPath string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		tracks, err := rec.RecommendTracks(ctx, seed, limit)
		return smartRecommendsMsg{tracks: tracks, err: err, providerName: providerName, gen: gen, queueLen: queueLen, tailPath: tailPath}
	}
}

// smartVolume is the per-cycle recommendation count: one sixth of the queue,
// clamped to [3, 30]. It is the limit passed to RecommendTracks, so it also
// caps what a cycle can inject.
func smartVolume(queueLen int) int {
	return max(3, min(queueLen/6, 30))
}

// smartTailPath returns the identity of the queue tail used to validate that
// a completed request still describes the current queue (the same mirror
// idea as trackPaging.lastPath).
func smartTailPath(tracks []playlist.Track) string {
	if len(tracks) == 0 {
		return ""
	}
	return tracks[len(tracks)-1].Path
}

// queueOwningProvider returns the provider owning the queue, resolved from
// the current track's URI scheme first (like the like-toggle), then from any
// queue row. Local-only queues resolve to nil.
func (m Model) queueOwningProvider() playlist.Provider {
	if cur, idx := m.playlist.Current(); idx >= 0 && cur.Path != "" {
		if prov := m.providerForTrack(cur.Path); prov != nil {
			return prov
		}
	}
	for _, t := range m.playlist.Tracks() {
		if prov := m.providerForTrack(t.Path); prov != nil {
			return prov
		}
	}
	return nil
}

// recommenderForQueue resolves the Recommender owning the queue, plus its
// display name. Nil when the queue has no owning provider or it does not
// implement provider.Recommender.
func (m Model) recommenderForQueue() (provider.Recommender, string) {
	prov := m.queueOwningProvider()
	if prov == nil {
		return nil, ""
	}
	if rec, ok := prov.(provider.Recommender); ok {
		return rec, prov.Name()
	}
	return nil, ""
}

// toggleSmartShuffle handles the Z key. Enabling requires the queue-owning
// provider to implement Recommender and implies shuffle (AddSmart no-ops
// without it); disabling needs nothing and drops the unplayed smart rows.
func (m *Model) toggleSmartShuffle() tea.Cmd {
	if m.playlist.Smart() {
		m.playlist.DisableSmart()
		if err := m.configSaver.Save("smart_shuffle", fmt.Sprintf("%v", m.playlist.Smart())); err != nil {
			m.status.Showf(statusTTLDefault, "Config save failed: %s", err)
		}
		m.status.Show("Smart Shuffle off", statusTTLDefault)
		m.player.ClearPreload()
		return m.preloadNext()
	}
	if rec, _ := m.recommenderForQueue(); rec == nil {
		m.status.Show("Smart Shuffle needs a supporting provider", statusTTLDefault)
		return nil
	}
	m.playlist.EnableSmart()
	if !m.playlist.Shuffled() {
		m.playlist.ToggleShuffle()
		if err := m.configSaver.Save("shuffle", fmt.Sprintf("%v", m.playlist.Shuffled())); err != nil {
			m.status.Showf(statusTTLDefault, "Config save failed: %s", err)
		}
	}
	if err := m.configSaver.Save("smart_shuffle", fmt.Sprintf("%v", m.playlist.Smart())); err != nil {
		m.status.Showf(statusTTLDefault, "Config save failed: %s", err)
	}
	m.status.Show("Smart Shuffle on", statusTTLDefault)
	m.player.ClearPreload()
	return tea.Batch(m.preloadNext(), m.smartMaybeFetch())
}

// smartMaybeFetch dispatches one recommendation request when Smart Shuffle
// should top up the queue.
//
// Prefetch condition: the unplayed order tail (Remaining — Smart and
// non-Smart rows alike) has fallen to max(2, queueLen/10), so recommendations
// only start mixing in near the end of the queue and then keep the tail from
// draining. Guards: mode + shuffle on, no request in flight, backoff
// expired, and the queue-owning provider still a Recommender.
func (m *Model) smartMaybeFetch() tea.Cmd {
	if !m.playlist.Smart() || !m.playlist.Shuffled() || m.smart.fetching {
		return nil
	}
	if !m.smart.retryAt.IsZero() && time.Now().Before(m.smart.retryAt) {
		return nil
	}
	rec, provName := m.recommenderForQueue()
	if rec == nil {
		return nil
	}
	queueLen := m.playlist.Len()
	if m.playlist.Remaining() > max(2, queueLen/10) {
		return nil
	}
	seed := append([]playlist.Track(nil), m.playlist.Tracks()...)
	m.smart.fetching = true
	return smartRecommendsCmd(m.newLikeContext(), rec, provName, seed, smartVolume(queueLen), queueLen, smartTailPath(seed), nextRequest(&m.requests.smart))
}

// handleSmartRecommends applies a completed recommendation batch (see
// update.go for dispatch). Stale generations keep the in-flight flag: a newer
// request is still outstanding. Failures are never fatal — smart mode stays
// on and retries after the backoff. Like the tracksAppendedMsg path, no
// preload refresh fires after injection; AddSmart's tail reshuffle matches
// what Add() already does mid-playback.
func (m *Model) handleSmartRecommends(msg smartRecommendsMsg) {
	if msg.gen != m.requests.smart {
		// Stale completion: a newer request is outstanding and owns the
		// in-flight flag.
		return
	}
	m.smart.fetching = false
	// Re-resolve the queue owner rather than checking the active provider:
	// the queue can outlive a provider switch, and dropping completions from
	// the still-owning provider would loop the tick dispatcher on a request
	// whose results are always discarded.
	if _, name := m.recommenderForQueue(); name != msg.providerName {
		return
	}
	if !m.playlist.Smart() {
		return // toggled off while the request was in flight
	}
	if msg.err != nil {
		m.smart.retryAt = time.Now().Add(smartRetryBackoff)
		m.status.Showf(statusTTLDefault, "Smart Shuffle failed: %s", msg.err)
		return
	}
	// Queue-identity guard (tracksAppendedMsg mirror pattern): apply the batch
	// only while the queue is still exactly the one it was seeded from.
	tracks := m.playlist.Tracks()
	if len(tracks) != msg.queueLen || smartTailPath(tracks) != msg.tailPath {
		return
	}
	if !m.playlist.Shuffled() {
		return // shuffle off mid-flight: AddSmart would silently no-op
	}
	fresh := make([]playlist.Track, 0, len(msg.tracks))
	for _, t := range msg.tracks {
		if t.Path == "" || m.smart.injected[t.Path] {
			continue
		}
		if m.smart.injected == nil {
			m.smart.injected = make(map[string]bool)
		}
		m.smart.injected[t.Path] = true
		fresh = append(fresh, t)
	}
	if len(fresh) == 0 {
		// Empty or all-repeats batch: back off quietly instead of re-asking
		// the same question on every tick.
		m.smart.retryAt = time.Now().Add(smartRetryBackoff)
		return
	}
	m.playlist.AddSmart(fresh...)
	m.adjustScroll()
}
