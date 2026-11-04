package model

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

type ratingTestProvider struct {
	name        string
	accept      bool
	calls       int
	ratings     []int
	hadDeadline bool
	err         error
}

func (p *ratingTestProvider) Name() string                                { return p.name }
func (p *ratingTestProvider) Playlists() ([]playlist.PlaylistInfo, error) { return nil, nil }
func (p *ratingTestProvider) Tracks(string) ([]playlist.Track, error)     { return nil, nil }
func (p *ratingTestProvider) CanRateTrack(track playlist.Track) bool {
	return p.accept && track.ProviderMeta[provider.MetaNavidromeID] != ""
}
func (p *ratingTestProvider) SetTrackRating(ctx context.Context, _ playlist.Track, rating int) error {
	p.calls++
	p.ratings = append(p.ratings, rating)
	_, p.hadDeadline = ctx.Deadline()
	return p.err
}

func ratingTestModel(p *ratingTestProvider, tracks ...playlist.Track) Model {
	pl := playlist.New()
	pl.Add(tracks...)
	return Model{
		playlist:  pl,
		provider:  p,
		providers: []provider.Entry{{Key: "navidrome", Name: p.name, Provider: p}},
		focus:     focusPlaylist,
	}
}

func ratingTrack(id string, rating int) playlist.Track {
	return playlist.Track{
		Path:         "https://music.example/rest/stream?id=" + id,
		Title:        "Track " + id,
		UserRating:   rating,
		ProviderMeta: map[string]string{provider.MetaNavidromeID: id},
	}
}

func TestRatingChordReturnsAsyncCommandWithoutBlockingSeekDigits(t *testing.T) {
	p := &ratingTestProvider{name: "Navidrome", accept: true}
	m := ratingTestModel(p, ratingTrack("one", 1))

	if cmd := m.handleMainKey(tea.KeyPressMsg{Text: "*"}); cmd != nil {
		t.Fatal("rating prefix returned a command")
	}
	cmd := m.handleMainKey(tea.KeyPressMsg{Text: "3"})
	if cmd == nil {
		t.Fatal("*3 returned no provider command")
	}
	if p.calls != 0 {
		t.Fatalf("provider calls before command = %d, want 0", p.calls)
	}
	msg, ok := cmd().(trackRatingSyncedMsg)
	if !ok {
		t.Fatalf("command message = %T, want trackRatingSyncedMsg", cmd())
	}
	if p.calls != 1 || len(p.ratings) != 1 || p.ratings[0] != 3 {
		t.Fatalf("provider calls = %d, ratings = %v", p.calls, p.ratings)
	}
	if !p.hadDeadline {
		t.Fatal("provider context has no deadline")
	}
	if msg.rating != 3 {
		t.Fatalf("message rating = %d, want 3", msg.rating)
	}

	plain := ratingTestModel(p, ratingTrack("two", 0))
	if cmd := plain.handleMainKey(tea.KeyPressMsg{Text: "3"}); cmd != nil {
		t.Fatal("plain seek digit returned a command")
	}
	if !plain.pendingSeekActive || plain.pendingSeekPct != 3 {
		t.Fatalf("plain digit seek state = active %v pct %d", plain.pendingSeekActive, plain.pendingSeekPct)
	}
}

func TestRatingChordClearsPendingSeek(t *testing.T) {
	p := &ratingTestProvider{name: "Navidrome", accept: true}
	m := ratingTestModel(p, ratingTrack("one", 1))

	m.handleMainKey(tea.KeyPressMsg{Text: "3"})
	if !m.pendingSeekActive {
		t.Fatal("plain digit did not prime seek")
	}
	m.handleMainKey(tea.KeyPressMsg{Text: "*"})
	if m.pendingSeekActive {
		t.Fatal("rating chord left the prior seek pending")
	}
}

type orderedRatingProvider struct {
	startFirst   chan struct{}
	releaseFirst chan struct{}
	mu           sync.Mutex
	ratings      []int
}

func (p *orderedRatingProvider) Name() string                                { return "Navidrome" }
func (p *orderedRatingProvider) Playlists() ([]playlist.PlaylistInfo, error) { return nil, nil }
func (p *orderedRatingProvider) Tracks(string) ([]playlist.Track, error)     { return nil, nil }
func (p *orderedRatingProvider) CanRateTrack(playlist.Track) bool            { return true }
func (p *orderedRatingProvider) SetTrackRating(_ context.Context, _ playlist.Track, rating int) error {
	if rating == 3 {
		close(p.startFirst)
		<-p.releaseFirst
	}
	p.mu.Lock()
	p.ratings = append(p.ratings, rating)
	p.mu.Unlock()
	return nil
}

func TestRatingWritesSerializeAndSkipSupersededQueuedValue(t *testing.T) {
	p := &orderedRatingProvider{startFirst: make(chan struct{}), releaseFirst: make(chan struct{})}
	track := ratingTrack("one", 1)
	pl := playlist.New()
	pl.Add(track)
	m := Model{
		playlist:  pl,
		provider:  p,
		providers: []provider.Entry{{Key: "navidrome", Name: p.Name(), Provider: p}},
		focus:     focusPlaylist,
	}

	first := m.rateSelectedTrack(3)
	done := make(chan tea.Msg, 3)
	go func() { done <- first() }()
	<-p.startFirst
	second := m.rateSelectedTrack(4)
	third := m.rateSelectedTrack(5)
	go func() { done <- second() }()
	go func() { done <- third() }()
	close(p.releaseFirst)
	for range 3 {
		<-done
	}

	p.mu.Lock()
	got := append([]int(nil), p.ratings...)
	p.mu.Unlock()
	if len(got) != 2 || got[0] != 3 || got[1] != 5 {
		t.Fatalf("provider ratings = %v, want [3 5]", got)
	}
}

func TestRatingWritesUseProviderSongIdentityAcrossStreamURLs(t *testing.T) {
	p := &ratingTestProvider{name: "Navidrome", accept: true}
	firstTrack := ratingTrack("one", 1)
	firstTrack.Path += "&salt=first"
	m := ratingTestModel(p, firstTrack)
	first := m.rateSelectedTrack(3)

	secondTrack := firstTrack
	secondTrack.Path = strings.Replace(firstTrack.Path, "salt=first", "salt=second", 1)
	m.playlist.SetTrack(0, secondTrack)
	second := m.rateSelectedTrack(4)

	second()
	first()
	if len(p.ratings) != 1 || p.ratings[0] != 4 {
		t.Fatalf("provider ratings = %v, want only latest [4]", p.ratings)
	}
}

func TestRatingProviderFailureDoesNotMutateLocalState(t *testing.T) {
	p := &ratingTestProvider{name: "Navidrome", accept: true, err: errors.New("server rejected it")}
	track := ratingTrack("one", 2)
	m := ratingTestModel(p, track)
	m.setPlaybackTrack(track)

	m.handleTrackRatingSynced(m.rateSelectedTrack(4)().(trackRatingSyncedMsg))
	got, _ := m.playlist.Track(0)
	if got.UserRating != 2 || m.playingTrack.UserRating != 2 {
		t.Fatalf("ratings after failure = row %d playing %d, want 2/2", got.UserRating, m.playingTrack.UserRating)
	}
	if !strings.Contains(m.status.text, "Navidrome did not save rating: server rejected it") {
		t.Fatalf("status = %q", m.status.text)
	}
}

func TestRatingSuccessUpdatesRowAndMatchingActivePlayback(t *testing.T) {
	p := &ratingTestProvider{name: "Navidrome", accept: true}
	track := ratingTrack("one", 1)
	m := ratingTestModel(p, track)
	m.setPlaybackTrack(track)

	m.handleTrackRatingSynced(m.rateSelectedTrack(5)().(trackRatingSyncedMsg))
	got, _ := m.playlist.Track(0)
	if got.UserRating != 5 || m.playingTrack.UserRating != 5 {
		t.Fatalf("ratings after success = row %d playing %d, want 5/5", got.UserRating, m.playingTrack.UserRating)
	}

	detached := ratingTestModel(p, track)
	detached.setPlaybackTrack(track)
	detached.playbackDetached = true
	detached.handleTrackRatingSynced(detached.rateSelectedTrack(4)().(trackRatingSyncedMsg))
	if detached.playingTrack.UserRating != 1 {
		t.Fatalf("detached playing rating = %d, want unchanged 1", detached.playingTrack.UserRating)
	}
}

func TestRatingRepliesUpdateTrackBySongIDAfterCursorMoveOrLaterRequest(t *testing.T) {
	p := &ratingTestProvider{name: "Navidrome", accept: true}
	first := ratingTrack("one", 1)
	second := ratingTrack("two", 2)
	m := ratingTestModel(p, first, second)

	firstReply := m.rateSelectedTrack(5)().(trackRatingSyncedMsg)
	m.plCursor = 1
	secondReply := m.rateSelectedTrack(4)().(trackRatingSyncedMsg)

	m.handleTrackRatingSynced(firstReply)
	m.handleTrackRatingSynced(secondReply)

	gotFirst, _ := m.playlist.Track(0)
	gotSecond, _ := m.playlist.Track(1)
	if gotFirst.UserRating != 5 || gotSecond.UserRating != 4 {
		t.Fatalf("ratings after replies = %d/%d, want 5/4", gotFirst.UserRating, gotSecond.UserRating)
	}
}

func TestRatingFailureWarnsAfterCursorMove(t *testing.T) {
	p := &ratingTestProvider{name: "Navidrome", accept: true, err: errors.New("late failure")}
	m := ratingTestModel(p, ratingTrack("one", 1), ratingTrack("two", 2))
	msg := m.rateSelectedTrack(5)().(trackRatingSyncedMsg)
	m.plCursor = 1

	m.handleTrackRatingSynced(msg)

	if !strings.Contains(m.status.text, "Navidrome did not save rating: late failure") {
		t.Fatalf("status = %q, want rating failure warning", m.status.text)
	}
}

func TestRatingUnsupportedTrackDoesNotReturnCommandOrMutate(t *testing.T) {
	p := &ratingTestProvider{name: "Navidrome", accept: false}
	m := ratingTestModel(p, ratingTrack("one", 2))
	if cmd := m.rateSelectedTrack(4); cmd != nil {
		t.Fatal("unsupported track returned a command")
	}
	got, _ := m.playlist.Track(0)
	if got.UserRating != 2 || p.calls != 0 {
		t.Fatalf("unsupported state = rating %d calls %d", got.UserRating, p.calls)
	}
	if !strings.Contains(m.status.text, "Rating is not available") {
		t.Fatalf("status = %q", m.status.text)
	}
}

func TestRatingChordIsRegisteredWithoutSingleKeyRun(t *testing.T) {
	for _, spec := range commandRegistry {
		if spec.KeyLabel == "* then 0-5" {
			if spec.runKey() != "" {
				t.Fatalf("rating chord run key = %q, want empty", spec.runKey())
			}
			if !spec.Keymap || spec.Mode&commandModeMain == 0 {
				t.Fatalf("rating chord registry = %+v", spec)
			}
			return
		}
	}
	t.Fatal("rating chord is absent from command registry")
}
