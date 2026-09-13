package podcast

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/fileutil"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

const (
	// playedTail is how close to the end an episode must get to count as
	// played. Podcast outros run long, so stopping inside the last minute
	// means the listener is done with it.
	playedTail = 60 * time.Second
	// minResumePosition is the shortest stored position worth resuming from.
	// Below it, starting over costs the listener nothing.
	minResumePosition = 15 * time.Second
	// resumeRewind backs up a resumed episode so the listener hears a few
	// seconds of context before where they stopped.
	resumeRewind = 5 * time.Second
	// maxStoredEpisodes bounds the store. The least recently updated entries
	// are dropped first.
	maxStoredEpisodes = 2000
	// flushInterval throttles disk writes while an episode plays. Interim
	// positions arrive every 15 seconds and are not worth a write each time.
	flushInterval = 30 * time.Second
)

// episodeState is one episode's stored listening state.
type episodeState struct {
	Feed        string `json:"feed,omitempty"`
	PositionSec int    `json:"position_sec"`
	DurationSec int    `json:"duration_sec,omitempty"`
	Played      bool   `json:"played,omitempty"`
	UpdatedUnix int64  `json:"updated_unix"`
}

// progressStore keeps per-episode listening state, persisted as a JSON object
// keyed by episode GUID.
//
// The RSS spec requires a globally unique GUID, and Feed falls back to the
// audio URL when a feed omits one, so the key stays stable across feed
// reloads and does not need the feed URL mixed in. The feed is stored in the
// value so entries remain attributable.
type progressStore struct {
	mu        sync.Mutex
	path      string
	entries   map[string]episodeState
	dirty     bool
	lastFlush time.Time
	loadErr   error
}

func newProgressStore() *progressStore {
	s := &progressStore{entries: make(map[string]episodeState)}
	dir, err := appdir.Dir()
	if err != nil {
		s.loadErr = fmt.Errorf("podcast progress directory: %w", err)
		return s
	}
	s.path = filepath.Join(dir, "podcast_progress.json")
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s
	}
	if err == nil {
		err = json.Unmarshal(data, &s.entries)
	}
	if err != nil {
		s.entries = make(map[string]episodeState)
		s.loadErr = fmt.Errorf("load podcast progress: %w", err)
	}
	return s
}

// episodeKey identifies an episode, or returns "" for a track that is not one.
func episodeKey(track playlist.Track) string {
	if strings.TrimSpace(track.Meta(provider.MetaPodcastFeed)) == "" {
		return ""
	}
	if guid := strings.TrimSpace(track.Meta(provider.MetaPodcastGUID)); guid != "" {
		return guid
	}
	return track.Path
}

// record stores a position for an episode, marking it played when position
// lands inside the last playedTail of a known duration.
func (s *progressStore) record(track playlist.Track, position, duration time.Duration) {
	key := episodeKey(track)
	if key == "" || position < 0 {
		return
	}
	if duration <= 0 && track.DurationSecs > 0 {
		duration = time.Duration(track.DurationSecs) * time.Second
	}
	state := episodeState{
		Feed:        track.Meta(provider.MetaPodcastFeed),
		PositionSec: int(position / time.Second),
		DurationSec: int(duration / time.Second),
		UpdatedUnix: time.Now().Unix(),
	}
	if duration > 0 && position >= duration-playedTail {
		state.Played = true
		state.PositionSec = 0
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Last write wins. Replaying a finished episode clears its played mark and
	// tracks the new position, which is what a listener starting it again
	// expects to see.
	s.entries[key] = state
	s.prune()
	s.dirty = true
	s.flushLocked(false)
}

// state returns the stored state for an episode.
func (s *progressStore) state(track playlist.Track) (provider.PlaybackState, bool) {
	key := episodeKey(track)
	if key == "" {
		return provider.PlaybackState{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	if !ok {
		return provider.PlaybackState{}, false
	}
	return provider.PlaybackState{
		Played:   entry.Played,
		Position: time.Duration(entry.PositionSec) * time.Second,
	}, true
}

// resumeAt returns where an episode should start, or 0 to start over.
func (s *progressStore) resumeAt(track playlist.Track) time.Duration {
	state, ok := s.state(track)
	if !ok || state.Played || state.Position < minResumePosition {
		return 0
	}
	if state.Position <= resumeRewind {
		return 0
	}
	return state.Position - resumeRewind
}

// has reports whether any state is stored.
func (s *progressStore) has() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries) > 0
}

// prune drops the least recently updated entries once the store is full.
// The caller holds s.mu.
func (s *progressStore) prune() {
	if len(s.entries) <= maxStoredEpisodes {
		return
	}
	keys := make([]string, 0, len(s.entries))
	for k := range s.entries {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int {
		return int(s.entries[a].UpdatedUnix - s.entries[b].UpdatedUnix)
	})
	for _, k := range keys[:len(s.entries)-maxStoredEpisodes] {
		delete(s.entries, k)
	}
}

// flushLocked writes the store to disk. Interim writes are throttled to
// flushInterval; force bypasses that. The caller holds s.mu.
func (s *progressStore) flushLocked(force bool) {
	if !s.dirty || s.path == "" {
		return
	}
	if !force && !s.lastFlush.IsZero() && time.Since(s.lastFlush) < flushInterval {
		return
	}
	data, err := json.Marshal(s.entries)
	if err != nil {
		return
	}
	// A failed write is not worth interrupting playback for; the next flush
	// retries with the same in-memory state.
	if err := fileutil.WriteFileAtomic(s.path, append(data, '\n'), 0o600); err != nil {
		return
	}
	s.dirty = false
	s.lastFlush = time.Now()
}

// flush writes any pending state to disk immediately.
func (s *progressStore) flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushLocked(true)
}

// Listening state is stored locally, so the podcast provider answers both the
// server-style reporting interfaces and the render-time state interface.
var (
	_ provider.ProgressReporter      = (*Provider)(nil)
	_ provider.TrackPosition         = (*Provider)(nil)
	_ provider.PlaybackStateReporter = (*Provider)(nil)
	_ provider.Closer                = (*Provider)(nil)
)

// CanReportPlayback reports whether track is a podcast episode.
func (*Provider) CanReportPlayback(track playlist.Track) bool { return episodeKey(track) != "" }

// ReportNowPlaying records nothing. It fires at the start of a track, where
// the position is either zero or the offset TrackPosition just supplied, and
// storing that would overwrite the position it came from.
func (*Provider) ReportNowPlaying(playlist.Track, time.Duration, bool) error { return nil }

// ReportProgress stores an interim listening position. The UI sends one every
// 15 seconds, so a position can be up to that stale after an abrupt exit.
func (p *Provider) ReportProgress(track playlist.Track, position time.Duration) error {
	p.progress.record(track, position, 0)
	return nil
}

// ReportScrobble stores the final position for a track being left. The UI
// only calls it past half the duration, so shorter listens rely on the
// interim reports above.
func (p *Provider) ReportScrobble(track playlist.Track, elapsed, duration time.Duration, _ bool) error {
	p.progress.record(track, elapsed, duration)
	p.progress.flush()
	return nil
}

// CanTrackPosition reports whether track is a podcast episode.
func (*Provider) CanTrackPosition(track playlist.Track) bool { return episodeKey(track) != "" }

// TrackPosition returns where an episode should resume, or 0 to start over.
func (p *Provider) TrackPosition(track playlist.Track) time.Duration {
	return p.progress.resumeAt(track)
}

// HasPlaybackState reports whether any episode state is stored.
func (p *Provider) HasPlaybackState() bool { return p.progress.has() }

// PlaybackState returns the stored listening state for an episode.
func (p *Provider) PlaybackState(track playlist.Track) (provider.PlaybackState, bool) {
	return p.progress.state(track)
}

// Close writes any pending listening state to disk.
func (p *Provider) Close() { p.progress.flush() }
