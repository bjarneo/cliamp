//go:build linux

package mediactl

import (
	"io"
	"math"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/prop"

	"github.com/bjarneo/cliamp/internal/playback"
)

// discardTransport is a D-Bus transport that drops every write. It lets
// the tests run the godbus property code without a session bus.
type discardTransport struct{}

func (discardTransport) Read([]byte) (int, error)    { return 0, io.EOF }
func (discardTransport) Write(p []byte) (int, error) { return len(p), nil }
func (discardTransport) Close() error                { return nil }

func newTestService(t *testing.T, send func(tea.Msg)) *Service {
	t.Helper()
	conn, err := dbus.NewConn(discardTransport{})
	if err != nil {
		t.Fatalf("dbus.NewConn() error = %v", err)
	}
	svc, err := newService(conn, send)
	if err != nil {
		t.Fatalf("newService() error = %v", err)
	}
	t.Cleanup(svc.Close)
	return svc
}

// returnsWithin fails the test when fn does not return within 2 seconds.
func returnsWithin(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s did not return while send was blocked", what)
	}
}

// TestServiceCallbacksDoNotWaitForSend holds send blocked, as prog.Send
// blocks until the event loop reads the message. Each D-Bus call and the
// event loop Update must still return, and the messages must keep their order.
func TestServiceCallbacksDoNotWaitForSend(t *testing.T) {
	const playerName = "org.mpris.MediaPlayer2.Player"
	type call struct {
		name    string
		do      func(*Service) *dbus.Error
		wantErr *dbus.Error
	}
	setVolume := func(v float64) call {
		return call{name: "Set Volume", do: func(s *Service) *dbus.Error {
			return s.props.Set(playerName, "Volume", dbus.MakeVariant(v))
		}}
	}
	setNaNVolume := setVolume(math.NaN())
	setNaNVolume.wantErr = prop.ErrInvalidArg
	next := call{name: "Next", do: func(s *Service) *dbus.Error { return playerIface{s}.Next() }}
	playPause := call{name: "PlayPause", do: func(s *Service) *dbus.Error { return playerIface{s}.PlayPause() }}
	seek := call{name: "Seek", do: func(s *Service) *dbus.Error { return playerIface{s}.DoSeek(5_000_000) }}

	tests := []struct {
		name  string
		calls []call
		want  []tea.Msg
	}{
		{
			name:  "one volume change",
			calls: []call{setVolume(0.5)},
			want:  []tea.Msg{playback.SetVolumeMsg{VolumeDB: linearToDb(0.5, initialVolumeFloor)}},
		},
		{
			name:  "rapid volume changes keep order",
			calls: []call{setVolume(0.2), setVolume(0.9), setVolume(0.5), setVolume(0.7)},
			want: []tea.Msg{
				playback.SetVolumeMsg{VolumeDB: linearToDb(0.2, initialVolumeFloor)},
				playback.SetVolumeMsg{VolumeDB: linearToDb(0.9, initialVolumeFloor)},
				playback.SetVolumeMsg{VolumeDB: linearToDb(0.5, initialVolumeFloor)},
				playback.SetVolumeMsg{VolumeDB: linearToDb(0.7, initialVolumeFloor)},
			},
		},
		{
			name:  "out of range volume clamps",
			calls: []call{setVolume(1.5), setVolume(-0.5)},
			want: []tea.Msg{
				playback.SetVolumeMsg{VolumeDB: linearToDb(1, initialVolumeFloor)},
				playback.SetVolumeMsg{VolumeDB: linearToDb(0, initialVolumeFloor)},
			},
		},
		{
			name:  "NaN volume sends nothing",
			calls: []call{setVolume(0.3), setNaNVolume, setVolume(0.6)},
			want: []tea.Msg{
				playback.SetVolumeMsg{VolumeDB: linearToDb(0.3, initialVolumeFloor)},
				playback.SetVolumeMsg{VolumeDB: linearToDb(0.6, initialVolumeFloor)},
			},
		},
		{
			name:  "methods and volume keep order",
			calls: []call{next, setVolume(0.3), playPause, seek, setVolume(0.6)},
			want: []tea.Msg{
				playback.NextMsg{},
				playback.SetVolumeMsg{VolumeDB: linearToDb(0.3, initialVolumeFloor)},
				playback.PlayPauseMsg{},
				playback.SeekMsg{Offset: 5 * time.Second},
				playback.SetVolumeMsg{VolumeDB: linearToDb(0.6, initialVolumeFloor)},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			release := make(chan struct{})
			got := make(chan tea.Msg, len(tt.want))
			svc := newTestService(t, func(msg tea.Msg) {
				<-release
				got <- msg
			})

			for _, c := range tt.calls {
				returnsWithin(t, c.name, func() {
					if err := c.do(svc); err != c.wantErr {
						t.Errorf("%s error = %v, want %v", c.name, err, c.wantErr)
					}
				})
			}
			// The event loop calls Update, which takes the godbus
			// Properties lock that the Volume Set held.
			returnsWithin(t, "Update", func() {
				svc.Update(playback.State{Status: playback.StatusPlaying, VolumeDB: -12, VolumeMinDB: -50})
			})

			close(release)
			for i, want := range tt.want {
				select {
				case msg := <-got:
					if msg != want {
						t.Fatalf("message %d = %#v, want %#v", i, msg, want)
					}
				case <-time.After(2 * time.Second):
					t.Fatalf("message %d not sent, want %#v", i, want)
				}
			}
			select {
			case msg := <-got:
				t.Fatalf("unexpected extra message %#v", msg)
			case <-time.After(50 * time.Millisecond):
			}
		})
	}
}

// TestServiceFirstUpdatePublishesState checks that the first Update
// publishes Volume and CanSeek, also when the new value is the Go zero value.
func TestServiceFirstUpdatePublishesState(t *testing.T) {
	const playerName = "org.mpris.MediaPlayer2.Player"
	tests := []struct {
		name        string
		state       playback.State
		wantVolume  float64
		wantCanSeek bool
	}{
		{name: "muted and not seekable", state: playback.State{VolumeDB: -50, VolumeMinDB: -50}, wantVolume: 0, wantCanSeek: false},
		{name: "full volume and seekable", state: playback.State{VolumeDB: 6, VolumeMinDB: -50, Seekable: true}, wantVolume: 1, wantCanSeek: true},
		{name: "0 dB and seekable", state: playback.State{VolumeDB: 0, VolumeMinDB: -50, Seekable: true}, wantVolume: linearAt(0), wantCanSeek: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService(t, func(tea.Msg) {})
			svc.Update(tt.state)
			if got := svc.props.GetMust(playerName, "Volume"); got != tt.wantVolume {
				t.Errorf("Volume = %v, want %v", got, tt.wantVolume)
			}
			if got := svc.props.GetMust(playerName, "CanSeek"); got != tt.wantCanSeek {
				t.Errorf("CanSeek = %v, want %v", got, tt.wantCanSeek)
			}
		})
	}
}

// TestServiceVolumeUsesStateFloor checks that the Volume property and a
// Volume Set use the engine floor from the last Update. Before the first
// Update, a Volume Set uses the player default floor.
func TestServiceVolumeUsesStateFloor(t *testing.T) {
	const playerName = "org.mpris.MediaPlayer2.Player"
	tests := []struct {
		name       string
		noUpdate   bool
		floor      float64
		volumeDB   float64
		wantVolume float64 // Volume property after Update
		wantSetDB  float64 // dB that a Volume Set of 0 sends
	}{
		{name: "no Update yet uses the player default", noUpdate: true, wantSetDB: -50},
		{name: "floor 0", floor: 0, volumeDB: 0, wantVolume: 0, wantSetDB: 0},
		{name: "floor -30", floor: -30, volumeDB: -30, wantVolume: 0, wantSetDB: -30},
		{name: "floor -50", floor: -50, volumeDB: -40, wantVolume: linearAt(-40), wantSetDB: -50},
		{name: "floor -90", floor: -90, volumeDB: -80, wantVolume: linearAt(-80), wantSetDB: -90},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := make(chan tea.Msg, 1)
			svc := newTestService(t, func(msg tea.Msg) { got <- msg })

			if !tt.noUpdate {
				svc.Update(playback.State{VolumeDB: tt.volumeDB, VolumeMinDB: tt.floor})
				if v := svc.props.GetMust(playerName, "Volume").(float64); math.Abs(v-tt.wantVolume) > 1e-12 {
					t.Fatalf("Volume = %v, want %v", v, tt.wantVolume)
				}
			}

			if err := svc.props.Set(playerName, "Volume", dbus.MakeVariant(0.0)); err != nil {
				t.Fatalf("Set Volume error = %v", err)
			}
			select {
			case msg := <-got:
				if want := (playback.SetVolumeMsg{VolumeDB: tt.wantSetDB}); msg != want {
					t.Fatalf("message = %#v, want %#v", msg, want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Set Volume sent no message")
			}
		})
	}
}

// TestServiceRepublishesClampedVolume checks that a Volume Set that clamps
// to the published volume publishes that volume again. godbus stores the
// value that the client sent, so without a new publish it keeps reporting
// that value. The model sends no Update when its state does not change, so
// the service publishes after send. An Update also publishes.
func TestServiceRepublishesClampedVolume(t *testing.T) {
	const playerName = "org.mpris.MediaPlayer2.Player"
	tests := []struct {
		name     string
		state    playback.State
		set      float64
		want     float64
		lostConn bool // the bus connection closes without Close
	}{
		{name: "below the floor", state: playback.State{VolumeDB: -50, VolumeMinDB: -50}, set: 0.001, want: 0},
		{name: "below 0 at the floor", state: playback.State{VolumeDB: -50, VolumeMinDB: -50}, set: -0.5, want: 0},
		{name: "above 1 at full volume", state: playback.State{VolumeDB: 6, VolumeMinDB: -50}, set: 1.5, want: 1},
		{name: "lost connection", state: playback.State{VolumeDB: -50, VolumeMinDB: -50}, set: 0.001, want: 0, lostConn: true},
	}
	for _, tt := range tests {
		t.Run(tt.name+" without Update", func(t *testing.T) {
			got := make(chan tea.Msg, 1)
			svc := newTestService(t, func(msg tea.Msg) { got <- msg })
			svc.Update(tt.state)

			if tt.lostConn {
				svc.conn.Close()
				// The emit fails, so Set returns an error after the
				// callback queues the message.
				_ = svc.props.Set(playerName, "Volume", dbus.MakeVariant(tt.set))
			} else if err := svc.props.Set(playerName, "Volume", dbus.MakeVariant(tt.set)); err != nil {
				t.Fatalf("Set Volume error = %v", err)
			}
			select {
			case <-got:
			case <-time.After(2 * time.Second):
				t.Fatal("Set Volume sent no message")
			}
			if tt.lostConn {
				// forwardMessages sends the next message only after
				// republishVolume returns without a panic.
				svc.dispatch(playback.NextMsg{})
				select {
				case <-got:
				case <-time.After(2 * time.Second):
					t.Fatal("forwardMessages stopped after the failed publish")
				}
			}
			deadline := time.Now().Add(2 * time.Second)
			for svc.props.GetMust(playerName, "Volume") != tt.want {
				if time.Now().After(deadline) {
					t.Fatalf("Volume = %v, want %v", svc.props.GetMust(playerName, "Volume"), tt.want)
				}
				time.Sleep(time.Millisecond)
			}
		})
		if tt.lostConn {
			continue
		}
		t.Run(tt.name+" with Update", func(t *testing.T) {
			// send blocks, so only Update can publish.
			release := make(chan struct{})
			svc := newTestService(t, func(tea.Msg) { <-release })
			t.Cleanup(func() { close(release) })
			svc.Update(tt.state)

			if err := svc.props.Set(playerName, "Volume", dbus.MakeVariant(tt.set)); err != nil {
				t.Fatalf("Set Volume error = %v", err)
			}
			if got := svc.props.GetMust(playerName, "Volume"); got != tt.set {
				t.Fatalf("Volume before Update = %v, want the client value %v", got, tt.set)
			}
			svc.Update(tt.state)
			if got := svc.props.GetMust(playerName, "Volume"); got != tt.want {
				t.Errorf("Volume = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestServiceUpdateAfterConnectionLoss checks that Update does not panic
// after the bus connection drops. godbus then fails each property emit, and
// SetMust panics. The model calls Update on the event loop, so a panic there
// ends cliamp in the middle of playback.
func TestServiceUpdateAfterConnectionLoss(t *testing.T) {
	base := playback.State{Status: playback.StatusPaused, VolumeDB: -12, VolumeMinDB: -50, Seekable: true}
	tests := []struct {
		name   string
		change func(*playback.State)
	}{
		{name: "status", change: func(s *playback.State) { s.Status = playback.StatusPlaying }},
		{name: "track", change: func(s *playback.State) { s.Track.Title = "Next" }},
		{name: "volume", change: func(s *playback.State) { s.VolumeDB = -6 }},
		{name: "can seek", change: func(s *playback.State) { s.Seekable = false }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService(t, func(tea.Msg) {})
			svc.Update(base)
			svc.conn.Close()

			state := base
			tt.change(&state)
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Update panicked after the connection dropped: %v", r)
				}
			}()
			svc.Update(state)
		})
	}
}

// TestServicePublishAfterConnectionLoss checks the guard for a connection
// that drops while Update or republishVolume publishes.
func TestServicePublishAfterConnectionLoss(t *testing.T) {
	svc := newTestService(t, func(tea.Msg) {})
	svc.conn.Close()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("publish panicked after the connection dropped: %v", r)
		}
	}()
	svc.publish("PlaybackStatus", string(playback.StatusPlaying))
}

// TestServiceTrackIDFollowsTrackIdentity checks that a change of only the
// duration or the art URL keeps mpris:trackid. A buffered track gets its
// probed length mid-track, and a client SetPosition with the id it read
// earlier must still apply. Metadata still publishes the new values.
func TestServiceTrackIDFollowsTrackIdentity(t *testing.T) {
	const playerName = "org.mpris.MediaPlayer2.Player"
	first := playback.Track{Title: "Song", Artist: "Artist", URL: "https://example.com/song", Duration: 3 * time.Minute}
	tests := []struct {
		name      string
		change    func(*playback.Track)
		wantNewID bool
	}{
		{name: "duration", change: func(tr *playback.Track) { tr.Duration = 3*time.Minute + 2*time.Second }},
		{name: "art url", change: func(tr *playback.Track) { tr.ArtURL = "file:///tmp/cover.jpg" }},
		{name: "title", change: func(tr *playback.Track) { tr.Title = "Other" }, wantNewID: true},
		{name: "url", change: func(tr *playback.Track) { tr.URL = "https://example.com/other" }, wantNewID: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService(t, func(tea.Msg) {})
			svc.Update(playback.State{Track: first})
			firstID := svc.props.GetMust(playerName, "Metadata").(map[string]dbus.Variant)["mpris:trackid"].Value()

			next := first
			tt.change(&next)
			svc.Update(playback.State{Track: next})
			metadata := svc.props.GetMust(playerName, "Metadata").(map[string]dbus.Variant)
			if id := metadata["mpris:trackid"].Value(); (id != firstID) != tt.wantNewID {
				t.Fatalf("mpris:trackid = %v after the %s change, first id %v, want new id %v", id, tt.name, firstID, tt.wantNewID)
			}
			if got, want := metadata["mpris:length"].Value(), next.Duration.Microseconds(); got != want {
				t.Fatalf("mpris:length = %v, want %v", got, want)
			}
			if got := metadata["mpris:artUrl"]; next.ArtURL != "" && got.Value() != next.ArtURL {
				t.Fatalf("mpris:artUrl = %v, want %v", got, next.ArtURL)
			}
		})
	}
}
