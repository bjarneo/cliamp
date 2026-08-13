package ipc

import (
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/playback"
)

// captureDispatcher records the last message sent via Send and optionally
// auto-replies on reply channels so handler cases with a Reply can be tested
// without spinning up a real UI goroutine.
type captureDispatcher struct {
	last      any
	autoReply Response
}

func (c *captureDispatcher) Send(msg any) {
	c.last = msg
	switch m := msg.(type) {
	case LoadMsg:
		m.Reply <- c.autoReply
	case ThemeMsg:
		m.Reply <- c.autoReply
	case VisMsg:
		m.Reply <- c.autoReply
	case ShuffleMsg:
		m.Reply <- c.autoReply
	case RepeatMsg:
		m.Reply <- c.autoReply
	case MonoMsg:
		m.Reply <- c.autoReply
	case SpeedMsg:
		m.Reply <- c.autoReply
	case EQMsg:
		m.Reply <- c.autoReply
	case DeviceMsg:
		m.Reply <- c.autoReply
	case StatusRequestMsg:
		m.Reply <- c.autoReply
	case QueueRequestMsg:
		m.Reply <- c.autoReply
	case LibraryRequestMsg:
		m.Reply <- c.autoReply
	case LyricsRequestMsg:
		m.Reply <- c.autoReply
	case HistoryRequestMsg:
		m.Reply <- c.autoReply
	case URLRequestMsg:
		m.Reply <- c.autoReply
	case SaveRequestMsg:
		m.Reply <- c.autoReply
	}
}

func newTestServer(disp Dispatcher) *Server {
	return &Server{disp: disp, done: make(chan struct{})}
}

func TestDispatchSimpleCommands(t *testing.T) {
	tests := []struct {
		cmd    string
		check  func(t *testing.T, got any)
		okWant bool
	}{
		{"play", func(t *testing.T, got any) {
			if _, ok := got.(PlayMsg); !ok {
				t.Errorf("got %T, want PlayMsg", got)
			}
		}, true},
		{"pause", func(t *testing.T, got any) {
			if _, ok := got.(PauseMsg); !ok {
				t.Errorf("got %T, want PauseMsg", got)
			}
		}, true},
		{"toggle", func(t *testing.T, got any) {
			if _, ok := got.(playback.PlayPauseMsg); !ok {
				t.Errorf("got %T, want playback.PlayPauseMsg", got)
			}
		}, true},
		{"stop", func(t *testing.T, got any) {
			if _, ok := got.(playback.StopMsg); !ok {
				t.Errorf("got %T, want playback.StopMsg", got)
			}
		}, true},
		{"next", func(t *testing.T, got any) {
			if _, ok := got.(playback.NextMsg); !ok {
				t.Errorf("got %T, want playback.NextMsg", got)
			}
		}, true},
		{"prev", func(t *testing.T, got any) {
			if _, ok := got.(playback.PrevMsg); !ok {
				t.Errorf("got %T, want playback.PrevMsg", got)
			}
		}, true},
	}

	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			disp := &captureDispatcher{}
			s := newTestServer(disp)
			resp := s.dispatch(Request{Cmd: tt.cmd})
			if resp.OK != tt.okWant {
				t.Errorf("OK = %v, want %v (err=%q)", resp.OK, tt.okWant, resp.Error)
			}
			tt.check(t, disp.last)
		})
	}
}

func TestDispatchUppercaseCommand(t *testing.T) {
	disp := &captureDispatcher{}
	s := newTestServer(disp)
	resp := s.dispatch(Request{Cmd: "PLAY"})
	if !resp.OK {
		t.Fatalf("uppercase cmd PLAY should still be accepted, got err=%q", resp.Error)
	}
	if _, ok := disp.last.(PlayMsg); !ok {
		t.Errorf("got %T, want PlayMsg", disp.last)
	}
}

func TestDispatchVolume(t *testing.T) {
	disp := &captureDispatcher{}
	s := newTestServer(disp)
	resp := s.dispatch(Request{Cmd: "volume", Value: -3.5})
	if !resp.OK {
		t.Fatalf("OK = false, err=%q", resp.Error)
	}
	got, ok := disp.last.(VolumeMsg)
	if !ok {
		t.Fatalf("got %T, want VolumeMsg", disp.last)
	}
	if got.DB != -3.5 {
		t.Errorf("DB = %f, want -3.5", got.DB)
	}
}

func TestDispatchSeek(t *testing.T) {
	disp := &captureDispatcher{}
	s := newTestServer(disp)
	resp := s.dispatch(Request{Cmd: "seek", Value: 1.5})
	if !resp.OK {
		t.Fatalf("OK = false, err=%q", resp.Error)
	}
	got, ok := disp.last.(SeekMsg)
	if !ok {
		t.Fatalf("got %T, want SeekMsg", disp.last)
	}
	if got.Offset != 1500*time.Millisecond {
		t.Errorf("Offset = %v, want 1.5s", got.Offset)
	}
}

func TestDispatchSetPosition(t *testing.T) {
	disp := &captureDispatcher{}
	s := newTestServer(disp)
	resp := s.dispatch(Request{Cmd: "seek.set", Value: 1.5})
	if !resp.OK {
		t.Fatalf("OK = false, err=%q", resp.Error)
	}
	got, ok := disp.last.(SetPositionMsg)
	if !ok {
		t.Fatalf("got %T, want SetPositionMsg", disp.last)
	}
	if got.Position != 1500*time.Millisecond {
		t.Errorf("Position = %v, want 1.5s", got.Position)
	}
}

func TestDispatchSetPositionRejectsNegative(t *testing.T) {
	s := newTestServer(&captureDispatcher{})
	resp := s.dispatch(Request{Cmd: "seek.set", Value: -1})
	if resp.OK {
		t.Fatal("seek.set with a negative position should return !OK")
	}
}

func TestDispatchLoadMissingPlaylist(t *testing.T) {
	s := newTestServer(&captureDispatcher{})
	resp := s.dispatch(Request{Cmd: "load"})
	if resp.OK {
		t.Error("load without playlist should return !OK")
	}
	if resp.Error == "" {
		t.Error("error message should be set")
	}
}

func TestDispatchLoadWithReply(t *testing.T) {
	disp := &captureDispatcher{autoReply: Response{OK: true, Playlist: "main"}}
	s := newTestServer(disp)
	resp := s.dispatch(Request{Cmd: "load", Playlist: "main"})
	if !resp.OK {
		t.Fatalf("OK = false, err=%q", resp.Error)
	}
	if resp.Playlist != "main" {
		t.Errorf("Playlist = %q, want main", resp.Playlist)
	}
}

func TestDispatchQueueMissingPath(t *testing.T) {
	s := newTestServer(&captureDispatcher{})
	resp := s.dispatch(Request{Cmd: "queue"})
	if resp.OK {
		t.Error("queue without path should return !OK")
	}
}

func TestDispatchQueue(t *testing.T) {
	disp := &captureDispatcher{}
	s := newTestServer(disp)
	resp := s.dispatch(Request{Cmd: "queue", Path: "/music/song.mp3"})
	if !resp.OK {
		t.Fatalf("OK = false, err=%q", resp.Error)
	}
	got, ok := disp.last.(QueueMsg)
	if !ok {
		t.Fatalf("got %T, want QueueMsg", disp.last)
	}
	if got.Path != "/music/song.mp3" {
		t.Errorf("Path = %q, want /music/song.mp3", got.Path)
	}
}

func TestDispatchThemeMissingName(t *testing.T) {
	s := newTestServer(&captureDispatcher{})
	resp := s.dispatch(Request{Cmd: "theme"})
	if resp.OK {
		t.Error("theme without name should return !OK")
	}
}

func TestDispatchThemeWithReply(t *testing.T) {
	disp := &captureDispatcher{autoReply: Response{OK: true}}
	s := newTestServer(disp)
	resp := s.dispatch(Request{Cmd: "theme", Name: "dracula"})
	if !resp.OK {
		t.Fatalf("OK = false, err=%q", resp.Error)
	}
	got, ok := disp.last.(ThemeMsg)
	if !ok {
		t.Fatalf("got %T, want ThemeMsg", disp.last)
	}
	if got.Name != "dracula" {
		t.Errorf("Name = %q, want dracula", got.Name)
	}
}

func TestDispatchVisMissingName(t *testing.T) {
	s := newTestServer(&captureDispatcher{})
	resp := s.dispatch(Request{Cmd: "vis"})
	if resp.OK {
		t.Error("vis without name should return !OK")
	}
}

func TestDispatchVisWithReply(t *testing.T) {
	disp := &captureDispatcher{autoReply: Response{OK: true, Visualizer: "bars"}}
	s := newTestServer(disp)
	resp := s.dispatch(Request{Cmd: "vis", Name: "bars"})
	if !resp.OK {
		t.Fatalf("OK = false, err=%q", resp.Error)
	}
	if resp.Visualizer != "bars" {
		t.Errorf("Visualizer = %q, want bars", resp.Visualizer)
	}
}

func TestDispatchShuffle(t *testing.T) {
	trueBool := true
	disp := &captureDispatcher{autoReply: Response{OK: true, Shuffle: &trueBool}}
	s := newTestServer(disp)
	resp := s.dispatch(Request{Cmd: "shuffle", Name: "on"})
	if !resp.OK {
		t.Fatalf("OK = false, err=%q", resp.Error)
	}
	got, ok := disp.last.(ShuffleMsg)
	if !ok {
		t.Fatalf("got %T, want ShuffleMsg", disp.last)
	}
	if got.Name != "on" {
		t.Errorf("Name = %q, want on", got.Name)
	}
}

func TestDispatchRepeat(t *testing.T) {
	disp := &captureDispatcher{autoReply: Response{OK: true, Repeat: "all"}}
	s := newTestServer(disp)
	resp := s.dispatch(Request{Cmd: "repeat", Name: "all"})
	if !resp.OK {
		t.Fatalf("OK = false, err=%q", resp.Error)
	}
	if _, ok := disp.last.(RepeatMsg); !ok {
		t.Errorf("got %T, want RepeatMsg", disp.last)
	}
}

func TestDispatchMono(t *testing.T) {
	trueBool := true
	disp := &captureDispatcher{autoReply: Response{OK: true, Mono: &trueBool}}
	s := newTestServer(disp)
	resp := s.dispatch(Request{Cmd: "mono", Name: "toggle"})
	if !resp.OK {
		t.Fatalf("OK = false, err=%q", resp.Error)
	}
	if _, ok := disp.last.(MonoMsg); !ok {
		t.Errorf("got %T, want MonoMsg", disp.last)
	}
}

func TestDispatchSpeedInvalid(t *testing.T) {
	tests := []float64{0, -1, -0.5}
	for _, v := range tests {
		s := newTestServer(&captureDispatcher{})
		resp := s.dispatch(Request{Cmd: "speed", Value: v})
		if resp.OK {
			t.Errorf("speed with value=%v should return !OK", v)
		}
	}
}

func TestDispatchSpeedValid(t *testing.T) {
	disp := &captureDispatcher{autoReply: Response{OK: true, Speed: 1.25}}
	s := newTestServer(disp)
	resp := s.dispatch(Request{Cmd: "speed", Value: 1.25})
	if !resp.OK {
		t.Fatalf("OK = false, err=%q", resp.Error)
	}
	got, ok := disp.last.(SpeedMsg)
	if !ok {
		t.Fatalf("got %T, want SpeedMsg", disp.last)
	}
	if got.Speed != 1.25 {
		t.Errorf("Speed = %f, want 1.25", got.Speed)
	}
}

func TestDispatchEQ(t *testing.T) {
	disp := &captureDispatcher{autoReply: Response{OK: true}}
	s := newTestServer(disp)
	resp := s.dispatch(Request{Cmd: "eq", Name: "rock", Band: 3, Value: 4.5})
	if !resp.OK {
		t.Fatalf("OK = false, err=%q", resp.Error)
	}
	got, ok := disp.last.(EQMsg)
	if !ok {
		t.Fatalf("got %T, want EQMsg", disp.last)
	}
	if got.Name != "rock" || got.Band != 3 || got.Value != 4.5 {
		t.Errorf("EQMsg = %+v, want Name=rock Band=3 Value=4.5", got)
	}
}

func TestDispatchDeviceMissingName(t *testing.T) {
	s := newTestServer(&captureDispatcher{})
	resp := s.dispatch(Request{Cmd: "device"})
	if resp.OK {
		t.Error("device without name should return !OK")
	}
}

func TestDispatchDevice(t *testing.T) {
	disp := &captureDispatcher{autoReply: Response{OK: true, Device: "alsa:default"}}
	s := newTestServer(disp)
	resp := s.dispatch(Request{Cmd: "device", Name: "list"})
	if !resp.OK {
		t.Fatalf("OK = false, err=%q", resp.Error)
	}
	if resp.Device != "alsa:default" {
		t.Errorf("Device = %q, want alsa:default", resp.Device)
	}
}

func TestDispatchStatus(t *testing.T) {
	disp := &captureDispatcher{autoReply: Response{OK: true, State: "playing", Position: 30}}
	s := newTestServer(disp)
	resp := s.dispatch(Request{Cmd: "status"})
	if !resp.OK {
		t.Fatalf("OK = false, err=%q", resp.Error)
	}
	if resp.State != "playing" {
		t.Errorf("State = %q, want playing", resp.State)
	}
	if _, ok := disp.last.(StatusRequestMsg); !ok {
		t.Errorf("got %T, want StatusRequestMsg", disp.last)
	}
}

func TestDispatchQueueRequest(t *testing.T) {
	disp := &captureDispatcher{autoReply: Response{OK: true}}
	resp := newTestServer(disp).dispatch(Request{Cmd: "queue.move", Index: 2, To: 4})
	if !resp.OK {
		t.Fatalf("OK = false, err=%q", resp.Error)
	}
	got, ok := disp.last.(QueueRequestMsg)
	if !ok || got.Op != "queue.move" || got.Index != 2 || got.To != 4 {
		t.Fatalf("got %#v, want queue.move 2 -> 4", disp.last)
	}
}

func TestDispatchProviderSearch(t *testing.T) {
	disp := &captureDispatcher{autoReply: Response{OK: true}}
	resp := newTestServer(disp).dispatch(Request{Cmd: "provider.search", Provider: "local", Query: "ambient", Limit: 20})
	if !resp.OK {
		t.Fatalf("OK = false, err=%q", resp.Error)
	}
	got, ok := disp.last.(LibraryRequestMsg)
	if !ok || got.Provider != "local" || got.Query != "ambient" || got.Limit != 20 {
		t.Fatalf("got %#v, want local ambient search", disp.last)
	}
}

func TestDispatchPlaylistMutation(t *testing.T) {
	disp := &captureDispatcher{autoReply: Response{OK: true}}
	track := TrackInfo{Path: "/song.flac", Title: "Song"}
	resp := newTestServer(disp).dispatch(Request{
		Cmd: "playlist.add", Provider: "local", Playlist: "Mix", NewName: "Renamed", Index: 2, Track: &track,
	})
	if !resp.OK {
		t.Fatalf("OK = false, err=%q", resp.Error)
	}
	got, ok := disp.last.(LibraryRequestMsg)
	if !ok || got.Op != "playlist.add" || got.Playlist != "Mix" || got.NewName != "Renamed" || got.Index != 2 || got.Track == nil || got.Track.Path != track.Path {
		t.Fatalf("got %#v, want complete playlist mutation", disp.last)
	}
}

func TestDispatchAlbumBrowse(t *testing.T) {
	disp := &captureDispatcher{autoReply: Response{OK: true}}
	resp := newTestServer(disp).dispatch(Request{Cmd: "provider.albums", Provider: "navidrome", Artist: "artist", Album: "album", Sort: "recent", Offset: 50, Limit: 100})
	if !resp.OK {
		t.Fatalf("OK = false, err=%q", resp.Error)
	}
	got, ok := disp.last.(LibraryRequestMsg)
	if !ok || got.Artist != "artist" || got.Album != "album" || got.Sort != "recent" || got.Offset != 50 || got.Limit != 100 {
		t.Fatalf("got %#v, want complete album browse", disp.last)
	}
}

func TestDispatchURLLoad(t *testing.T) {
	disp := &captureDispatcher{autoReply: Response{OK: true}}
	resp := newTestServer(disp).dispatch(Request{Cmd: "url.load", Path: "https://example.com/live.mp3"})
	if !resp.OK {
		t.Fatalf("OK = false, err=%q", resp.Error)
	}
	got, ok := disp.last.(URLRequestMsg)
	if !ok || got.URL != "https://example.com/live.mp3" {
		t.Fatalf("got %#v, want URL load", disp.last)
	}
}

func TestDispatchSave(t *testing.T) {
	disp := &captureDispatcher{autoReply: Response{OK: true, Output: "/tmp/song.flac"}}
	resp := newTestServer(disp).dispatch(Request{Cmd: "save"})
	if !resp.OK || resp.Output != "/tmp/song.flac" {
		t.Fatalf("response = %#v", resp)
	}
	if _, ok := disp.last.(SaveRequestMsg); !ok {
		t.Fatalf("got %#v, want SaveRequestMsg", disp.last)
	}
}

func TestDispatchLyricsAndHistory(t *testing.T) {
	for _, request := range []Request{{Cmd: "lyrics"}, {Cmd: "history", Limit: 25}, {Cmd: "history.clear"}} {
		disp := &captureDispatcher{autoReply: Response{OK: true}}
		resp := newTestServer(disp).dispatch(request)
		if !resp.OK {
			t.Fatalf("%s: OK = false, err=%q", request.Cmd, resp.Error)
		}
		if request.Cmd == "history.clear" {
			got, ok := disp.last.(HistoryRequestMsg)
			if !ok || got.Op != request.Cmd {
				t.Fatalf("got %#v, want history.clear", disp.last)
			}
		}
	}
}

func TestDispatchUnknownCmd(t *testing.T) {
	s := newTestServer(&captureDispatcher{})
	resp := s.dispatch(Request{Cmd: "dostuff"})
	if resp.OK {
		t.Error("unknown cmd should return !OK")
	}
	if resp.Error == "" {
		t.Error("error should be set for unknown cmd")
	}
}

// deadDispatcher never replies on the Reply channel — triggers the timeout branch.
type deadDispatcher struct{}

func (deadDispatcher) Send(msg any) {}

func TestDispatchReplyTimeout(t *testing.T) {
	// Replace time.After with a fast timeout to keep this test cheap.
	// The production code uses 3s; a test using real time would be slow,
	// so we exercise the shutdown-path via s.done instead.
	s := newTestServer(deadDispatcher{})
	close(s.done) // simulate server shutting down

	tests := []Request{
		{Cmd: "load", Playlist: "main"},
		{Cmd: "theme", Name: "dracula"},
		{Cmd: "vis", Name: "bars"},
		{Cmd: "shuffle", Name: "on"},
		{Cmd: "repeat", Name: "all"},
		{Cmd: "mono", Name: "toggle"},
		{Cmd: "speed", Value: 1.0},
		{Cmd: "eq", Name: "rock"},
		{Cmd: "device", Name: "list"},
		{Cmd: "status"},
	}
	for _, r := range tests {
		t.Run(r.Cmd, func(t *testing.T) {
			resp := s.dispatch(r)
			if resp.OK {
				t.Errorf("%s should return !OK during shutdown", r.Cmd)
			}
			if resp.Error == "" {
				t.Errorf("%s should set error message during shutdown", r.Cmd)
			}
		})
	}
}
