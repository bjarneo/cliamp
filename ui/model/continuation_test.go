package model

import (
	"errors"
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
)

type extendingProvider struct {
	commandsTestProvider
	calls   int
	offsets []int
	batch   []playlist.Track
	err     error
}

func (p *extendingProvider) CanExtendPlaylist(id string) bool { return id == "wave" }
func (p *extendingProvider) ExtendTracks(_ string, offset int) ([]playlist.Track, error) {
	p.calls++
	p.offsets = append(p.offsets, offset)
	return p.batch, p.err
}

func newContinuationModel(t *testing.T) (Model, *extendingProvider, *playbackFakeEngine) {
	t.Helper()
	p := &extendingProvider{commandsTestProvider: commandsTestProvider{name: "Wave"}, batch: pageOf("c.mp3", "d.mp3")}
	engine := &playbackFakeEngine{}
	m := Model{provider: p, player: engine, playlist: playlist.New(), focus: focusPlaylist}
	m.requests.tracks = 1
	next, _ := m.Update(tracksLoadedMsg{tracks: pageOf("a.mp3", "b.mp3"), playlistID: "wave", providerName: p.Name(), gen: 1})
	return next.(Model), p, engine
}

// Extract a continuation from a key's batch without running unrelated commands.
func extendedMessage(t *testing.T, cmd tea.Cmd) tracksExtendedMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("missing continuation command")
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, child := range batch {
			if child != nil {
				if result, ok := child().(tracksExtendedMsg); ok {
					return result
				}
			}
		}
		t.Fatal("no continuation in batch")
	}
	if result, ok := msg.(tracksExtendedMsg); ok {
		return result
	}
	t.Fatalf("unexpected message %T", msg)
	return tracksExtendedMsg{}
}

func TestCursorLoadsOneBatchAtEnd(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyDown}, {Code: tea.KeyEnd}, {Code: tea.KeyPgDown}} {
		t.Run(key.String(), func(t *testing.T) {
			m, p, engine := newContinuationModel(t)
			next, cmd := m.Update(key)
			m = next.(Model)
			if m.plCursor != 1 {
				t.Fatalf("cursor=%d", m.plCursor)
			}
			if duplicate := m.extendTracks(false); duplicate != nil {
				t.Fatal("parallel continuation")
			}
			msg := extendedMessage(t, cmd)
			next, _ = m.Update(msg)
			m = next.(Model)
			if p.calls != 1 || p.offsets[0] != 2 || m.playlist.Len() != 4 || m.plCursor != 1 {
				t.Fatal("batch did not append in place")
			}
			if engine.stopCalls > 1 || len(engine.playCalls) != 0 {
				t.Fatal("browsing changed playback")
			}
		})
	}
}

func TestLastTrackWaitsThenContinues(t *testing.T) {
	m, p, engine := newContinuationModel(t)
	m.playlist.SetIndex(1)
	engine.playing = true
	// Prefetch has started, then the last loaded track finishes.
	cmd := m.extendTracks(false)
	engine.Stop()
	if duplicate := m.nextTrack(); duplicate != nil {
		t.Fatal("started duplicate fetch")
	}
	if !m.continuation.waiting {
		t.Fatal("did not wait at tail")
	}
	next, _ := m.Update(extendedMessage(t, cmd))
	m = next.(Model)
	if p.calls != 1 || m.playlist.Index() != 2 || len(engine.playCalls) != 1 || engine.playCalls[0] != "c.mp3" {
		t.Fatalf("index=%d plays=%v", m.playlist.Index(), engine.playCalls)
	}
}

func TestContinuationDoesNotRestartAfterStop(t *testing.T) {
	m, _, engine := newContinuationModel(t)
	m.playlist.SetIndex(1)
	cmd := m.nextTrack()
	m.stopPlayback()
	next, _ := m.Update(extendedMessage(t, cmd))
	m = next.(Model)
	if len(engine.playCalls) != 0 || m.continuation.waiting {
		t.Fatal("stale auto-play after Stop")
	}
	if m.playlist.Len() != 4 {
		t.Fatal("stopped queue should still receive requested batch")
	}
}

func TestContinuationStaleAfterReplacementOrRefresh(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		m, _, _ := newContinuationModel(t)
		cmd := m.extendTracks(false)
		if refresh {
			m.fetchProviderTracks("wave")
		} else {
			m.replacePlaylist(pageOf("new.mp3"))
		}
		next, _ := m.Update(extendedMessage(t, cmd))
		m = next.(Model)
		want := 1
		if refresh {
			want = 2
		}
		if m.playlist.Len() != want {
			t.Fatal("stale batch appended")
		}
	}
}

func TestContinuationErrorAndEmptyDoNotLoop(t *testing.T) {
	for _, fail := range []bool{false, true} {
		m, p, engine := newContinuationModel(t)
		m.playlist.SetIndex(1)
		p.batch = nil
		if fail {
			p.err = errors.New("offline")
		}
		next, _ := m.Update(extendedMessage(t, m.nextTrack()))
		m = next.(Model)
		if m.continuation.waiting || len(engine.playCalls) != 0 {
			t.Fatal("bad result started playback")
		}
		if cmd := m.extendTracks(false); cmd != nil {
			t.Fatal("unbounded retry")
		}
		if fail {
			m.continuation.retryAt = time.Time{}
			p.err = nil
			p.batch = pageOf("recovered.mp3")
			next, _ = m.Update(extendedMessage(t, m.nextTrack()))
			m = next.(Model)
			if m.playlist.Index() != 2 {
				t.Fatal("retry did not resume")
			}
		}
	}
}

func TestLastPlayingTrackPrefetchesOnTick(t *testing.T) {
	m, _, engine := newContinuationModel(t)
	m.playlist.SetIndex(1)
	engine.playing = true
	engine.duration = time.Minute
	next, _ := m.Update(tickMsg(time.Now()))
	m = next.(Model)
	if !m.continuation.loading {
		t.Fatal("last track did not prefetch")
	}
}

func TestFinitePlaylistDoesNotContinue(t *testing.T) {
	m, _, engine := newContinuationModel(t)
	m.clearContinuation()
	m.playlist.SetIndex(1)
	if cmd := m.nextTrack(); cmd != nil {
		t.Fatal("finite queue started request")
	}
	if engine.playing {
		t.Fatal("finite queue should stop")
	}
}

func TestRepeatAllWaitsForContinuationBeforeWrapping(t *testing.T) {
	m, _, engine := newContinuationModel(t)
	m.playlist.SetRepeat(playlist.RepeatAll)
	m.playlist.SetIndex(1)
	cmd := m.nextTrack()
	if !m.continuation.waiting || len(engine.playCalls) != 0 {
		t.Fatal("wrapped before requesting new batch")
	}
	next, _ := m.Update(extendedMessage(t, cmd))
	m = next.(Model)
	if m.playlist.Index() != 2 {
		t.Fatal("did not advance to new batch")
	}
}

func TestRepeatOneAndQueueKeepPriority(t *testing.T) {
	for _, repeatOne := range []bool{false, true} {
		m, p, _ := newContinuationModel(t)
		m.playlist.SetIndex(1)
		if repeatOne {
			m.playlist.SetRepeat(playlist.RepeatOne)
		} else {
			m.playlist.Queue(0)
		}
		m.nextTrack()
		if p.calls != 0 || m.continuation.loading {
			t.Fatal("continuation bypassed repeat or queue")
		}
	}
}

func TestPendingContinuationSuppressesOldPreload(t *testing.T) {
	m, _, engine := newContinuationModel(t)
	engine.hasPreload = true
	before := engine.clearPreloadCalls
	m.extendTracks(false)
	if engine.clearPreloadCalls != before+1 || m.preloadNext() != nil {
		t.Fatal("preloaded old order while batch pending")
	}
}

func TestContinuationStaleAfterProviderSwitch(t *testing.T) {
	m, _, _ := newContinuationModel(t)
	cmd := m.extendTracks(false)
	m.providers = []ProviderEntry{{Name: "Other", Provider: commandsTestProvider{name: "Other"}}}
	m.switchProvider(0)
	next, _ := m.Update(extendedMessage(t, cmd))
	m = next.(Model)
	if m.playlist.Len() != 2 {
		t.Fatal("old provider appended after switch")
	}
}

func TestDrainedLastTrackContinuesThroughTick(t *testing.T) {
	m, _, engine := newContinuationModel(t)
	m.playlist.SetIndex(1)
	engine.playing = true
	engine.drained = true
	engine.duration = time.Minute
	next, cmd := m.Update(tickMsg(time.Now()))
	m = next.(Model)
	if !m.continuation.waiting || engine.playing {
		t.Fatal("drained tail did not stop and wait")
	}
	next, _ = m.Update(extendedMessage(t, cmd))
	m = next.(Model)
	if m.playlist.Index() != 2 || len(engine.playCalls) != 1 || engine.playCalls[0] != "c.mp3" {
		t.Fatalf("index=%d plays=%v", m.playlist.Index(), engine.playCalls)
	}
}

func TestTickPreservesPriorityPreload(t *testing.T) {
	for _, repeat := range []playlist.RepeatMode{playlist.RepeatOff, playlist.RepeatAll, playlist.RepeatOne} {
		t.Run(fmt.Sprint(repeat), func(t *testing.T) {
			m, _, engine := newContinuationModel(t)
			m.playlist.SetIndex(1)
			m.playlist.SetRepeat(repeat)
			if repeat != playlist.RepeatOne {
				m.playlist.Queue(0)
			}
			engine.playing = true
			engine.duration = time.Minute
			engine.hasPreload = true
			before := engine.clearPreloadCalls
			next, _ := m.Update(tickMsg(time.Now()))
			m = next.(Model)
			if m.continuation.loading || engine.clearPreloadCalls != before || !engine.hasPreload {
				t.Fatal("tick continuation discarded priority preload")
			}
		})
	}
}
