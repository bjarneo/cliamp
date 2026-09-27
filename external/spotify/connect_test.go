package spotify

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/devgianlu/go-librespot/dealer"
	librespotPlayer "github.com/devgianlu/go-librespot/player"
	connectpb "github.com/devgianlu/go-librespot/proto/spotify/connectstate"
	devicespb "github.com/devgianlu/go-librespot/proto/spotify/connectstate/devices"
	"google.golang.org/protobuf/proto"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/playlist"
)

func mustPayload(t *testing.T, body string) dealer.RequestPayload {
	t.Helper()
	var p dealer.RequestPayload
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return p
}

func newTestReceiver(send func(any)) *connectReceiver {
	return newConnectReceiver(&Session{}, ConnectConfig{Name: "test-cliamp", Send: send})
}

func TestConnectConfigDeviceName(t *testing.T) {
	tests := []struct {
		name string
		cfg  ConnectConfig
		want string
	}{
		{"empty falls back to default", ConnectConfig{}, connectDefaultName},
		{"blank falls back to default", ConnectConfig{Name: "  "}, connectDefaultName},
		{"custom name", ConnectConfig{Name: "Kitchen"}, "Kitchen"},
	}
	for _, tt := range tests {
		if got := tt.cfg.deviceName(); got != tt.want {
			t.Errorf("deviceName() = %q, want %q", got, tt.want)
		}
	}
}

func TestRepeatMode(t *testing.T) {
	tests := []struct {
		name string
		opts *connectpb.ContextPlayerOptions
		want playlist.RepeatMode
	}{
		{"nil", nil, playlist.RepeatOff},
		{"off", &connectpb.ContextPlayerOptions{}, playlist.RepeatOff},
		{"context", &connectpb.ContextPlayerOptions{RepeatingContext: true}, playlist.RepeatAll},
		{"track", &connectpb.ContextPlayerOptions{RepeatingTrack: true}, playlist.RepeatOne},
		{"track beats context", &connectpb.ContextPlayerOptions{RepeatingContext: true, RepeatingTrack: true}, playlist.RepeatOne},
	}
	for _, tt := range tests {
		if got := repeatMode(tt.opts); got != tt.want {
			t.Errorf("%s: repeatMode() = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestProvidedToPlaylist(t *testing.T) {
	pt := &connectpb.ProvidedTrack{
		Uri: "spotify:track:abc",
		Metadata: map[string]string{
			"title":       "Song",
			"artist_name": "Artist",
			"album_title": "Album",
			"duration_ms": "241000",
		},
	}
	got := providedToPlaylist(pt)
	if got.Path != "spotify:track:abc" || got.Title != "Song" || got.Artist != "Artist" ||
		got.Album != "Album" || got.DurationSecs != 241 {
		t.Fatalf("providedToPlaylist() = %+v", got)
	}
}

func TestProvidedToPlaylistUnplayable(t *testing.T) {
	got := providedToPlaylist(&connectpb.ProvidedTrack{Uri: "spotify:track:x", Removed: []string{"x"}})
	if !got.Unplayable {
		t.Fatal("removed track should map to Unplayable")
	}
}

func TestSplitSpotifyURI(t *testing.T) {
	tests := []struct {
		uri     string
		wantTyp string
		wantID  string
		wantOK  bool
	}{
		{"spotify:track:abc123", "track", "abc123", true},
		{"spotify:episode:xyz", "episode", "xyz", true},
		{"/local/file.mp3", "", "", false},
		{"spotify:noid", "", "", false},
		{"spotify:track:", "", "", false},
	}
	for _, tt := range tests {
		typ, id, ok := splitSpotifyURI(tt.uri)
		if typ != tt.wantTyp || id != tt.wantID || ok != tt.wantOK {
			t.Errorf("splitSpotifyURI(%q) = %q,%q,%v want %q,%q,%v", tt.uri, typ, id, ok, tt.wantTyp, tt.wantID, tt.wantOK)
		}
	}
}

func TestContextMetadataMerges(t *testing.T) {
	got := contextMetadata(
		map[string]string{"context_description": "My List", "shared": "ctx"},
		map[string]string{"shared": "list", "image_url": "img"},
	)
	if got["context_description"] != "My List" || got["image_url"] != "img" || got["shared"] != "ctx" {
		t.Fatalf("contextMetadata = %v", got)
	}
}

// TestHandleCommandDispatch table-drives remote commands into playback msgs.
func TestHandleCommandDispatch(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    func(any) bool
	}{
		{"pause", `{"command":{"endpoint":"pause"}}`, func(m any) bool { _, ok := m.(playback.PauseMsg); return ok }},
		{"resume", `{"command":{"endpoint":"resume"}}`, func(m any) bool { _, ok := m.(playback.PlayMsg); return ok }},
		{"skip_next", `{"command":{"endpoint":"skip_next"}}`, func(m any) bool { _, ok := m.(playback.NextMsg); return ok }},
		{"skip_prev", `{"command":{"endpoint":"skip_prev"}}`, func(m any) bool { _, ok := m.(playback.PrevMsg); return ok }},
		{"seek absolute", `{"command":{"endpoint":"seek_to","value":45000}}`, func(m any) bool { v, ok := m.(playback.SetPositionMsg); return ok && v.Position == 45*time.Second }},
		{"seek beginning", `{"command":{"endpoint":"seek_to","position":12000,"relative":"beginning"}}`, func(m any) bool { v, ok := m.(playback.SetPositionMsg); return ok && v.Position == 12*time.Second }},
		{"seek relative", `{"command":{"endpoint":"seek_to","position":-3000,"relative":"current"}}`, func(m any) bool { v, ok := m.(playback.SeekMsg); return ok && v.Offset == -3*time.Second }},
		{"shuffle on", `{"command":{"endpoint":"set_shuffling_context","value":true}}`, func(m any) bool { v, ok := m.(playback.SetShuffleMsg); return ok && v.On }},
		{"shuffle off", `{"command":{"endpoint":"set_shuffling_context","value":false}}`, func(m any) bool { v, ok := m.(playback.SetShuffleMsg); return ok && !v.On }},
		{"repeat context", `{"command":{"endpoint":"set_repeating_context","value":true}}`, func(m any) bool { v, ok := m.(playback.SetRepeatMsg); return ok && v.Mode == playlist.RepeatAll }},
		{"repeat track", `{"command":{"endpoint":"set_repeating_track","value":true}}`, func(m any) bool { v, ok := m.(playback.SetRepeatMsg); return ok && v.Mode == playlist.RepeatOne }},
		{"queue add", `{"command":{"endpoint":"add_to_queue","track":{"uri":"spotify:track:q1"}}}`, func(m any) bool { v, ok := m.(playback.EnqueueMsg); return ok && v.Track.Path == "spotify:track:q1" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sent []any
			r := newTestReceiver(func(m any) { sent = append(sent, m) })
			if !r.handleCommand(nil, mustPayload(t, tt.payload)) {
				t.Fatal("handleCommand returned false")
			}
			if len(sent) == 0 {
				t.Fatal("no message sent")
			}
			found := false
			for _, m := range sent {
				if tt.want(m) {
					found = true
				}
			}
			if !found {
				t.Fatalf("sent %v; expected message not found", sent)
			}
		})
	}
}

func TestHandleCommandUnknown(t *testing.T) {
	r := newTestReceiver(func(any) {})
	if r.handleCommand(nil, mustPayload(t, `{"command":{"endpoint":"bogus_cmd"}}`)) {
		t.Fatal("unknown command should return false")
	}
}

// TestOptionsToggleSequence: repeat track then context-off yields RepeatOne;
// clearing both yields RepeatOff — matching the app's toggle sequence.
func TestOptionsToggleSequence(t *testing.T) {
	var sent []any
	r := newTestReceiver(func(m any) { sent = append(sent, m) })

	must := func(body string) {
		t.Helper()
		if !r.handleCommand(nil, mustPayload(t, body)) {
			t.Fatalf("handleCommand failed: %s", body)
		}
	}
	must(`{"command":{"endpoint":"set_repeating_track","value":true}}`)
	must(`{"command":{"endpoint":"set_repeating_context","value":true}}`)
	if got := repeatMode(r.opts); got != playlist.RepeatOne {
		t.Fatalf("repeatMode = %v, want RepeatOne", got)
	}
	must(`{"command":{"endpoint":"set_repeating_track","value":false}}`)
	if got := repeatMode(r.opts); got != playlist.RepeatAll {
		t.Fatalf("repeatMode = %v, want RepeatAll", got)
	}
	must(`{"command":{"endpoint":"set_repeating_context","value":false}}`)
	if got := repeatMode(r.opts); got != playlist.RepeatOff {
		t.Fatalf("repeatMode = %v, want RepeatOff", got)
	}
}

// TestPlayerStateRules checks the invariants Spotify requires: paused ⇒ speed 0,
// playing ⇒ position advances between snapshot and report.
func TestPlayerStateRules(t *testing.T) {
	r := newTestReceiver(func(any) {})

	r.notifyUpdate(playback.State{
		Status:   playback.StatusPaused,
		Position: 30 * time.Second,
		Track:    playback.Track{URL: "spotify:track:abc", Duration: 3 * time.Minute},
	})
	st := r.playerState()
	if !st.IsPaused || st.PlaybackSpeed != 0 {
		t.Fatalf("paused state: IsPaused=%v speed=%v, want paused with speed 0", st.IsPaused, st.PlaybackSpeed)
	}
	if st.PositionAsOfTimestamp != 30_000 {
		t.Fatalf("paused position = %d, want 30000", st.PositionAsOfTimestamp)
	}
	if st.Track == nil || st.Track.Uri != "spotify:track:abc" {
		t.Fatalf("track = %+v, want spotify uri", st.Track)
	}

	r.notifyUpdate(playback.State{
		Status:   playback.StatusPlaying,
		Position: 30 * time.Second,
		Track:    playback.Track{URL: "spotify:track:abc", Duration: 3 * time.Minute},
	})
	time.Sleep(5 * time.Millisecond)
	st = r.playerState()
	if st.IsPaused || st.PlaybackSpeed != 1 {
		t.Fatalf("playing state: IsPaused=%v speed=%v", st.IsPaused, st.PlaybackSpeed)
	}
	if st.PositionAsOfTimestamp < 30_000 {
		t.Fatalf("playing position should advance, got %d", st.PositionAsOfTimestamp)
	}
}

// TestPlayerStateNonSpotifyTrack: local files keep the device registered but
// report no track (cliamp can't resolve arbitrary paths into Spotify URIs).
func TestPlayerStateNonSpotifyTrack(t *testing.T) {
	r := newTestReceiver(func(any) {})
	r.notifyUpdate(playback.State{
		Status: playback.StatusPlaying,
		Track:  playback.Track{URL: "/home/u/song.mp3", Title: "Local"},
	})
	st := r.playerState()
	if st.Track != nil {
		t.Fatalf("non-spotify track should not be reported, got %+v", st.Track)
	}
	if !st.IsPlaying {
		t.Fatal("device should still report playing")
	}
}

func TestDeviceInfo(t *testing.T) {
	r := newTestReceiver(func(any) {})
	r.notifyUpdate(playback.State{VolumeDB: -30}) // mute → 0 volume steps
	di := r.deviceInfo()
	if di.Name != "test-cliamp" {
		t.Fatalf("Name = %q", di.Name)
	}
	if di.DeviceType != devicespb.DeviceType_COMPUTER {
		t.Fatalf("DeviceType = %v", di.DeviceType)
	}
	if !di.Capabilities.SupportsTransferCommand || !di.Capabilities.IsControllable {
		t.Fatal("missing required Connect capabilities")
	}
	if di.Volume != 0 {
		t.Fatalf("Volume = %d, want 0 for -30dB", di.Volume)
	}
}

func TestSleepTimerEcho(t *testing.T) {
	var sent []any
	r := newTestReceiver(func(m any) { sent = append(sent, m) })
	r.handleCommand(nil, mustPayload(t, `{"command":{"endpoint":"set_sleep_timer","timer_type":{"type":"duration","duration_s":60}}}`))
	if r.sleepState == nil {
		t.Fatal("sleep timer state should be echoed")
	}
	ts := r.sleepState.GetTimestamp()
	if ts == nil || ts.Timestamp <= time.Now().UnixMilli() {
		t.Fatalf("sleep timer timestamp = %+v", ts)
	}
	r.handleCommand(nil, mustPayload(t, `{"command":{"endpoint":"set_sleep_timer","timer_type":{"type":"clear"}}}`))
	if _, ok := r.sleepState.TimerType.(*connectpb.SleepTimer_None_); !ok {
		t.Fatalf("cleared sleep timer = %v", r.sleepState.TimerType)
	}
}

// TestPutDelayRespectsCooldown: a 429 backoff must win over the regular
// ~200ms min-PUT spacing, even when a playback update arrives mid-cooldown
// with a fresh lastPut (which used to reset the timer and wipe the backoff).
func TestPutDelayRespectsCooldown(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name              string
		lastPut, cooldown time.Time
		wantMin, wantMax  time.Duration
	}{
		{"idle: PUT now", now.Add(-time.Second), time.Time{}, 0, 0},
		{"spacing only", now, time.Time{}, 150 * time.Millisecond, connectStateMinPut},
		{"cooldown beats fresh lastPut", now, now.Add(30 * time.Second), 29 * time.Second, 30 * time.Second},
		{"cooldown beats stale lastPut", now.Add(-time.Hour), now.Add(5 * time.Second), 4 * time.Second, 5 * time.Second},
		{"expired cooldown falls back to spacing", now, now.Add(-time.Second), 150 * time.Millisecond, connectStateMinPut},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := putDelay(tt.lastPut, tt.cooldown, now); got < tt.wantMin || got > tt.wantMax {
				t.Fatalf("putDelay() = %v, want in [%v, %v]", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

// TestNotifyUpdateOnlyDirtiesOnChange: the TUI notifies every tick, so
// position drift alone must not mark dirty — otherwise the receiver PUTs
// ~1/sec and risks being throttled. Only meaningful change dirties.
func TestNotifyUpdateOnlyDirtiesOnChange(t *testing.T) {
	r := newTestReceiver(func(any) {})
	drained := func() bool {
		select {
		case <-r.dirty:
			return true
		default:
			return false
		}
	}

	st := playback.State{
		Status:   playback.StatusPlaying,
		Track:    playback.Track{Title: "Song", URL: "spotify:track:abc"},
		VolumeDB: -6,
		Seekable: true,
	}
	r.notifyUpdate(st)
	if !drained() {
		t.Fatal("first notifyUpdate did not signal dirty")
	}
	// Identical state plus position drift: no dirty.
	st.Position += time.Second
	r.notifyUpdate(st)
	if drained() {
		t.Fatal("unchanged notifyUpdate signaled dirty")
	}
	// Status flip dirties again (snapshot stays fresh regardless).
	st.Status = playback.StatusPaused
	r.notifyUpdate(st)
	if !drained() {
		t.Fatal("changed notifyUpdate did not signal dirty")
	}
	if got, _ := r.snapshot(); got.Status != playback.StatusPaused {
		t.Fatalf("snapshot status = %q, want Paused", got.Status)
	}
}

// TestSchedulePutDefersToCooldown proves end-to-end that a dirty signal
// during backoff doesn't trigger an early PUT: lastPut is stale (so without
// the cooldown this would flush immediately), yet no PUT happens.
func TestSchedulePutDefersToCooldown(t *testing.T) {
	r := newTestReceiver(func(any) {})
	r.spotConnID = "conn-id"
	r.cooldownUntil = time.Now().Add(30 * time.Second)
	r.schedulePut(nil) // nil session: would panic if it flushed
	if !r.lastPut.IsZero() {
		t.Fatalf("schedulePut flushed during cooldown, lastPut = %v", r.lastPut)
	}
}

func TestVolumeMessage(t *testing.T) {
	var sent []any
	r := newTestReceiver(func(m any) { sent = append(sent, m) })
	cmd := &connectpb.SetVolumeCommand{Volume: librespotPlayer.MaxStateVolume}
	payload, err := proto.Marshal(cmd)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	r.handleMessage(nil, dealer.Message{Uri: "hm://connect-state/v1/connect/volume", Payload: payload})
	if len(sent) != 1 {
		t.Fatalf("sent %v", sent)
	}
	vm, ok := sent[0].(playback.SetVolumeMsg)
	if !ok {
		t.Fatalf("sent %T, want SetVolumeMsg", sent[0])
	}
	if vm.VolumeDB < 5.9 || vm.VolumeDB > 6.1 {
		t.Fatalf("VolumeDB = %v, want ~6", vm.VolumeDB)
	}
}
