package yandex

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

func TestWaveExtendsBeyondInitialBatches(t *testing.T) {
	p, log := newTestProvider(t, [][]track{
		{{ID: "1", Available: true}}, {{ID: "2", Available: true}}, {{ID: "3", Available: true}},
		{{ID: "3", Available: true}, {ID: "4", Available: true}}, {{ID: "5", Available: true}},
	})
	initial, err := p.Tracks(wavePlaylistID)
	if err != nil || len(initial) != 3 {
		t.Fatalf("initial=%v err=%v", initial, err)
	}
	// Both callers ask for the same offset; only one request may advance Rotor.
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tracks, err := p.ExtendTracks(wavePlaylistID, 3)
			if err != nil || len(tracks) != 1 || tracks[0].Path != TrackURIPrefix+"4" {
				t.Errorf("tracks=%v err=%v", tracks, err)
			}
		}()
	}
	wg.Wait()
	if got := log.count("/rotor/session/session-1/tracks"); got != 3 {
		t.Fatalf("requests=%d, want 3", got)
	}
	tracks, err := p.ExtendTracks(wavePlaylistID, 4)
	if err != nil || len(tracks) != 1 || tracks[0].Path != TrackURIPrefix+"5" {
		t.Fatalf("tracks=%v err=%v", tracks, err)
	}
	if _, err := p.Tracks(wavePlaylistID); err != nil {
		t.Fatal(err)
	}
	if log.count("/rotor/session/new") != 1 {
		t.Fatal("continuation restarted the session")
	}
	// Feedback for an old track must retain its original batch ID.
	if err := p.ReportNowPlaying(initial[0], 0, true); err != nil {
		t.Fatal(err)
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	found := false
	for _, body := range log.bodies {
		if feedback, ok := body.(*rotorFeedback); ok {
			found = true
			if feedback.BatchID != "batch-0" {
				t.Errorf("batch=%q", feedback.BatchID)
			}
		}
	}
	if !found {
		t.Fatal("missing feedback")
	}
}

func TestWaveExtensionFailureAndRefresh(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		name := "http error"
		if refresh {
			name = "refresh during request"
		}
		t.Run(name, func(t *testing.T) {
			p := New("test-token")
			p.wave = &waveState{sessionID: "session", batchIDs: map[string]string{}, keysByID: map[string]string{}}
			entered, release := make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				<-release
				if !refresh {
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
					return
				}
				writeResult(w, map[string]any{"sequence": []any{map[string]any{"type": "track", "track": track{ID: "2", Available: true}}}, "batchId": "next"})
			}))
			defer server.Close()
			p.api.apiBase = server.URL
			done := make(chan error, 1)
			go func() { _, err := p.ExtendTracks(wavePlaylistID, 0); done <- err }()
			<-entered
			if refresh {
				p.Refresh()
			}
			close(release)
			err := <-done
			if err == nil {
				t.Fatal("expected error")
			}
			if refresh && !errors.Is(err, playlist.ErrListChanged) {
				t.Fatalf("err=%v", err)
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			if refresh && p.wave != nil {
				t.Fatal("stale response restored old session")
			}
			if !refresh && len(p.wave.tracks) != 0 {
				t.Fatal("failed request changed cache")
			}
		})
	}
}

func TestWaveExtensionEmptyAndInvalid(t *testing.T) {
	p, log := newTestProvider(t, [][]track{{{ID: "1", Available: true}}})
	if _, err := p.Tracks(wavePlaylistID); err != nil {
		t.Fatal(err)
	}
	if tracks, err := p.ExtendTracks(wavePlaylistID, 1); err != nil || len(tracks) != 0 {
		t.Fatalf("tracks=%v err=%v", tracks, err)
	}
	calls := log.count("/rotor/session/session-1/tracks")
	if tracks, err := p.ExtendTracks(wavePlaylistID, 1); err != nil || len(tracks) != 0 {
		t.Fatalf("repeat empty: %v %v", tracks, err)
	}
	if log.count("/rotor/session/session-1/tracks") != calls {
		t.Fatal("empty batch triggered another request")
	}
	for _, offset := range []int{-1, 2} {
		if _, err := p.ExtendTracks(wavePlaylistID, offset); !errors.Is(err, playlist.ErrListChanged) {
			t.Fatalf("offset=%d err=%v", offset, err)
		}
	}
	if p.CanExtendPlaylist(likedPlaylistID) {
		t.Fatal("liked tracks must stay finite")
	}
	if _, err := p.ExtendTracks(likedPlaylistID, 0); err == nil {
		t.Fatal("extended normal playlist")
	}
}

func TestWaveFeedbackUsesOriginalKeyForRealID(t *testing.T) {
	p, log := newTestProvider(t, [][]track{
		{{ID: "alias", RealID: "real", Albums: []album{{ID: 11}}, Available: true}},
		{{ID: "another-alias", RealID: "real", Available: true}},
		{{ID: "2", Available: true}},
	})
	tracks, err := p.Tracks(wavePlaylistID)
	if err != nil || len(tracks) != 2 {
		t.Fatalf("tracks=%v err=%v", tracks, err)
	}
	if err := p.ReportNowPlaying(tracks[0], 0, true); err != nil {
		t.Fatal(err)
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	for _, body := range log.bodies {
		if feedback, ok := body.(*rotorFeedback); ok {
			if feedback.Event.TrackID != "alias:11" || feedback.BatchID != "batch-0" {
				t.Fatalf("feedback=%+v", feedback)
			}
			return
		}
	}
	t.Fatal("missing alias track feedback")
}
