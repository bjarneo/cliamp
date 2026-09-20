package model

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
	"github.com/charmbracelet/x/ansi"
)

// Native engine seeks discard a previously registered gapless source.
type reviewSeekEngine struct{ *playbackFakeEngine }

func (e *reviewSeekEngine) Seek(ticket uint64, offset time.Duration) error {
	if err := e.playbackFakeEngine.Seek(ticket, offset); err != nil {
		return err
	}
	e.ClearPreload()
	return nil
}

func TestImmediateSeekRearmsGaplessSource(t *testing.T) {
	for _, absolute := range []bool{false, true} {
		name := "relative"
		if absolute {
			name = "absolute"
		}
		t.Run(name, func(t *testing.T) {
			e := &reviewSeekEngine{&playbackFakeEngine{playing: true, seekable: true, duration: time.Hour}}
			pl := playlist.New()
			pl.Add(playlist.Track{Path: "first.mp3"}, playlist.Track{Path: "second.mp3"})
			m := Model{player: e, playlist: pl}
			first, _ := pl.Current()
			m.setPlaybackTrack(first)
			prepare := m.preloadNext()
			if prepare == nil {
				t.Fatal("missing initial preload")
			}
			updated, _ := m.Update(prepare())
			m = updated.(Model)
			old := e.nextTicket
			if old == 0 {
				t.Fatal("initial preload not registered")
			}
			var cmd tea.Cmd
			if absolute {
				cmd = m.seekAbsolute(30 * time.Second)
			} else {
				cmd = m.doSeek(5 * time.Second)
			}
			if cmd == nil {
				t.Fatal("successful seek did not schedule replacement preload")
			}
			updated, _ = m.Update(cmd())
			m = updated.(Model)
			if e.nextTicket == 0 || e.nextTicket == old || m.preloaded == nil || m.preloaded.ticket != e.nextTicket {
				t.Fatalf("preload not rearmed: old=%d engine=%d model=%+v", old, e.nextTicket, m.preloaded)
			}
		})
	}
}

func TestStopDiscardsAsyncSeekFailure(t *testing.T) {
	e := &playbackFakeEngine{playing: true, seekable: true, ytdlSeek: true, duration: time.Hour, seekYTDLErr: errors.New("restart failed")}
	m := streamSeekModel(e)
	cmd := m.seekAbsolute(30 * time.Second)
	if cmd == nil {
		t.Fatal("missing seek")
	}
	completion := cmd()
	m.stopPlayback()
	before := m.status
	updated, follow := m.Update(completion)
	m = updated.(Model)
	if follow != nil || m.status != before || m.seek.active || m.seek.inFlight || m.seek.grace != 0 {
		t.Fatalf("stopped seek completion affected state: status=%+v seek=%+v", m.status, m.seek)
	}
}

func TestPendingStationPresentationKeepsActiveMetadata(t *testing.T) {
	e := &playbackFakeEngine{playing: true}
	old := playlist.Track{Path: "https://radio.invalid/old", Title: "Old station", Stream: true}
	requested := playlist.Track{Path: "https://radio.invalid/new", Title: "Requested station", Stream: true}
	pl := playlist.New()
	pl.Add(old, requested)
	m := Model{player: e, playlist: pl}
	m.setPlaybackTrack(old)
	m.streamTitle = "Old station song"
	previousWidth := ui.PanelWidth
	ui.PanelWidth = 100
	defer func() { ui.PanelWidth = previousWidth }()
	if m.playTrack(requested) == nil {
		t.Fatal("missing station preparation")
	}
	for name, rendered := range map[string]string{"header": ansi.Strip(m.renderTrackInfo()), "terminal": renderTerminalTitle(m.terminalTitleValues())} {
		if !strings.Contains(rendered, requested.Title) || strings.Contains(rendered, "Old station song") {
			t.Fatalf("%s shows mismatched station metadata: %q", name, rendered)
		}
	}
	active, _, ok := m.playbackSnapshot()
	if !ok || active.Path != old.Path || m.streamTitle != "Old station song" {
		t.Fatalf("active metadata changed while preparing: track=%+v title=%q", active, m.streamTitle)
	}
}

func TestDrainedFeedRemainsOnePendingRequest(t *testing.T) {
	e := &playbackFakeEngine{playing: true, drained: true}
	pl := playlist.New()
	pl.Add(playlist.Track{Path: "old.mp3"}, playlist.Track{Path: "https://feed.invalid/rss", Feed: true}, playlist.Track{Path: "later.mp3"})
	m := Model{player: e, playlist: pl, vis: ui.NewVisualizer(float64(e.SampleRate()))}
	old, _ := pl.Current()
	m.setPlaybackTrack(old)
	m.SetVisualizer("none")
	updated, _ := m.Update(tickMsg(time.Now()))
	m = updated.(Model)
	if m.pending == nil || !m.buffering || pl.Index() != 1 {
		t.Fatalf("feed resolution not reserved: pending=%+v buffering=%v index=%d", m.pending, m.buffering, pl.Index())
	}
	ticket := m.pending.ticket
	for range 3 {
		updated, _ = m.Update(tickMsg(time.Now()))
		m = updated.(Model)
	}
	if m.pending == nil || m.pending.ticket != ticket || pl.Index() != 1 {
		t.Fatalf("drained ticks advanced pending feed: pending=%+v index=%d", m.pending, pl.Index())
	}
}

func TestStaleFeedCannotReplaceStopOrSelection(t *testing.T) {
	for _, stop := range []bool{true, false} {
		name := "selection"
		if stop {
			name = "stop"
		}
		t.Run(name, func(t *testing.T) {
			e := &playbackFakeEngine{playing: true}
			old := playlist.Track{Path: "old.mp3"}
			pl := playlist.New()
			pl.Add(old)
			m := Model{player: e, playlist: pl}
			m.setPlaybackTrack(old)
			m.playTrack(playlist.Track{Path: "https://feed.invalid/rss", Feed: true})
			if m.pending == nil {
				t.Fatal("feed has no reserved ticket")
			}
			ticket := m.pending.ticket
			if stop {
				m.stopPlayback()
			} else {
				m.playTrack(playlist.Track{Path: "replacement.mp3"})
			}
			updated, cmd := m.Update(feedTrackResolvedMsg{ticket: ticket, tracks: []playlist.Track{{Path: "stale-episode.mp3"}}})
			m = updated.(Model)
			current, _ := pl.Current()
			if cmd != nil || pl.Len() != 1 || current.Path != old.Path {
				t.Fatalf("stale feed replaced queue: %+v", pl.Tracks())
			}
			if stop && e.playing {
				t.Fatal("stale feed restarted stopped playback")
			}
			if !stop && ownedPath(m) != "replacement.mp3" {
				t.Fatalf("stale feed replaced selection: %q", ownedPath(m))
			}
		})
	}
}

func TestFailedSourceStopsDrainedRepeatPlayback(t *testing.T) {
	for _, repeat := range []playlist.RepeatMode{playlist.RepeatOff, playlist.RepeatOne, playlist.RepeatAll} {
		t.Run(repeat.String(), func(t *testing.T) {
			e := &playbackFakeEngine{playing: true, drained: true}
			old := playlist.Track{Path: "old.mp3"}
			requested := playlist.Track{Path: "https://stream.invalid/new", Stream: true, DurationSecs: 120}
			pl := playlist.New()
			pl.Add(old, requested)
			pl.SetRepeat(repeat)
			m := Model{player: e, playlist: pl, vis: ui.NewVisualizer(float64(e.SampleRate()))}
			m.SetVisualizer("none")
			m.setPlaybackTrack(old)
			m.playTrack(requested)
			ticket := m.pending.ticket
			updated, _ := m.Update(sourcePreparedMsg{ticket: ticket, track: requested, err: errors.New("source unavailable")})
			m = updated.(Model)
			starts := e.startSeq
			for range 3 {
				updated, _ = m.Update(tickMsg(time.Now()))
				m = updated.(Model)
			}
			if e.playing || m.pending != nil || e.startSeq != starts {
				t.Fatalf("failed drained source retried: playing=%v pending=%+v starts=%d/%d", e.playing, m.pending, e.startSeq, starts)
			}
		})
	}
}

type reviewAdvanceOnClearEngine struct{ *playbackFakeEngine }

func (e *reviewAdvanceOnClearEngine) ClearPreload() {
	e.gaplessAdvanced = true
	e.playbackFakeEngine.ClearPreload()
}

func TestNestedGaplessAdoptionDeliversLyrics(t *testing.T) {
	e := &reviewAdvanceOnClearEngine{&playbackFakeEngine{playing: true}}
	old := playlist.Track{Path: "old.mp3", Title: "Old", Artist: "Artist"}
	next := playlist.Track{Path: "next.mp3", Title: "Next", Artist: "Artist", EmbeddedLyrics: "[00:01.00]Adopted lyrics"}
	pl := playlist.New()
	pl.Add(old, next)
	m := Model{player: e, playlist: pl, lyrics: lyricsState{visible: true}}
	m.setPlaybackTrack(old)
	seedGaplessPreload(&m, e.playbackFakeEngine, next)
	m.replacePlayerPlaylist([]playlist.Track{{Path: "browsed.mp3"}})
	updated, cmd := m.Update(struct{}{})
	m = updated.(Model)
	if ownedPath(m) != next.Path || cmd == nil {
		t.Fatalf("nested adoption lost lyrics effect: active=%q cmd=%v", ownedPath(m), cmd != nil)
	}
	var deliver func(tea.Cmd)
	delivered := false
	deliver = func(command tea.Cmd) {
		if command == nil {
			return
		}
		switch msg := command().(type) {
		case tea.BatchMsg:
			for _, child := range msg {
				deliver(child)
			}
		case lyricsLoadedMsg:
			delivered = true
			updated, _ = m.Update(msg)
			m = updated.(Model)
		default:
			t.Fatalf("unexpected adoption effect %T", msg)
		}
	}
	deliver(cmd)
	if !delivered || m.lyrics.loading || len(m.lyrics.lines) != 1 {
		t.Fatalf("adopted lyrics not delivered: delivered=%v loading=%v lines=%v", delivered, m.lyrics.loading, m.lyrics.lines)
	}
}
