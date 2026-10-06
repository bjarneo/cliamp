package model

import (
	"testing"
	"time"

	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/playlist"
)

// positionCountingEngine counts the reads of the engine position.
type positionCountingEngine struct {
	playbackFakeEngine
	positionReads int
}

func (e *positionCountingEngine) Position() time.Duration {
	e.positionReads++
	return e.playbackFakeEngine.Position()
}

func (e *positionCountingEngine) PositionAndDuration() (time.Duration, time.Duration) {
	e.positionReads++
	return e.playbackFakeEngine.PositionAndDuration()
}

// notifyPlaybackChange builds the playback state, which reads the engine
// position, only when the media controls or a playback.state hook get it.
// A hook for another event does not count.
func TestNotifyPlaybackChangeBuildsOnlyWhenNeeded(t *testing.T) {
	tests := []struct {
		name      string
		event     string // the plugin hook, if any
		notifier  bool
		wantReads bool
	}{
		{name: "a playback.state hook", event: luaplugin.EventPlaybackState, wantReads: true},
		{name: "a track.change hook", event: luaplugin.EventTrackChange},
		{name: "media controls", notifier: true, wantReads: true},
		{name: "nothing listens"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := &positionCountingEngine{}
			m := Model{player: engine, playlist: playlist.New()}
			if tt.event != "" {
				m.luaMgr, _, _ = newEventTestPlugin(t, tt.event)
			}
			if tt.notifier {
				m.notifier = &fakeNotifier{}
			}
			m.notifyPlaybackChange()
			if got := engine.positionReads > 0; got != tt.wantReads {
				t.Fatalf("position read = %v, want %v", got, tt.wantReads)
			}
		})
	}
}

// The track events give the decoded duration of a track that has no
// duration of its own, such as a scanned local file. A duration from the
// metadata stays.
func TestPluginEventDuration(t *testing.T) {
	local := playlist.Track{Title: "A", Path: "a.mp3"}
	tagged := playlist.Track{Title: "A", Path: "a.mp3", DurationSecs: 180}
	tests := []struct {
		name   string
		event  string
		track  playlist.Track
		engine time.Duration
		emit   func(m *Model, track playlist.Track)
		want   string
	}{
		{name: "track.change of a local file", event: luaplugin.EventTrackChange, track: local, engine: 20 * time.Second,
			emit: func(m *Model, track playlist.Track) { m.nowPlaying(track) }, want: "20"},
		{name: "track.change keeps the metadata", event: luaplugin.EventTrackChange, track: tagged, engine: 181 * time.Second,
			emit: func(m *Model, track playlist.Track) { m.nowPlaying(track) }, want: "180"},
		{name: "track.change with no known duration", event: luaplugin.EventTrackChange, track: local,
			emit: func(m *Model, track playlist.Track) { m.nowPlaying(track) }, want: "0"},
		{name: "playback.state of a local file", event: luaplugin.EventPlaybackState, track: local, engine: 20 * time.Second,
			emit: func(m *Model, _ playlist.Track) { m.notifyPlaybackChange() }, want: "20"},
		{name: "track.scrobble of a local file", event: luaplugin.EventTrackScrobble, track: local, engine: 20 * time.Second,
			emit: func(m *Model, track playlist.Track) { m.maybeScrobble(track, 15*time.Second, 20*time.Second) }, want: "20"},
		{name: "queue.end of a local file", event: luaplugin.EventQueueEnd, track: local, engine: 20 * time.Second,
			emit: func(m *Model, _ playlist.Track) { m.playingTrackStarted = true; m.endQueue() }, want: "20"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr, reports, _ := newReportTestPlugin(t, tt.event, `tostring(ev.duration)`)
			engine := &playbackFakeEngine{playing: true, duration: tt.engine}
			m := newPluginStateModel(engine, tt.track)
			m.luaMgr = mgr
			m.setPlaybackTrack(tt.track)

			tt.emit(&m, tt.track)
			select {
			case got := <-reports:
				if got != tt.want {
					t.Fatalf("%s duration = %s, want %s", tt.event, got, tt.want)
				}
			case <-time.After(3 * time.Second):
				t.Fatalf("the %s hook did not run", tt.event)
			}
		})
	}
}
