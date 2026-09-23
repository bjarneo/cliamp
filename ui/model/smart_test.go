package model

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gopxl/beep/v2"

	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui"
)

// fakeRecommenderProvider is a queue-owning provider whose RecommendTracks
// serves a fixed pool and records the requests it saw.
type fakeRecommenderProvider struct {
	commandsTestProvider
	recs []playlist.Track
	err  error

	calls       int
	lastLimit   int
	lastSeedLen int
}

func (f *fakeRecommenderProvider) URISchemes() []string { return []string{"spotify"} }

func (f *fakeRecommenderProvider) NewStreamer(string) (beep.StreamSeekCloser, beep.Format, time.Duration, error) {
	return nil, beep.Format{}, 0, nil
}

func (f *fakeRecommenderProvider) RecommendTracks(_ context.Context, seed []playlist.Track, limit int) ([]playlist.Track, error) {
	f.calls++
	f.lastLimit = limit
	f.lastSeedLen = len(seed)
	if f.err != nil {
		return nil, f.err
	}
	n := min(limit, len(f.recs))
	return f.recs[:n], nil
}

// smartConfigSaver captures configSaver.Save calls.
type smartConfigSaver struct {
	pairs [][2]string
}

func (r *smartConfigSaver) Save(key, value string) error {
	r.pairs = append(r.pairs, [2]string{key, value})
	return nil
}

func (r *smartConfigSaver) has(key, value string) bool {
	for _, p := range r.pairs {
		if p[0] == key && p[1] == value {
			return true
		}
	}
	return false
}

func smartTracks(prefix string, n int) []playlist.Track {
	tracks := make([]playlist.Track, n)
	for i := range tracks {
		tracks[i] = playlist.Track{Path: "spotify:track:" + prefix + strconv.Itoa(i), Title: "R" + strconv.Itoa(i), Artist: "RA"}
	}
	return tracks
}

func newSmartTestModel(fake *fakeRecommenderProvider, saver *smartConfigSaver) Model {
	fake.commandsTestProvider.name = "Spotify"
	player := &playbackFakeEngine{}
	m := Model{
		player:      player,
		playlist:    playlist.New(),
		configSaver: saver,
		provider:    fake,
		providers:   []ProviderEntry{{Key: "spotify", Name: "Spotify", Provider: fake}},
		vis:         ui.NewVisualizer(float64(player.SampleRate())),
	}
	m.playlist.Add(spotifyTracks(6)...)
	return m
}

// drainNearTail advances playback until the unplayed order tail sits at or
// below the prefetch threshold, where smartMaybeFetch may dispatch.
func drainNearTail(p *playlist.Playlist) {
	for p.Remaining() > max(2, p.Len()/10) {
		if _, ok := p.Next(); !ok {
			break
		}
	}
}

func TestSmartToggleNeedsRecommender(t *testing.T) {
	fake := &fakeSpotifyLibProvider{name: "Spotify"} // implements CustomStreamer, not Recommender
	saver := &smartConfigSaver{}
	m := newSpotifyTestModel(fake)
	m.configSaver = saver
	m.playlist.Add(spotifyTracks(2)...)

	updated, cmd := m.Update(tea.KeyPressMsg{Text: "Z"})
	m = updated.(Model)
	if cmd != nil {
		t.Fatalf("Z returned %T; want refusal without command", cmd)
	}
	if m.playlist.Smart() {
		t.Fatal("smart mode enabled without a Recommender provider")
	}
	if m.playlist.Shuffled() {
		t.Fatal("shuffle must not flip when smart is refused")
	}
	if len(saver.pairs) != 0 {
		t.Fatalf("persist calls = %v; want none", saver.pairs)
	}
	if !strings.Contains(m.status.text, "Smart Shuffle") {
		t.Fatalf("status = %q; want supporting-provider toast", m.status.text)
	}
}

func TestSmartToggleOnOffWithRecommender(t *testing.T) {
	fake := &fakeRecommenderProvider{recs: smartTracks("rec", 10)}
	saver := &smartConfigSaver{}
	m := newSmartTestModel(fake, saver)

	// On: smart flag set, shuffle implied, both persisted, toast confirms.
	updated, _ := m.Update(tea.KeyPressMsg{Text: "Z"})
	m = updated.(Model)
	if !m.playlist.Smart() {
		t.Fatal("smart mode not enabled")
	}
	if !m.playlist.Shuffled() {
		t.Fatal("shuffle not auto-enabled with smart mode")
	}
	if !saver.has("smart_shuffle", "true") || !saver.has("shuffle", "true") {
		t.Fatalf("persist calls = %v; want smart_shuffle and shuffle saved true", saver.pairs)
	}
	if m.status.text != "Smart Shuffle on" {
		t.Fatalf("status = %q", m.status.text)
	}

	// Injected rows appear and are marked.
	m.playlist.AddSmart(smartTracks("a", 2)...)
	base := m.playlist.Len()

	// Off: flag cleared, unplayed smart rows dropped, state persisted.
	updated, _ = m.Update(tea.KeyPressMsg{Text: "Z"})
	m = updated.(Model)
	if m.playlist.Smart() {
		t.Fatal("smart mode not disabled")
	}
	if !saver.has("smart_shuffle", "false") {
		t.Fatalf("persist calls = %v; want smart_shuffle saved false", saver.pairs)
	}
	if m.playlist.Len() != base-2 {
		t.Fatalf("queue len = %d; want unplayed smart rows removed (%d)", m.playlist.Len(), base-2)
	}
	if m.status.text != "Smart Shuffle off (-2 queued)" {
		t.Fatalf("status = %q; want removed-count feedback", m.status.text)
	}
}

// TestSmartEnableTopsUpImmediately: enabling Smart Shuffle dispatches a
// recommendation request right away — without waiting for the queue tail to
// drain — and the landed batch announces itself with a count toast.
func TestSmartEnableTopsUpImmediately(t *testing.T) {
	fake := &fakeRecommenderProvider{recs: smartTracks("rec", 10)}
	m := newSmartTestModel(fake, &smartConfigSaver{})

	updated, cmd := m.Update(tea.KeyPressMsg{Text: "Z"})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("enable returned no command; want an immediate top-up fetch")
	}

	// Run the batch: preload + the immediate smart fetch.
	msg := runCmdUntil[smartRecommendsMsg](t, cmd)
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if got := m.playlist.Len(); got != 6+3 {
		t.Fatalf("queue len = %d; want immediate injection of smartVolume(6) = 3", got)
	}
	if !strings.Contains(m.status.text, "+3 queued") {
		t.Fatalf("status = %q; want injection-count feedback", m.status.text)
	}
}

// runCmdUntil unwraps command batches until it produces a msg of type T.
func runCmdUntil[T tea.Msg](t *testing.T, cmd tea.Cmd) T {
	t.Helper()
	var zero T
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		if typed, ok := msg.(T); ok {
			return typed
		}
	}
	t.Fatalf("no %T in command batch", zero)
	return zero
}

func TestSmartToggleOnKeepsExistingShuffle(t *testing.T) {
	fake := &fakeRecommenderProvider{}
	saver := &smartConfigSaver{}
	m := newSmartTestModel(fake, saver)
	m.playlist.ToggleShuffle() // already shuffled

	updated, _ := m.Update(tea.KeyPressMsg{Text: "Z"})
	m = updated.(Model)
	if !m.playlist.Shuffled() {
		t.Fatal("shuffle must stay on, not double-toggle")
	}
	if saver.has("shuffle", "true") || saver.has("shuffle", "false") {
		t.Fatalf("persist calls = %v; shuffle untouched must not be saved", saver.pairs)
	}
}

func TestSmartMaybeFetchConditions(t *testing.T) {
	freshModel := func() (Model, *fakeRecommenderProvider) {
		fake := &fakeRecommenderProvider{recs: smartTracks("rec", 30)}
		m := newSmartTestModel(fake, &smartConfigSaver{})
		return m, fake
	}

	// Smart off: never dispatches.
	m, _ := freshModel()
	if cmd := m.smartMaybeFetch(); cmd != nil {
		t.Fatal("dispatched with smart mode off")
	}

	// Shuffle off: never dispatches (AddSmart would no-op).
	m, _ = freshModel()
	m.playlist.EnableSmart()
	if cmd := m.smartMaybeFetch(); cmd != nil {
		t.Fatal("dispatched with shuffle off")
	}

	// Tail high: the unplayed order tail above max(2, len/10) blocks dispatch.
	m, fake := freshModel()
	m.playlist.EnableSmart()
	m.playlist.ToggleShuffle()
	m.playlist.AddSmart(smartTracks("pad", 5)...) // 11 tracks, threshold 2, tail 10
	if cmd := m.smartMaybeFetch(); cmd != nil {
		t.Fatal("dispatched with a long unplayed tail")
	}
	if fake.calls != 0 {
		t.Fatalf("provider calls = %d; want 0", fake.calls)
	}

	// Tail low: dispatches once, then the in-flight guard blocks re-dispatch.
	m, fake = freshModel()
	m.playlist.EnableSmart()
	m.playlist.ToggleShuffle()
	drainNearTail(m.playlist)
	cmd := m.smartMaybeFetch()
	if cmd == nil {
		t.Fatal("no dispatch with low cushion")
	}
	msg, ok := cmd().(smartRecommendsMsg)
	if !ok {
		t.Fatalf("dispatch produced %T", cmd())
	}
	if !m.smart.fetching {
		t.Fatal("in-flight flag not set on dispatch")
	}
	if cmd := m.smartMaybeFetch(); cmd != nil {
		t.Fatal("dispatched while a request is in flight")
	}

	// A valid completion resets the flag and injects; volume is the clamp.
	if msg.gen != m.requests.smart {
		t.Fatalf("msg gen = %d; want %d", msg.gen, m.requests.smart)
	}
	updated, _ := m.Update(msg)
	m = updated.(Model)
	if m.smart.fetching {
		t.Fatal("in-flight flag not reset on completion")
	}
	if got := m.playlist.Len(); got != 6+min(smartVolume(6), 30) {
		t.Fatalf("queue len = %d; want 6 + volume-injected rows", got)
	}
	if fake.lastLimit != smartVolume(6) {
		t.Fatalf("provider limit = %d; want clamp(len/6, 3, 30) = %d", fake.lastLimit, smartVolume(6))
	}
	if fake.lastSeedLen != 6 {
		t.Fatalf("seed len = %d; want queue snapshot of 6", fake.lastSeedLen)
	}
}

func TestSmartMaybeFetchBackoff(t *testing.T) {
	fake := &fakeRecommenderProvider{err: errors.New("boom")}
	m := newSmartTestModel(fake, &smartConfigSaver{})
	m.playlist.EnableSmart()
	m.playlist.ToggleShuffle()
	drainNearTail(m.playlist)

	cmd := m.smartMaybeFetch()
	if cmd == nil {
		t.Fatal("no dispatch")
	}
	msg := cmd().(smartRecommendsMsg)
	updated, _ := m.Update(msg)
	m = updated.(Model)

	if !m.playlist.Smart() {
		t.Fatal("smart mode must survive a failed batch")
	}
	if !strings.Contains(m.status.text, "Smart Shuffle failed") {
		t.Fatalf("status = %q; want failure toast", m.status.text)
	}
	if m.smart.retryAt.IsZero() || time.Now().After(m.smart.retryAt) {
		t.Fatalf("retryAt = %v; want a future backoff deadline", m.smart.retryAt)
	}
	if cmd := m.smartMaybeFetch(); cmd != nil {
		t.Fatal("dispatched during the failure backoff")
	}

	// After the backoff window passes, dispatching resumes.
	m.smart.retryAt = time.Now().Add(-time.Second)
	if cmd := m.smartMaybeFetch(); cmd == nil {
		t.Fatal("no dispatch after backoff expired")
	}
}

func TestSmartRecommendsMsgGuards(t *testing.T) {
	newDispatched := func(t *testing.T) (Model, smartRecommendsMsg, *fakeRecommenderProvider) {
		t.Helper()
		fake := &fakeRecommenderProvider{recs: smartTracks("rec", 10)}
		m := newSmartTestModel(fake, &smartConfigSaver{})
		m.playlist.EnableSmart()
		m.playlist.ToggleShuffle()
		drainNearTail(m.playlist)
		cmd := m.smartMaybeFetch()
		if cmd == nil {
			t.Fatal("no dispatch")
		}
		msg, ok := cmd().(smartRecommendsMsg)
		if !ok {
			t.Fatalf("dispatch produced %T", cmd())
		}
		return m, msg, fake
	}

	// Stale generation: dropped, and the in-flight flag stays with the
	// newer outstanding request.
	m, msg, _ := newDispatched(t)
	nextRequest(&m.requests.smart)
	updated, _ := m.Update(msg)
	m = updated.(Model)
	if m.playlist.Len() != 6 {
		t.Fatalf("stale gen injected; queue len = %d", m.playlist.Len())
	}
	if !m.smart.fetching {
		t.Fatal("stale completion must not release the newer request's in-flight flag")
	}

	// Queue identity mismatch (queue mutated mid-flight): dropped.
	m, msg, _ = newDispatched(t)
	m.playlist.Add(smartTracks("extra", 1)...)
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if m.playlist.Len() != 7 {
		t.Fatalf("tail-mismatched batch injected; queue len = %d", m.playlist.Len())
	}
	if m.smart.fetching {
		t.Fatal("current-gen completion must reset the in-flight flag")
	}

	// Valid: deduped against the session set, empties skipped, rows marked.
	m, msg, _ = newDispatched(t)
	dup := msg.tracks[0]
	msg.tracks = append(msg.tracks, dup, playlist.Track{Path: "", Title: "empty"})
	updated, _ = m.Update(msg)
	m = updated.(Model)
	injected := m.playlist.Len() - 6
	if injected != len(msg.tracks)-2 {
		t.Fatalf("injected %d rows; want session-deduped %d", injected, len(msg.tracks)-2)
	}
	smart := 0
	for _, tr := range m.playlist.Tracks() {
		if tr.Smart {
			smart++
		}
	}
	if smart != injected {
		t.Fatalf("smart-marked rows = %d; want %d", smart, injected)
	}

	// A second batch with the same tracks is fully deduped and backs off.
	tail := smartTailPath(m.playlist.Tracks())
	msg2 := smartRecommendsMsg{
		tracks:       msg.tracks,
		providerName: "Spotify",
		gen:          m.requests.smart,
		queueLen:     m.playlist.Len(),
		tailPath:     tail,
	}
	updated, _ = m.Update(msg2)
	m = updated.(Model)
	if m.playlist.Len() != 6+injected {
		t.Fatalf("all-repeats batch injected rows; queue len = %d", m.playlist.Len())
	}
	if m.smart.retryAt.IsZero() {
		t.Fatal("all-repeats batch must trigger the backoff")
	}
}

func TestSmartVolume(t *testing.T) {
	tests := []struct {
		queueLen int
		want     int
	}{
		{0, 3},
		{5, 3},
		{6, 3},
		{18, 3},
		{60, 10},
		{180, 30},
		{600, 30},
	}
	for _, tt := range tests {
		if got := smartVolume(tt.queueLen); got != tt.want {
			t.Errorf("smartVolume(%d) = %d; want %d", tt.queueLen, got, tt.want)
		}
	}
}

func TestSmartHeaderChipAndRowMarker(t *testing.T) {
	oldPanelWidth := ui.PanelWidth
	ui.PanelWidth = 80
	t.Cleanup(func() { ui.PanelWidth = oldPanelWidth })

	p := playlist.New()
	p.Add(
		playlist.Track{Title: "Plain"},
		playlist.Track{Title: "Rec", Smart: true},
		playlist.Track{Title: "Saved rec", Smart: true, Bookmark: true},
	)
	m := Model{player: &playbackFakeEngine{}, playlist: p, focus: focusPlaylist, plVisible: 3}

	header := ansi.Strip(m.renderPlaylistHeader())
	if strings.Contains(header, "Smart") {
		t.Fatalf("header = %q; [Smart] chip must be hidden while mode is off", header)
	}
	m.playlist.EnableSmart()
	if header := ansi.Strip(m.renderPlaylistHeader()); !strings.Contains(header, "[Smart 2]") {
		t.Fatalf("header = %q; want [Smart 2] chip (pending count) beside [Shuffle]", header)
	}

	rows := ansi.Strip(m.renderPlaylist())
	if !strings.Contains(rows, "✚") {
		t.Fatalf("rows = %q; want smart row marker", rows)
	}
	if strings.Count(rows, "✚") != 1 {
		t.Fatalf("rows = %q; want exactly one smart marker", rows)
	}
	if !strings.Contains(rows, "★") {
		t.Fatalf("rows = %q; bookmark must still render", rows)
	}
}

func TestSmartShuffleAppliedFromConfig(t *testing.T) {
	pl := playlist.New()
	pl.Add(spotifyTracks(3)...)

	cfg := config.Config{Repeat: "off", SmartShuffle: true}
	cfg.ApplyPlaylist(pl)
	if !pl.Smart() {
		t.Fatal("config.SmartShuffle must seed the playlist smart flag at startup")
	}
	if !pl.Shuffled() {
		t.Fatal("config.SmartShuffle must imply shuffle at startup")
	}

	pl = playlist.New()
	cfg = config.Config{Repeat: "off"}
	cfg.ApplyPlaylist(pl)
	if pl.Smart() || pl.Shuffled() {
		t.Fatal("default config must leave smart and shuffle off")
	}
}

// TestSmartFetchTickWiring drives one tick with playback active and smart mode
// on, asserting the tick path dispatches the recommendation request.
func TestSmartFetchTickWiring(t *testing.T) {
	fake := &fakeRecommenderProvider{recs: smartTracks("rec", 5)}
	m := newSmartTestModel(fake, &smartConfigSaver{})
	m.playlist.EnableSmart()
	m.playlist.ToggleShuffle()
	drainNearTail(m.playlist)
	engine := m.player.(*playbackFakeEngine)
	engine.playing = true

	updated, cmd := m.Update(tickMsg(time.Now()))
	m = updated.(Model)
	_ = m
	if cmd == nil {
		t.Fatal("tick returned no command")
	}
	// The batch contains the next tick; drain msgs until the smart request
	// shows up (or the batch runs dry).
	found := false
	var msgs []tea.Msg
	var collect func(tea.Cmd)
	collect = func(c tea.Cmd) {
		if c == nil {
			return
		}
		msg := c()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, sub := range batch {
				collect(sub)
			}
			return
		}
		msgs = append(msgs, msg)
	}
	collect(cmd)
	for _, msg := range msgs {
		if _, ok := msg.(smartRecommendsMsg); ok {
			found = true
		}
	}
	if !found {
		t.Fatalf("tick batch contained no smartRecommendsMsg; got %d msgs", len(msgs))
	}
	if fake.calls != 1 {
		t.Fatalf("provider calls = %d; want 1", fake.calls)
	}
}

// TestSmartAcceptsQueueOwnerAcrossProviderSwitch: the accept guard re-resolves
// the queue-owning provider instead of checking the active one, so a provider
// switch with a still-spotify queue keeps Smart Shuffle working (and the tick
// dispatcher from hammering an always-dropped request).
func TestSmartAcceptsQueueOwnerAcrossProviderSwitch(t *testing.T) {
	fake := &fakeRecommenderProvider{recs: smartTracks("rec", 3)}
	fake.commandsTestProvider.name = "Spotify"
	local := &commandsTestProvider{name: "Local"}
	player := &playbackFakeEngine{}
	m := Model{
		player:      player,
		playlist:    playlist.New(),
		configSaver: &smartConfigSaver{},
		provider:    local, // active provider does not own the queue
		providers: []ProviderEntry{
			{Key: "local", Name: "Local", Provider: local},
			{Key: "spotify", Name: "Spotify", Provider: fake},
		},
		vis: ui.NewVisualizer(float64(player.SampleRate())),
	}
	m.playlist.Add(spotifyTracks(6)...)
	m.playlist.EnableSmart()
	m.playlist.ToggleShuffle()
	drainNearTail(m.playlist)

	cmd := m.smartMaybeFetch()
	if cmd == nil {
		t.Fatal("no dispatch — queue owner should resolve via the providers list")
	}
	msg, ok := cmd().(smartRecommendsMsg)
	if !ok {
		t.Fatalf("dispatch produced %T", cmd())
	}
	updated, _ := m.Update(msg)
	m = updated.(Model)
	if got := m.playlist.Len(); got != 6+3 {
		t.Fatalf("queue len = %d; want recommendations injected despite the provider switch", got)
	}
}

var _ provider.Recommender = (*fakeRecommenderProvider)(nil)
