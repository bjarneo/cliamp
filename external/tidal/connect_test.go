package tidal

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/player"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"golang.org/x/net/dns/dnsmessage"
)

type connectFixture struct {
	server      *httptest.Server
	mu          sync.Mutex
	commands    []string
	reject      string
	hold        string
	startVolume any
}

func newConnectFixture(t *testing.T) *connectFixture {
	t.Helper()
	f := &connectFixture{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tracks/42" {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 42, "title": "Test track", "duration": 60, "artists": []any{map[string]any{"name": "Artist"}}})
			return
		}
		if r.URL.Path == "/queues" {
			if r.Header.Get("Authorization") != "Bearer token" {
				w.WriteHeader(401)
				return
			}
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["repeat_mode"] != "off" {
				t.Error("receiver queue must not repeat")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "test-queue", "items": []any{map[string]any{"id": "test-item"}}})
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		state := "PLAYING"
		position := int64(0)
		send := func(m any) { _ = wsjson.Write(r.Context(), conn, m) }
		status := func() {
			send(map[string]any{"command": "notifyPlayerStatusChanged", "playerState": state, "duration": 60000, "progress": position})
		}
		for {
			var m map[string]any
			if wsjson.Read(r.Context(), conn, &m) != nil {
				return
			}
			command, _ := m["command"].(string)
			f.mu.Lock()
			f.commands = append(f.commands, command)
			reject, hold := f.reject, f.hold
			f.mu.Unlock()
			if command == "startSession" {
				send(map[string]any{"command": "notifySessionStarted", "sessionId": 123})
				send(map[string]any{"command": "notifyDeviceStatusChanged", "volume": f.startVolume})
				status()
				continue
			}
			if command == hold {
				continue
			}
			code := 0
			if command == reject {
				code = 7
			}
			send(map[string]any{"command": "notifyRequestResult", "requestId": m["requestId"], "resultCode": code})
			if code != 0 {
				continue
			}
			switch command {
			case "loadCloudQueue":
				if m["autoplay"] != false {
					t.Error("handoff must prepare paused")
				}
				send(map[string]any{"command": "notifyQueueChanged", "queueInfo": m["queueInfo"]})
				send(map[string]any{"command": "notifyMediaChanged", "mediaInfo": m["currentMediaInfo"]})
				state = "PAUSED"
				position = 0
				status()
			case "seek":
				position = int64(m["position"].(float64))
				status()
			case "play":
				state = "PLAYING"
				status()
			case "pause":
				state = "PAUSED"
				status()
			case "stop":
				state = "STOPPED"
				status()
			case "setVolume":
				send(map[string]any{"command": "notifyDeviceStatusChanged", "volume": map[string]any{"level": m["level"], "mute": false}})
			}
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}
func (f *connectFixture) open(t *testing.T) *connectSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s, err := openConnect(ctx, "ws"+strings.TrimPrefix(f.server.URL, "http"), f.server.Client(), "42")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.close)
	return s
}
func TestConnectSessionCommandResults(t *testing.T) {
	for _, name := range []string{"success", "rejection", "timeout"} {
		t.Run(name, func(t *testing.T) {
			f := newConnectFixture(t)
			if name == "rejection" {
				f.reject = "pause"
			}
			if name == "timeout" {
				f.hold = "pause"
			}
			s := f.open(t)
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			err := s.command(ctx, "pause", nil)
			if name == "success" && err != nil {
				t.Fatal(err)
			}
			if name == "rejection" && (err == nil || !strings.Contains(err.Error(), "(7)")) {
				t.Fatalf("rejection=%v", err)
			}
			if name == "timeout" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("timeout=%v", err)
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if len(s.pending) != 0 {
				t.Fatal("pending command leaked")
			}
		})
	}
}
func TestConnectLoadPausedAtPosition(t *testing.T) {
	for _, paused := range []bool{false, true} {
		t.Run(map[bool]string{false: "playing", true: "paused"}[paused], func(t *testing.T) {
			f := newConnectFixture(t)
			s := f.open(t)
			c := testClient(f.server)
			c.queueURL = f.server.URL + "/queues"
			p := New("lossless", "", "")
			p.client = c
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			queue, err := p.loadConnectTrack(ctx, s, TrackURIPrefix+"42", 12*time.Second, paused)
			if err != nil {
				t.Fatal(err)
			}
			if queue != "test-queue" {
				t.Fatalf("queue=%q", queue)
			}
			st := s.snapshot()
			if st.Position != 12*time.Second || st.State == "PAUSED" != paused {
				t.Fatalf("state=%+v", st)
			}
		})
	}
}
func TestConnectQueueErrorsDoNotExposeTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte("secret-token"))
	}))
	defer srv.Close()
	c := testClient(srv)
	_, err := c.createConnectQueue(context.Background(), srv.URL, "42")
	if err == nil || !strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("error=%v", err)
	}
}
func TestConnectQueueRejectsIncompleteResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"id":"q","items":[]}`)) }))
	defer srv.Close()
	_, err := testClient(srv).createConnectQueue(context.Background(), srv.URL, "42")
	if err == nil {
		t.Fatal("accepted missing queue item")
	}
}
func TestConnectCompletionAndTakeover(t *testing.T) {
	cases := []struct {
		name, state                                     string
		position                                        time.Duration
		pause, external, disconnected, drained, playing bool
	}{
		{name: "natural completion", state: "PAUSED", position: time.Minute, drained: true, playing: true},
		{name: "near end pause", state: "PAUSED", position: time.Minute - time.Millisecond, playing: true},
		{name: "explicit pause at end", state: "PAUSED", position: time.Minute, pause: true, playing: true},
		{name: "external takeover", state: "PLAYING", external: true},
		{name: "disconnected", state: "PLAYING", disconnected: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			st := connectState{QueueID: "q", ItemID: "i", TrackID: "42", State: tt.state, Position: tt.position, Duration: time.Minute}
			if tt.external {
				st.QueueID = "external"
			}
			if tt.disconnected {
				st.Err = errors.New("disconnected")
			}
			o := &ConnectOutput{session: &connectSession{state: st}, queue: "q", path: TrackURIPrefix + "42", pausedIntent: tt.pause}
			if o.Drained() != tt.drained || o.IsPlaying() != tt.playing {
				t.Fatalf("drained=%v playing=%v", o.Drained(), o.IsPlaying())
			}
			if tt.drained && o.IsPaused() {
				t.Fatal("completion hidden from tickDrain")
			}
			if (tt.external || tt.disconnected) && o.StreamErr() == nil {
				t.Fatal("missing remote failure")
			}
		})
	}
}

type connectLocalFake struct {
	player.Engine
	mu              sync.Mutex
	closed          bool
	gen             uint64
	plays           int
	playing, paused bool
	position        time.Duration
	playErr         error
	onPlay          func()
}

func (f *connectLocalFake) VolumeMin() float64             { return -50 }
func (f *connectLocalFake) Close()                         { f.mu.Lock(); f.closed = true; f.mu.Unlock() }
func (f *connectLocalFake) SetPlaybackGeneration(g uint64) { f.mu.Lock(); f.gen = g; f.mu.Unlock() }
func (f *connectLocalFake) PlayAtForGeneration(_ string, _ time.Duration, position time.Duration, _ uint64) error {
	if f.onPlay != nil {
		f.onPlay()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.plays++
	if f.playErr == nil {
		f.playing = true
		f.paused = false
		f.position = position
	}
	return f.playErr
}
func (f *connectLocalFake) IsPlaying() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.playing }
func (f *connectLocalFake) IsPaused() bool  { f.mu.Lock(); defer f.mu.Unlock(); return f.paused }
func (f *connectLocalFake) TogglePause()    { f.mu.Lock(); defer f.mu.Unlock(); f.paused = !f.paused }
func (f *connectLocalFake) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.playing = false
	f.paused = false
}
func TestConnectWorkerOrderingAndStalePlay(t *testing.T) {
	local := &connectLocalFake{}
	o := NewConnectOutput(local, nil)
	defer o.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	o.async(func(context.Context) error { close(entered); <-release; return nil })
	<-entered
	o.SetPlaybackGeneration(2)
	var mu sync.Mutex
	order := []int{}
	for i := range 3 {
		o.async(func(context.Context) error { mu.Lock(); order = append(order, i); mu.Unlock(); return nil })
	}
	close(release)
	err := o.PlayAtForGeneration("local.mp3", time.Minute, 0, 1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("stale play=%v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 || order[0] != 0 || order[1] != 1 || order[2] != 2 {
		t.Fatalf("order=%v", order)
	}
	local.mu.Lock()
	defer local.mu.Unlock()
	if local.plays != 0 {
		t.Fatal("stale local play committed")
	}
}

func TestConnectCanceledLoadPausesReceiver(t *testing.T) {
	f := newConnectFixture(t)
	f.hold = "play"
	s := f.open(t)
	c := testClient(f.server)
	c.queueURL = f.server.URL + "/queues"
	p := New("lossless", "", "")
	p.client = c
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := p.loadConnectTrack(ctx, s, TrackURIPrefix+"42", 0, false)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("load error = %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.commands[len(f.commands)-1] != "pause" {
		t.Fatalf("canceled load left receiver running: %v", f.commands)
	}
}

func TestConnectRepeatedSeekAndPause(t *testing.T) {
	f := newConnectFixture(t)
	s := f.open(t)
	o := NewConnectOutput(&connectLocalFake{}, nil)
	defer o.Close()
	s.mu.Lock()
	s.state.QueueID, s.state.TrackID = "q", "42"
	s.mu.Unlock()
	o.mu.Lock()
	o.session, o.queue, o.path = s, "q", TrackURIPrefix+"42"
	o.mu.Unlock()
	for _, target := range []time.Duration{20 * time.Second, 45 * time.Second, 5 * time.Second} {
		if err := o.Seek(target - o.Position()); err != nil {
			t.Fatal(err)
		}
		if o.Position() != target {
			t.Fatalf("position = %v, want %v", o.Position(), target)
		}
	}
	// Rapid toggles must observe the preceding receiver update, not the same
	// stale snapshot twice.
	o.TogglePause()
	o.TogglePause()
	if err := o.submit(context.Background(), func(context.Context) error { return nil }, true); err != nil {
		t.Fatal(err)
	}
	if o.IsPaused() || !o.IsPlaying() {
		t.Fatal("two toggles did not resume playback")
	}
}

func TestConnectDiscoverySplitRecords(t *testing.T) {
	name := func(s string) dnsmessage.Name {
		n, err := dnsmessage.NewName(s)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	service := "receiver." + connectService
	host := "receiver.local."
	resource := func(n string, typ dnsmessage.Type, body dnsmessage.ResourceBody) dnsmessage.Resource {
		return dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: name(n), Type: typ, Class: dnsmessage.ClassINET, TTL: 120}, Body: body}
	}
	records := []dnsmessage.Resource{
		resource(service, dnsmessage.TypeTXT, &dnsmessage.TXTResource{TXT: []string{"fn=Living room"}}),
		resource(connectService, dnsmessage.TypePTR, &dnsmessage.PTRResource{PTR: name(service)}),
		resource(service, dnsmessage.TypeSRV, &dnsmessage.SRVResource{Port: 2019, Target: name(host)}),
		resource(host, dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{192, 168, 2, 12}}),
	}
	for _, reverse := range []bool{false, true} {
		d := newConnectDiscovery()
		d.consume([]byte("malformed"))
		for i := range records {
			if reverse {
				i = len(records) - 1 - i
			}
			message := dnsmessage.Message{Header: dnsmessage.Header{Response: true}, Additionals: []dnsmessage.Resource{records[i]}}
			packet, err := message.Pack()
			if err != nil {
				t.Fatal(err)
			}
			d.consume(packet)
		}
		devices := d.devices()
		if len(devices) != 1 || devices[0].Name != "tidal:192.168.2.12:2019" || devices[0].Description != "Tidal Connect: Living room" {
			t.Fatalf("devices = %+v", devices)
		}
	}
}

func TestConnectReturnToLocal(t *testing.T) {
	for _, name := range []string{"playing", "paused", "failed", "canceled"} {
		t.Run(name, func(t *testing.T) {
			f := newConnectFixture(t)
			s := f.open(t)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := s.wait(ctx, func(st connectState) bool { return st.State == "PLAYING" }); err != nil {
				t.Fatal(err)
			}
			if name == "paused" {
				if err := s.command(ctx, "pause", nil); err != nil {
					t.Fatal(err)
				}
				if err := s.wait(ctx, func(st connectState) bool { return st.State == "PAUSED" }); err != nil {
					t.Fatal(err)
				}
			}
			s.mu.Lock()
			s.state.QueueID, s.state.TrackID = "q", "42"
			s.state.Position = 12 * time.Second
			s.mu.Unlock()
			local := &connectLocalFake{}
			o := NewConnectOutput(local, nil)
			defer o.Close()
			o.mu.Lock()
			o.session, o.queue, o.path = s, "q", TrackURIPrefix+"42"
			o.mu.Unlock()
			o.SetPlaybackGeneration(1)
			if name == "failed" {
				local.playErr = errors.New("local decoder failed")
			}
			if name == "canceled" {
				local.onPlay = func() { o.SetPlaybackGeneration(2) }
			}
			err := o.SwitchOutput(ctx, "local", "")
			if name == "failed" || name == "canceled" {
				if err == nil || !o.RemoteOutput() || local.IsPlaying() {
					t.Fatalf("failed transfer committed: err=%v remote=%v", err, o.RemoteOutput())
				}
				if name == "failed" {
					if err := s.wait(ctx, func(st connectState) bool { return st.State == "PLAYING" }); err != nil {
						t.Fatal(err)
					}
					if !o.IsPlaying() || o.StreamErr() != nil {
						t.Fatal("rollback did not restore playback")
					}
				}
				return
			}
			if err != nil || o.RemoteOutput() || !local.IsPlaying() || local.IsPaused() != (name == "paused") {
				t.Fatalf("handoff: err=%v remote=%v paused=%v", err, o.RemoteOutput(), local.IsPaused())
			}
			local.mu.Lock()
			defer local.mu.Unlock()
			if local.position != 12*time.Second {
				t.Fatalf("handoff position = %v", local.position)
			}
		})
	}
}

func TestConnectTLSRejectsUntrustedCertificates(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()
	config, err := connectTLS()
	if err != nil {
		t.Fatal(err)
	}
	for _, certs := range [][]*x509.Certificate{nil, {srv.Certificate()}} {
		if err := config.VerifyConnection(tls.ConnectionState{PeerCertificates: certs}); err == nil {
			t.Fatal("accepted an untrusted receiver")
		}
	}
}

func TestConnectControlFailuresKeepPlaybackState(t *testing.T) {
	for _, control := range []string{"volume", "pause", "timeout", "busy"} {
		t.Run(control, func(t *testing.T) {
			f := newConnectFixture(t)
			f.reject = "setVolume"
			if control == "pause" {
				f.reject = "pause"
			}
			s := f.open(t)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := s.wait(ctx, func(st connectState) bool { return st.State == "PLAYING" }); err != nil {
				t.Fatal(err)
			}
			s.mu.Lock()
			s.state.QueueID, s.state.TrackID = "q", "42"
			s.mu.Unlock()
			o := NewConnectOutput(&connectLocalFake{}, nil)
			defer o.Close()
			o.mu.Lock()
			o.session, o.queue, o.path = s, "q", TrackURIPrefix+"42"
			o.mu.Unlock()
			switch control {
			case "volume":
				o.SetVolume(-6)
			case "pause":
				o.TogglePause()
			case "timeout":
				o.async(func(ctx context.Context) error {
					ctx, cancel := context.WithTimeout(ctx, time.Millisecond)
					defer cancel()
					return s.wait(ctx, func(st connectState) bool { return st.State == "PAUSED" })
				})
			case "busy":
				entered, release, drained := make(chan struct{}), make(chan struct{}), make(chan struct{})
				o.async(func(context.Context) error { close(entered); <-release; return nil })
				<-entered
				for i := range cap(o.work) {
					o.async(func(context.Context) error {
						if i == cap(o.work)-1 {
							close(drained)
						}
						return nil
					})
				}
				o.SetVolume(-6) // The queue is full; this must not fail the session.
				close(release)
				<-drained
			}
			if err := o.submit(ctx, func(context.Context) error { return nil }, true); err != nil {
				t.Fatal(err)
			}
			if !o.IsPlaying() || o.StreamErr() != nil {
				t.Fatalf("control failure stopped playback: %v", o.StreamErr())
			}
			// A rejected pause must also restore its intent, allowing the next
			// natural completion to advance the queue.
			s.mu.Lock()
			s.state.State, s.state.Position = "PAUSED", s.state.Duration
			s.mu.Unlock()
			if !o.Drained() {
				t.Fatal("control failure blocked queue advancement")
			}
		})
	}
}

func TestConnectVolumeKnown(t *testing.T) {
	for _, tt := range []struct {
		name   string
		volume any
		known  bool
		want   float64
	}{
		{"missing volume", nil, false, -50},
		{"missing level", map[string]any{"mute": false}, false, -50},
		{"negative level", map[string]any{"level": -0.1}, false, -50},
		{"above maximum", map[string]any{"level": 1.1}, false, -50},
		{"zero", map[string]any{"level": 0}, true, -50},
		{"half", map[string]any{"level": 0.5}, true, 20 * math.Log10(0.5)},
		{"maximum", map[string]any{"level": 1}, true, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newConnectFixture(t)
			f.startVolume = tt.volume
			s := f.open(t)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := s.wait(ctx, func(st connectState) bool { return st.State == "PLAYING" }); err != nil {
				t.Fatal(err)
			}
			o := &ConnectOutput{Engine: &connectLocalFake{}, session: s}
			if o.VolumeKnown() != tt.known || math.Abs(o.Volume()-tt.want) > 1e-6 {
				t.Fatalf("known=%v volume=%v", o.VolumeKnown(), o.Volume())
			}
			// Absolute settings still work before the initial volume report.
			if err := s.command(ctx, "setVolume", map[string]any{"level": 0.25}); err != nil {
				t.Fatal(err)
			}
			if err := s.wait(ctx, func(st connectState) bool { return st.VolumeKnown && st.Volume == 0.25 }); err != nil {
				t.Fatal(err)
			}
			if !o.VolumeKnown() {
				t.Fatal("volume did not become known")
			}
		})
	}
}
