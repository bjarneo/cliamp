package model

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/external/mass"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/sendspin"
)

type fakeMusicAssistant struct {
	mu      sync.Mutex
	calls   []string
	queued  []string
	players []mass.Player
}

func (f *fakeMusicAssistant) record(call string) {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
}

func (f *fakeMusicAssistant) SendspinPlayerID(context.Context, string) (string, error) {
	return "me", nil
}
func (f *fakeMusicAssistant) Players(context.Context) ([]mass.Player, error) { return f.players, nil }
func (f *fakeMusicAssistant) PlayMedia(_ context.Context, id string, uris []string) error {
	f.record("play " + id)
	f.queued = uris
	return nil
}
func (f *fakeMusicAssistant) Pause(_ context.Context, id string) error {
	f.record("pause " + id)
	return nil
}
func (f *fakeMusicAssistant) Resume(_ context.Context, id string) error {
	f.record("resume " + id)
	return nil
}
func (f *fakeMusicAssistant) Next(_ context.Context, id string) error {
	f.record("next " + id)
	return nil
}
func (f *fakeMusicAssistant) Previous(_ context.Context, id string) error {
	f.record("previous " + id)
	return nil
}
func (f *fakeMusicAssistant) Join(_ context.Context, leader, id string) error {
	f.record("join " + leader + " " + id)
	return nil
}
func (f *fakeMusicAssistant) Leave(_ context.Context, id string) error {
	f.record("leave " + id)
	return nil
}

type fakeRenderer struct{ connected, streaming bool }

func (r *fakeRenderer) ClientID() string               { return "client" }
func (r *fakeRenderer) Connected() bool                { return r.connected }
func (r *fakeRenderer) Streaming() bool                { return r.streaming }
func (r *fakeRenderer) NowPlaying() sendspin.TrackInfo { return sendspin.TrackInfo{} }

func massRow(uri string) playlist.Track {
	return playlist.Track{Path: "mass://" + uri, Title: uri, ProviderMeta: map[string]string{mass.MetaURI: uri}}
}

func massTestModel(t *testing.T, r *fakeRenderer, tracks ...playlist.Track) (Model, *fakeMusicAssistant, *playbackFakeEngine) {
	t.Helper()
	engine := &playbackFakeEngine{}
	m := newHeadlessModel(t, engine, nil, tracks...)
	ma := &fakeMusicAssistant{}
	m.SetMusicAssistant(ma, r)
	return m, ma, engine
}

func statusTexts(msgs []tea.Msg) []string {
	var out []string
	for _, msg := range msgs {
		if s, ok := msg.(ShowStatusMsg); ok {
			out = append(out, s.Text)
		}
	}
	return out
}

// Playing a Music Assistant row queues it and the Music Assistant rows after
// it, so playback continues past the row, and opens the Sendspin output.
func TestPlayMassRowQueuesFollowingRows(t *testing.T) {
	local := playlist.Track{Path: "/music/local.flac", Title: "local"}
	m, ma, engine := massTestModel(t, &fakeRenderer{connected: true},
		massRow("lib://track/0"), massRow("lib://track/1"), local, massRow("lib://album/2"))
	m.playlist.SetIndex(1)

	runCmd(m.playCurrentTrack())

	if !slices.Equal(ma.calls, []string{"play me"}) {
		t.Fatalf("calls = %v", ma.calls)
	}
	if want := []string{"lib://track/1", "lib://album/2"}; !reflect.DeepEqual(ma.queued, want) {
		t.Errorf("queued = %v, want %v", ma.queued, want)
	}
	if !slices.Equal(engine.playCalls, []string{sendspin.URI}) {
		t.Errorf("engine played %v, want the Sendspin stream", engine.playCalls)
	}
}

func TestPlayMassRowExplainsOfflineRenderer(t *testing.T) {
	m, ma, engine := massTestModel(t, &fakeRenderer{connected: false}, massRow("lib://track/0"))
	m.playlist.SetIndex(0)

	texts := statusTexts(runCmd(m.playCurrentTrack()))

	if len(ma.calls) != 0 {
		t.Errorf("calls = %v, want none while the renderer is offline", ma.calls)
	}
	if len(texts) != 1 || !strings.Contains(texts[0], "not connected") {
		t.Errorf("status = %q", texts)
	}
	if slices.Contains(engine.playCalls, "mass://lib://track/0") {
		t.Error("a Music Assistant row must never be opened as a file")
	}
}

// While Music Assistant's stream owns the speaker, transport keys drive its
// queue; otherwise they drive the local playlist.
func TestTransportFollowsSpeakerOwner(t *testing.T) {
	r := &fakeRenderer{connected: true, streaming: true}
	m, ma, engine := massTestModel(t, r, massRow("lib://track/0"), massRow("lib://track/1"))
	m.playlist.SetIndex(0)
	engine.playing = true

	runCmd(m.skipNext())
	runCmd(m.skipPrev())
	runCmd(m.togglePlayPause())
	runCmd(m.togglePlayPause())

	if want := []string{"next me", "previous me", "pause me", "resume me"}; !slices.Equal(ma.calls, want) {
		t.Fatalf("calls = %v, want %v", ma.calls, want)
	}
	if m.playlist.Index() != 0 {
		t.Errorf("local playlist moved to %d", m.playlist.Index())
	}

	r.streaming = false
	ma.calls = nil
	if cmd := m.massTransport("next"); cmd != nil {
		t.Error("massTransport returned a command while local audio owns the speaker")
	}
}

func TestPlayingLocalTrackPausesMusicAssistant(t *testing.T) {
	local := playlist.Track{Path: "/music/local.flac", Title: "local"}
	m, ma, _ := massTestModel(t, &fakeRenderer{connected: true, streaming: true}, local)
	m.playlist.SetIndex(0)

	runCmd(m.playCurrentTrack())

	if !slices.Contains(ma.calls, "pause me") {
		t.Errorf("calls = %v, want Music Assistant paused", ma.calls)
	}
}

func TestSpaceTogglesSpeakerInLocalGroup(t *testing.T) {
	m, ma, _ := massTestModel(t, &fakeRenderer{connected: true})
	m.massGroup = massGroupState{visible: true, localID: "me", rows: []mass.Player{
		{ID: "me", Name: "cliamp", Available: true},
		{ID: "kitchen", Name: "Kitchen", Available: true, SyncedTo: "me"},
		{ID: "den", Name: "Den", Available: true},
		{ID: "attic", Name: "Attic"},
	}}

	press := func(row int) {
		m.massGroup.cursor = row
		m.massGroup.loading = false
		runCmd(m.handleMassGroupKey(tea.KeyPressMsg{Code: tea.KeySpace}))
	}
	press(0) // this computer: nothing to do
	press(1) // grouped: take it out
	press(2) // free: add it
	press(3) // offline: refused

	if want := []string{"leave kitchen", "join me den"}; !slices.Equal(ma.calls, want) {
		t.Errorf("calls = %v, want %v", ma.calls, want)
	}
}
