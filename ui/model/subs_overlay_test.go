package model

import (
	"fmt"
	"github.com/bjarneo/cliamp/ui"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// subProv is a stub provider that lists subscriptions and serves their episodes.
type subProv struct {
	subs     []provider.SubscriptionInfo
	episodes map[string][]playlist.Track
	err      error
}

func (p *subProv) Name() string { return "Stub" }

func (p *subProv) Playlists() ([]playlist.PlaylistInfo, error) { return nil, nil }

func (p *subProv) Tracks(id string) ([]playlist.Track, error) { return p.AlbumTracks(id) }

func (p *subProv) Subscriptions() []provider.SubscriptionInfo { return p.subs }

func (p *subProv) AlbumTracks(id string) ([]playlist.Track, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.episodes[id], nil
}

func published(title, date string) playlist.Track {
	return playlist.Track{
		Path:         "https://cdn/" + title + ".mp3",
		Title:        title,
		Stream:       true,
		ProviderMeta: map[string]string{provider.MetaPodcastPublished: date},
	}
}

func TestLatestEpisode(t *testing.T) {
	tests := []struct {
		name   string
		tracks []playlist.Track
		want   string
		wantOK bool
	}{
		{"newest first", []playlist.Track{published("new", "2026-09-10"), published("old", "2026-01-02")}, "new", true},
		{"oldest first", []playlist.Track{published("old", "2026-01-02"), published("new", "2026-09-10")}, "new", true},
		{"no dates falls back to the first", []playlist.Track{{Title: "first"}, {Title: "second"}}, "first", true},
		{"single episode", []playlist.Track{published("only", "2026-03-03")}, "only", true},
		{"empty feed", nil, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := latestEpisode(tt.tracks)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got.Title != tt.want {
				t.Errorf("Title = %q, want %q", got.Title, tt.want)
			}
		})
	}
}

func stubSubsModel() *Model {
	prov := &subProv{
		subs: []provider.SubscriptionInfo{
			{ID: "feed-a", Name: "Wading Through AI", Author: "Casey Muratori"},
			{ID: "feed-b", Name: "Part Of The Problem", Author: "GaS Digital Network"},
			{ID: "feed-c", Name: "Dead Drop", Author: "John Kiriakou"},
		},
	}
	m := &Model{provider: prov, playlist: playlist.New()}
	return m
}

func TestOpenSubsOverlay(t *testing.T) {
	m := stubSubsModel()

	if !m.hasSubscriptions() {
		t.Fatal("hasSubscriptions() = false with three subscribed shows")
	}
	m.openSubsOverlay()

	if !m.subs.visible {
		t.Error("overlay did not open")
	}
	if len(m.subs.shows) != 3 {
		t.Errorf("shows = %d, want 3", len(m.subs.shows))
	}
}

func TestOpenSubsOverlayWithoutSubscriptions(t *testing.T) {
	m := &Model{provider: &subProv{}, playlist: playlist.New()}

	m.openSubsOverlay()

	if m.subs.visible {
		t.Error("overlay opened with no subscribed shows")
	}
}

func TestSubsFilter(t *testing.T) {
	tests := []struct {
		name   string
		filter string
		want   []string
	}{
		{"no filter shows all", "", []string{"Wading Through AI", "Part Of The Problem", "Dead Drop"}},
		{"matches the title", "wading", []string{"Wading Through AI"}},
		{"matches the author", "kiriakou", []string{"Dead Drop"}},
		{"fuzzy, not substring", "ptp", []string{"Part Of The Problem"}},
		{"no match", "zzzz", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := stubSubsModel()
			m.openSubsOverlay()
			m.subs.filter = tt.filter
			m.updateSubsFilter()

			var got []string
			for _, idx := range m.subsVisibleShows() {
				got = append(got, m.subs.shows[idx].Name)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("matches = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("match %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestSelectedSubscriptionFollowsFilter(t *testing.T) {
	m := stubSubsModel()
	m.openSubsOverlay()
	m.subs.filter = "kiriakou"
	m.updateSubsFilter()

	show, ok := m.selectedSubscription()
	if !ok {
		t.Fatal("selectedSubscription() returned nothing")
	}
	if show.ID != "feed-c" {
		t.Errorf("ID = %q, want feed-c", show.ID)
	}
}

func TestAddSubscriptionEpisodesModes(t *testing.T) {
	episodes := []playlist.Track{published("ep1", "2026-09-10"), published("ep2", "2026-09-01")}

	tests := []struct {
		name          string
		mode          subsLoadMode
		tracks        []playlist.Track
		wantPlaylist  int
		wantQueue     int
		wantQueueHead string
	}{
		{"append leaves the queue alone", subsLoadAppend, episodes, 2, 0, ""},
		{"queue adds every episode", subsLoadQueue, episodes, 2, 2, "ep1"},
		{"latest queues one", subsLoadLatest, episodes[:1], 1, 1, "ep1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := stubSubsModel()

			start := m.addSubscriptionEpisodes(tt.tracks, tt.mode, "Show")

			if start != 0 {
				t.Fatalf("start = %d, want 0", start)
			}
			if got := m.playlist.Len(); got != tt.wantPlaylist {
				t.Errorf("playlist length = %d, want %d", got, tt.wantPlaylist)
			}
			if got := m.playlist.QueueLen(); got != tt.wantQueue {
				t.Errorf("queue length = %d, want %d", got, tt.wantQueue)
			}
			if tt.wantQueueHead != "" {
				head := m.playlist.QueueWindow(0, 1)
				if len(head) != 1 || head[0].Title != tt.wantQueueHead {
					t.Errorf("queue head = %v, want %q", head, tt.wantQueueHead)
				}
			}
		})
	}
}

// Appending a second show must extend the playlist and leave the first show's
// queued episodes in place, which is the whole point of the overlay.
func TestAddSubscriptionEpisodesAccumulates(t *testing.T) {
	m := stubSubsModel()
	first := []playlist.Track{published("a1", "2026-09-10"), published("a2", "2026-09-01")}
	second := []playlist.Track{published("b1", "2026-09-09")}

	m.addSubscriptionEpisodes(first, subsLoadQueue, "First")
	start := m.addSubscriptionEpisodes(second, subsLoadLatest, "Second")

	if start != 2 {
		t.Errorf("second batch start = %d, want 2", start)
	}
	if got := m.playlist.Len(); got != 3 {
		t.Errorf("playlist length = %d, want 3", got)
	}
	if got := m.playlist.QueueLen(); got != 3 {
		t.Errorf("queue length = %d, want 3", got)
	}
	titles := make([]string, 0, 3)
	for _, tr := range m.playlist.QueueWindow(0, 3) {
		titles = append(titles, tr.Title)
	}
	want := []string{"a1", "a2", "b1"}
	for i := range want {
		if titles[i] != want[i] {
			t.Errorf("queue[%d] = %q, want %q", i, titles[i], want[i])
		}
	}
}

func TestAddSubscriptionEpisodesEmptyFeed(t *testing.T) {
	m := stubSubsModel()

	if got := m.addSubscriptionEpisodes(nil, subsLoadAppend, "Empty Show"); got != -1 {
		t.Errorf("start = %d, want -1", got)
	}
	if m.subs.err == "" {
		t.Error("no error surfaced for an empty feed")
	}
	if m.playlist.Len() != 0 {
		t.Errorf("playlist length = %d, want 0", m.playlist.Len())
	}
}

func TestLoadLatestFromAllSubscriptionsKeepsSubscriptionOrder(t *testing.T) {
	prov := &subProv{
		subs: []provider.SubscriptionInfo{
			{ID: "feed-a", Name: "A"},
			{ID: "feed-b", Name: "B"},
			{ID: "feed-c", Name: "C"},
		},
		episodes: map[string][]playlist.Track{
			"feed-a": {published("a-old", "2026-01-01"), published("a-new", "2026-09-10")},
			"feed-b": {published("b-new", "2026-09-09"), published("b-old", "2026-02-02")},
			// feed-c returns nothing, so it must be reported as failed.
		},
	}
	m := &Model{provider: prov, playlist: playlist.New()}
	m.openSubsOverlay()

	cmd := m.loadLatestFromAllSubscriptions()
	if cmd == nil {
		t.Fatal("loadLatestFromAllSubscriptions returned no command")
	}
	msg, ok := cmd().(subsLatestAllMsg)
	if !ok {
		t.Fatal("expected a subsLatestAllMsg")
	}

	var titles []string
	for _, tr := range msg.tracks {
		titles = append(titles, tr.Title)
	}
	want := []string{"a-new", "b-new"}
	if len(titles) != len(want) {
		t.Fatalf("tracks = %v, want %v", titles, want)
	}
	for i := range want {
		if titles[i] != want[i] {
			t.Errorf("track %d = %q, want %q", i, titles[i], want[i])
		}
	}
	if len(msg.failed) != 1 || msg.failed[0] != "C" {
		t.Errorf("failed = %v, want [C]", msg.failed)
	}
}

func TestHandleSubsLatestAllAppendsWithoutQueueing(t *testing.T) {
	m := stubSubsModel()
	m.subs.loading = true

	m.addLatestSweep(subsLatestAllMsg{tracks: []playlist.Track{published("a", "2026-09-10"), published("b", "2026-09-09")}})

	if m.subs.loading {
		t.Error("loading flag still set after the sweep returned")
	}
	if got := m.playlist.Len(); got != 2 {
		t.Errorf("playlist length = %d, want 2", got)
	}
	if got := m.playlist.QueueLen(); got != 0 {
		t.Errorf("queue length = %d, want 0; the sweep builds a list, it does not queue", got)
	}
}

// sectionedSubProv adds section semantics, so only real show rows are
// actionable in the provider list.
type sectionedSubProv struct {
	subProv
	favoritable map[string]bool
}

func (p *sectionedSubProv) IDPrefix(string) string { return "" }

func (p *sectionedSubProv) IsFavoritableID(id string) bool { return p.favoritable[id] }

func TestSelectedProviderShow(t *testing.T) {
	prov := &sectionedSubProv{
		subProv:     subProv{episodes: map[string][]playlist.Track{"f:feed-a": {published("a", "2026-09-10")}}},
		favoritable: map[string]bool{"f:feed-a": true},
	}
	lists := []playlist.PlaylistInfo{
		{ID: "browse:categories", Name: "Browse Categories"},
		{ID: "f:feed-a", Name: "Part Of The Problem"},
	}

	tests := []struct {
		name    string
		cursor  int
		loading bool
		wantOK  bool
		wantID  string
	}{
		{"a subscribed show", 1, false, true, "f:feed-a"},
		{"a section entry", 0, false, false, ""},
		{"cursor out of range", 5, false, false, ""},
		{"while the provider is loading", 1, true, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{provider: prov, playlist: playlist.New(), providerLists: lists, provCursor: tt.cursor, provLoading: tt.loading}
			id, _, ok := m.selectedProviderShow()
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if id != tt.wantID {
				t.Errorf("id = %q, want %q", id, tt.wantID)
			}
		})
	}
}

func TestLoadLatestFromProviderListQueuesNewest(t *testing.T) {
	prov := &sectionedSubProv{
		subProv: subProv{episodes: map[string][]playlist.Track{
			"f:feed-a": {published("old", "2026-01-01"), published("newest", "2026-09-10")},
		}},
		favoritable: map[string]bool{"f:feed-a": true},
	}
	m := &Model{
		provider:      prov,
		playlist:      playlist.New(),
		providerLists: []playlist.PlaylistInfo{{ID: "f:feed-a", Name: "Show"}},
	}

	cmd := m.loadLatestFromProviderList()
	if cmd == nil {
		t.Fatal("no command returned for a highlighted show")
	}
	msg, ok := cmd().(subsEpisodesMsg)
	if !ok {
		t.Fatal("expected a subsEpisodesMsg")
	}
	if msg.mode != subsLoadLatest {
		t.Errorf("mode = %v, want subsLoadLatest", msg.mode)
	}
	if len(msg.tracks) != 1 || msg.tracks[0].Title != "newest" {
		t.Fatalf("tracks = %v, want just the newest episode", msg.tracks)
	}

	m.addSubscriptionEpisodes(msg.tracks, msg.mode, msg.name)

	if got := m.playlist.Len(); got != 1 {
		t.Errorf("playlist length = %d, want 1", got)
	}
	if got := m.playlist.QueueLen(); got != 1 {
		t.Errorf("queue length = %d, want 1", got)
	}
}

func TestLoadLatestFromProviderListIgnoresSectionRows(t *testing.T) {
	prov := &sectionedSubProv{favoritable: map[string]bool{}}
	m := &Model{
		provider:      prov,
		playlist:      playlist.New(),
		providerLists: []playlist.PlaylistInfo{{ID: "browse:categories", Name: "Browse Categories"}},
	}

	if cmd := m.loadLatestFromProviderList(); cmd != nil {
		t.Error("a section row produced a load command")
	}
}

func TestAppendShowFromProviderListAppendsEverything(t *testing.T) {
	prov := &sectionedSubProv{
		subProv: subProv{episodes: map[string][]playlist.Track{
			"f:feed-a": {published("one", "2026-09-10"), published("two", "2026-09-01")},
		}},
		favoritable: map[string]bool{"f:feed-a": true},
	}
	m := &Model{
		provider:      prov,
		playlist:      playlist.New(),
		providerLists: []playlist.PlaylistInfo{{ID: "f:feed-a", Name: "Show"}},
	}
	m.playlist.Add(playlist.Track{Path: "/already-here.mp3"})
	m.playlist.Queue(0)

	cmd := m.appendShowFromProviderList()
	if cmd == nil {
		t.Fatal("no command returned for a highlighted show")
	}
	msg := cmd().(subsEpisodesMsg)
	if msg.mode != subsLoadAppend {
		t.Errorf("mode = %v, want subsLoadAppend", msg.mode)
	}
	m.addSubscriptionEpisodes(msg.tracks, msg.mode, msg.name)

	if got := m.playlist.Len(); got != 3 {
		t.Errorf("playlist length = %d, want 3; the existing track must survive", got)
	}
	if got := m.playlist.QueueLen(); got != 1 {
		t.Errorf("queue length = %d, want the existing queue entry to survive", got)
	}
	first, _ := m.playlist.Track(0)
	if first.Path != "/already-here.mp3" {
		t.Errorf("first track = %q, want the pre-existing one", first.Path)
	}
}

// The overlay's episodes load through the provider that owns the subscription
// list, not the active provider, which may be a different service whose
// AlbumTracks would not know a podcast feed URL.
func TestOverlayLoadsThroughTheSubscriptionProvider(t *testing.T) {
	podcasts := &subProv{
		subs:     []provider.SubscriptionInfo{{ID: "feed-a", Name: "Show"}},
		episodes: map[string][]playlist.Track{"feed-a": {published("ep", "2026-09-10")}},
	}
	other := &albumOnlyProv{} // active, loads albums but keeps no subscriptions
	m := &Model{
		provider:  other,
		providers: []ProviderEntry{{Name: "Other", Provider: other}, {Name: "Podcasts", Provider: podcasts}},
		playlist:  playlist.New(),
	}

	if !m.openSubsOverlay() {
		t.Fatal("overlay did not open")
	}
	cmd := m.loadSubscription(subsLoadAppend)
	if cmd == nil {
		t.Fatal("no load command")
	}
	msg := cmd().(subsEpisodesMsg)
	if msg.err != nil || len(msg.tracks) != 1 {
		t.Errorf("tracks = %v, err = %v; want the podcast provider's one episode", msg.tracks, msg.err)
	}
}

func TestOpenSubsOverlayReportsWhetherItOpened(t *testing.T) {
	if m := stubSubsModel(); !m.openSubsOverlay() {
		t.Error("openSubsOverlay() = false with subscriptions present")
	}
	if m := (&Model{provider: &subProv{}, playlist: playlist.New()}); m.openSubsOverlay() {
		t.Error("openSubsOverlay() = true with nothing to show")
	}
}

// With one row of budget and a notice to show, the notice is all that fits.
func TestRenderSubsBodyOneRowWithNotice(t *testing.T) {
	old := ui.PanelWidth
	ui.PanelWidth = 60
	t.Cleanup(func() { ui.PanelWidth = old })
	m := stubSubsModel()
	m.openSubsOverlay()
	m.plVisible = 1
	m.subs.err = "feed failed"

	body := m.renderSubsBody()

	if got := strings.Count(body, "\n") + 1; got != 1 {
		t.Errorf("body rows = %d, want 1\n%q", got, body)
	}
	if !strings.Contains(body, "feed failed") {
		t.Errorf("body = %q, want the notice", body)
	}
}

// albumOnlyProv is an active provider that can load albums but knows nothing
// about podcast feeds, the shape that made the overlay pick the wrong loader.
type albumOnlyProv struct{ plainProv }

func (p *albumOnlyProv) AlbumTracks(id string) ([]playlist.Track, error) {
	return nil, fmt.Errorf("unknown album %q", id)
}
