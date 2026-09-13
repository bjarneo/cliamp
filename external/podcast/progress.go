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

	"github.com/bjarneo/cliamp/applog"
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
	// maxStoredEpisodes bounds the store. The least recently updated episodes
	// are dropped first, together with any aliases that point at them.
	maxStoredEpisodes = 2000
	// flushInterval throttles disk writes while an episode plays. Interim
	// positions arrive every 15 seconds and are not worth a write each time.
	flushInterval = 30 * time.Second
	// progressFormat is the on-disk layout version. Version 1 was a flat map
	// of episode keys; version 2 separates episodes from their aliases.
	progressFormat = 2
	// ambiguousAlias marks a title that two different episodes have claimed.
	// Such a title identifies neither, so it is never resolved.
	ambiguousAlias = "\x00ambiguous"
	// keySep joins the feed and GUID of an episode key. A GUID is only as
	// unique as its publisher made it, so the feed scopes it.
	keySep = "\x00"
)

// episodeState is one episode's stored listening state.
type episodeState struct {
	Feed        string `json:"feed,omitempty"`
	PositionSec int    `json:"position_sec"`
	DurationSec int    `json:"duration_sec,omitempty"`
	Played      bool   `json:"played,omitempty"`
	UpdatedUnix int64  `json:"updated_unix"`
}

// progressFile is the persisted form of the store.
type progressFile struct {
	Version  int                     `json:"version"`
	Episodes map[string]episodeState `json:"episodes"`
	// Aliases map a show-and-title key to an episode key, so an episode that
	// arrives without its feed and GUID can still be found.
	Aliases map[string]string `json:"aliases,omitempty"`
}

// progressStore keeps per-episode listening state, persisted as JSON.
//
// Each episode has one record under a key built from its feed and GUID, the
// two things a feed states about it. A restored track has lost both, and its
// URL is no help since podcast CDNs rewrite enclosure URLs per request, so
// the store also keeps aliases from show and title to the episode key. An
// alias is only trusted while it points at exactly one episode.
type progressStore struct {
	mu        sync.Mutex
	path      string
	episodes  map[string]episodeState
	aliases   map[string]string
	dirty     bool
	lastFlush time.Time
	// loadErr is set when an existing file could not be read. While it is
	// set the store never writes, so a file that failed to parse is left for
	// the listener rather than overwritten with an empty one.
	loadErr error
}

func newProgressStore() *progressStore {
	s := &progressStore{episodes: map[string]episodeState{}, aliases: map[string]string{}}
	dir, err := appdir.Dir()
	if err != nil {
		s.loadErr = fmt.Errorf("podcast progress directory: %w", err)
		applog.Warn("podcast progress: %v; positions will not be saved", s.loadErr)
		return s
	}
	s.path = filepath.Join(dir, "podcast_progress.json")
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s
	}
	if err == nil {
		err = s.load(data)
	}
	if err != nil {
		s.episodes = map[string]episodeState{}
		s.aliases = map[string]string{}
		s.loadErr = fmt.Errorf("load podcast progress from %s: %w", s.path, err)
		applog.Warn("podcast progress: %v; the file is left untouched and positions will not be saved", s.loadErr)
	}
	return s
}

// load parses either layout. A version 1 file is a flat map keyed by bare
// GUID; its entries carry the feed, so they migrate to feed-scoped keys. The
// title keys version 1 wrote for restored tracks name no episode and are
// dropped; the listener earns them back the next time the episode plays.
func (s *progressStore) load(data []byte) error {
	var file progressFile
	if err := json.Unmarshal(data, &file); err == nil && file.Version >= progressFormat {
		if file.Episodes == nil {
			file.Episodes = map[string]episodeState{}
		}
		if file.Aliases == nil {
			file.Aliases = map[string]string{}
		}
		s.episodes, s.aliases = file.Episodes, file.Aliases
		return nil
	}
	var flat map[string]episodeState
	if err := json.Unmarshal(data, &flat); err != nil {
		return err
	}
	for key, entry := range flat {
		if strings.HasPrefix(key, "title:") {
			continue
		}
		s.episodes[episodeKeyFor(entry.Feed, key)] = entry
	}
	s.dirty = true
	return nil
}

// episodeKeyFor builds the store key for a feed and GUID. A GUID without a
// feed is stored bare, which is the best the data allows.
func episodeKeyFor(feed, guid string) string {
	if feed == "" {
		return guid
	}
	return feed + keySep + guid
}

// episodeKey identifies an episode from its metadata, or returns "" for a
// track that carries none.
func episodeKey(track playlist.Track) string {
	feed := strings.TrimSpace(track.Meta(provider.MetaPodcastFeed))
	if feed == "" {
		return ""
	}
	guid := strings.TrimSpace(track.Meta(provider.MetaPodcastGUID))
	if guid == "" {
		guid = track.Path
	}
	return episodeKeyFor(feed, guid)
}

// titleKey identifies an episode by show and title, the two things that
// survive a saved playlist and a rewritten URL. It returns "" when either is
// missing, since a key needs both to be worth trusting.
func titleKey(track playlist.Track) string {
	show := strings.ToLower(strings.TrimSpace(track.Album))
	title := strings.ToLower(strings.TrimSpace(track.Title))
	if show == "" || title == "" {
		return ""
	}
	return "title:" + show + keySep + title
}

// resolveLocked returns the episode key for a track: its own when it carries
// metadata, otherwise the one its title aliases. The caller holds s.mu.
func (s *progressStore) resolveLocked(track playlist.Track) (string, bool) {
	if key := episodeKey(track); key != "" {
		return key, true
	}
	target, ok := s.aliases[titleKey(track)]
	if !ok || target == ambiguousAlias {
		return "", false
	}
	if _, ok := s.episodes[target]; !ok {
		return "", false
	}
	return target, true
}

// record stores a position for an episode, marking it played when position
// lands inside the last playedTail of a known duration.
func (s *progressStore) record(track playlist.Track, position, duration time.Duration) {
	if position < 0 {
		return
	}
	if duration <= 0 && track.DurationSecs > 0 {
		duration = time.Duration(track.DurationSecs) * time.Second
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.resolveLocked(track)
	if !ok {
		return
	}
	state := episodeState{
		Feed:        track.Meta(provider.MetaPodcastFeed),
		PositionSec: int(position / time.Second),
		DurationSec: int(duration / time.Second),
		UpdatedUnix: time.Now().Unix(),
	}
	if state.Feed == "" {
		state.Feed = s.episodes[key].Feed
	}
	// An episode shorter than the tail would otherwise count as played from
	// its first report, so a short one has to reach at least its midpoint.
	if duration > 0 && position >= max(duration-playedTail, duration/2) {
		state.Played = true
		state.PositionSec = 0
	}
	// Last write wins. Replaying a finished episode clears its played mark and
	// tracks the new position, which is what a listener starting it again
	// expects to see.
	s.episodes[key] = state
	s.aliasLocked(titleKey(track), key)
	s.prune()
	s.dirty = true
	s.flushLocked(false)
}

// aliasLocked points a title at an episode. A title that already names a
// different episode is marked ambiguous and never resolved again: two
// episodes with one title cannot be told apart, so neither may claim a
// restored track. The caller holds s.mu.
func (s *progressStore) aliasLocked(title, key string) {
	if title == "" {
		return
	}
	existing, ok := s.aliases[title]
	switch {
	case !ok:
		s.aliases[title] = key
	case existing == key, existing == ambiguousAlias:
	default:
		s.aliases[title] = ambiguousAlias
	}
}

// state returns the stored state for an episode.
func (s *progressStore) state(track playlist.Track) (provider.PlaybackState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.resolveLocked(track)
	if !ok {
		return provider.PlaybackState{}, false
	}
	entry, ok := s.episodes[key]
	if !ok {
		return provider.PlaybackState{}, false
	}
	return provider.PlaybackState{
		Played:   entry.Played,
		Position: time.Duration(entry.PositionSec) * time.Second,
	}, true
}

// knows reports whether the store can identify a track that carries no
// metadata: only an episode this listener has played before, whose title
// names exactly one episode, is recognized. A radio stream or a library
// track is never mistaken for one.
func (s *progressStore) knows(track playlist.Track) bool {
	if episodeKey(track) != "" {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.resolveLocked(track)
	return ok
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
	return len(s.episodes) > 0
}

// prune drops the least recently updated episodes once the store is full,
// and the aliases that pointed at them. The caller holds s.mu.
func (s *progressStore) prune() {
	if len(s.episodes) <= maxStoredEpisodes {
		return
	}
	keys := make([]string, 0, len(s.episodes))
	for k := range s.episodes {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int {
		return int(s.episodes[a].UpdatedUnix - s.episodes[b].UpdatedUnix)
	})
	for _, k := range keys[:len(s.episodes)-maxStoredEpisodes] {
		delete(s.episodes, k)
	}
	for title, target := range s.aliases {
		if target == ambiguousAlias {
			continue
		}
		if _, ok := s.episodes[target]; !ok {
			delete(s.aliases, title)
		}
	}
}

// flushLocked writes the store to disk. Interim writes are throttled to
// flushInterval; force bypasses that. Nothing is written while loadErr is
// set, so a file that could not be read is never replaced. The caller holds
// s.mu.
func (s *progressStore) flushLocked(force bool) error {
	if !s.dirty || s.path == "" || s.loadErr != nil {
		return nil
	}
	if !force && !s.lastFlush.IsZero() && time.Since(s.lastFlush) < flushInterval {
		return nil
	}
	data, err := json.Marshal(progressFile{Version: progressFormat, Episodes: s.episodes, Aliases: s.aliases})
	if err != nil {
		return fmt.Errorf("encoding podcast progress: %w", err)
	}
	if err := fileutil.WriteFileAtomic(s.path, append(data, '\n'), 0o600); err != nil {
		// The state stays dirty in memory, so the next flush retries it.
		return fmt.Errorf("writing podcast progress to %s: %w", s.path, err)
	}
	s.dirty = false
	s.lastFlush = time.Now()
	return nil
}

// flush writes any pending state to disk immediately.
func (s *progressStore) flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushLocked(true)
}

var (
	_ provider.ProgressReporter      = (*Provider)(nil)
	_ provider.TrackPosition         = (*Provider)(nil)
	_ provider.PlaybackStateReporter = (*Provider)(nil)
	_ provider.Closer                = (*Provider)(nil)
)

// CanReportPlayback reports whether track is an episode this provider tracks:
// one carrying podcast metadata, or one already in the store under its show and
// title, which is how an episode restored from a saved playlist is recognized.
func (p *Provider) CanReportPlayback(track playlist.Track) bool {
	return p.progress.knows(track)
}

// ReportNowPlaying records nothing. It fires at the start of a track, where
// the position is either zero or the offset TrackPosition just supplied, and
// storing that would overwrite the position it came from.
func (*Provider) ReportNowPlaying(playlist.Track, time.Duration, bool) error { return nil }

// recordable reports whether a position may be written for track. A track with
// no podcast metadata is only tracked once the store already knows it, so
// nothing new is recorded for a stream this provider cannot identify.
func (p *Provider) recordable(track playlist.Track) bool {
	return p.progress.knows(track)
}

// ReportProgress stores an interim listening position. The UI sends one every
// 15 seconds, so a position can be up to that stale after an abrupt exit.
func (p *Provider) ReportProgress(track playlist.Track, position time.Duration) error {
	if !p.recordable(track) {
		return nil
	}
	p.progress.record(track, position, 0)
	return nil
}

// ReportScrobble stores the final position for a track being left. The UI
// only calls it past half the duration, so shorter listens rely on the
// interim reports above.
func (p *Provider) ReportScrobble(track playlist.Track, elapsed, duration time.Duration, _ bool) error {
	if !p.recordable(track) {
		return nil
	}
	p.progress.record(track, elapsed, duration)
	return p.progress.flush()
}

// CanTrackPosition reports whether this provider has a position for track.
func (p *Provider) CanTrackPosition(track playlist.Track) bool {
	return p.progress.knows(track)
}

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

// Close writes any pending listening state to disk. provider.Closer returns
// nothing, so a failure at exit is reported on stderr, where the listener
// still sees it after the TUI is gone.
func (p *Provider) Close() {
	if err := p.progress.flush(); err != nil {
		applog.Warn("podcast progress: %v", err)
		fmt.Fprintf(os.Stderr, "podcast progress: %v\n", err)
	}
}
