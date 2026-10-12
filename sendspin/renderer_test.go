package sendspin

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sendspin/sendspin-go/pkg/protocol"
	"github.com/gorilla/websocket"
)

// fakeServer is a minimal Sendspin server: it completes the handshake,
// answers clock sync, records client messages and lets the test push
// messages to the client.
type fakeServer struct {
	t     *testing.T
	srv   *httptest.Server
	ready chan struct{}

	mu     sync.Mutex
	conn   *websocket.Conn
	hello  map[string]any
	states []map[string]any
}

func newFakeServer(t *testing.T) *fakeServer {
	f := &fakeServer{t: t, ready: make(chan struct{})}
	up := websocket.Upgrader{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sendspin" {
			http.NotFound(w, r)
			return
		}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		f.serve(c)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeServer) addr() string { return strings.TrimPrefix(f.srv.URL, "http://") }

func (f *fakeServer) serve(c *websocket.Conn) {
	defer c.Close()
	var hello struct {
		Type    string         `json:"type"`
		Payload map[string]any `json:"payload"`
	}
	if err := c.ReadJSON(&hello); err != nil || hello.Type != "client/hello" {
		return
	}
	f.mu.Lock()
	f.conn = c
	f.hello = hello.Payload
	f.mu.Unlock()
	f.send("server/hello", map[string]any{"server_id": "fake", "name": "fake", "version": 1,
		"active_roles": []string{"player@v1", "metadata@v1"}})
	readyOnce := sync.OnceFunc(func() { close(f.ready) })
	for {
		var msg struct {
			Type    string         `json:"type"`
			Payload map[string]any `json:"payload"`
		}
		if err := c.ReadJSON(&msg); err != nil {
			return
		}
		switch msg.Type {
		case "client/time":
			now := time.Now().UnixMicro()
			f.send("server/time", map[string]any{
				"client_transmitted": msg.Payload["client_transmitted"],
				"server_received":    now, "server_transmitted": now,
			})
			readyOnce()
		case "client/state":
			if pl, ok := msg.Payload["player"].(map[string]any); ok {
				f.mu.Lock()
				f.states = append(f.states, pl)
				f.mu.Unlock()
			}
		}
	}
}

func (f *fakeServer) send(typ string, payload any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.conn.WriteJSON(map[string]any{"type": typ, "payload": payload}); err != nil {
		f.t.Logf("send %s: %v", typ, err) // expected after the test drops the connection
	}
}

func (f *fakeServer) sendChunk(ts int64, pcm []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.conn.WriteMessage(websocket.BinaryMessage, protocol.CreateAudioChunk(ts, pcm)); err != nil {
		f.t.Errorf("send chunk: %v", err)
	}
}

func startRenderer(t *testing.T, addr string) *Renderer {
	t.Helper()
	r, err := Start(Config{Addr: addr, Name: "test", ClientIDPath: filepath.Join(t.TempDir(), "id")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestRendererPlaysScheduledPCM drives a whole stream: the renderer asks for
// 48 kHz PCM, syncs its clock, applies Music Assistant's volume, plays the
// chunks when their timestamps come due and reports the metadata.
func TestRendererPlaysScheduledPCM(t *testing.T) {
	f := newFakeServer(t)
	r := startRenderer(t, f.addr())
	select {
	case <-f.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("renderer never synced its clock")
	}

	f.mu.Lock()
	hello := f.hello
	f.mu.Unlock()
	raw, _ := json.Marshal(hello)
	var got struct {
		Roles   []string `json:"supported_roles"`
		Support struct {
			Formats []protocol.AudioFormat `json:"supported_formats"`
		} `json:"player@v1_support"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Roles, []string{"player@v1", "metadata@v1"}) {
		t.Errorf("roles = %v", got.Roles)
	}
	want48k := []protocol.AudioFormat{{Codec: "pcm", Channels: 2, SampleRate: 48000, BitDepth: 16}}
	if !reflect.DeepEqual(got.Support.Formats, want48k) {
		t.Errorf("formats = %+v", got.Support.Formats)
	}

	stream, format, _, err := r.NewStreamer(URI)
	if err != nil || format.SampleRate != SampleRate {
		t.Fatalf("NewStreamer: %v %v", format, err)
	}
	defer stream.Close()
	if !r.Streaming() {
		t.Fatal("Streaming() = false with an open streamer")
	}

	f.send("server/command", map[string]any{"player": map[string]any{"command": "volume", "volume": 50}})
	f.send("stream/start", map[string]any{"player": map[string]any{"codec": "pcm", "sample_rate": 48000, "channels": 2, "bit_depth": 16}})
	f.send("server/state", map[string]any{"metadata": map[string]any{
		"timestamp": 0, "title": "Song", "artist": "Band",
		"progress": map[string]any{"track_progress": 60000, "track_duration": 200000, "playback_speed": 1000},
	}})

	const level = 16000
	pcm := make([]byte, 960*frameBytes) // one 20 ms chunk
	for i := 0; i < len(pcm); i += 2 {
		binary.LittleEndian.PutUint16(pcm[i:], uint16(level))
	}
	start := time.Now().Add(400 * time.Millisecond).UnixMicro()
	for i := range 25 {
		f.sendChunk(start+int64(i)*20_000, pcm)
	}

	want := level * 0.5 / 32768
	buf := make([][2]float64, 480)
	var heard float64
	waitFor(t, "audio", func() bool {
		n, ok := stream.Stream(buf)
		if !ok {
			t.Fatal("stream ended")
		}
		for _, s := range buf[:n] {
			if s[0] != 0 {
				heard = s[0]
				return true
			}
		}
		return false
	})
	if math.Abs(heard-want) > 0.001 {
		t.Errorf("sample = %.4f, want %.4f (volume 50)", heard, want)
	}
	if now := r.NowPlaying(); now.Title != "Song" || now.Artist != "Band" || now.DurationSecs != 200 {
		t.Errorf("NowPlaying = %+v", now)
	}
	// Music Assistant said 60 s in; the stream itself has played under 1 s.
	if pos := r.Position(); pos < 60*time.Second || pos > 70*time.Second {
		t.Errorf("Position = %v, want just past the reported 60s", pos)
	}
	f.mu.Lock()
	acked := len(f.states) > 0 && f.states[len(f.states)-1]["volume"] == float64(50)
	f.mu.Unlock()
	if !acked {
		t.Error("volume change was not reported back in client/state")
	}

	stream.Close()
	if r.Streaming() {
		t.Error("Streaming() = true after the streamer closed")
	}
}

// TestStreamerDropsAudioOfReplacedStream: PCM queued for a stream that has
// since been stopped or replaced, such as audio queued while the output was
// paused, must not play ahead of the current stream.
func TestStreamerDropsAudioOfReplacedStream(t *testing.T) {
	r := &Renderer{pcm: make(chan []byte, 8)}
	s, _, _, _ := r.NewStreamer(URI)
	chunk := func(epoch uint64, v int16) []byte {
		b := make([]byte, 8+SampleRate/10*frameBytes) // 100 ms
		binary.LittleEndian.PutUint64(b, epoch)
		for i := 8; i < len(b); i += 2 {
			binary.LittleEndian.PutUint16(b[i:], uint16(v))
		}
		return b
	}
	r.pcm <- chunk(0, 1000) // queued while the output was paused
	r.epoch.Store(2)        // stopped (1), then resumed as a new stream (2)
	buf := make([][2]float64, 64)
	if s.Stream(buf); buf[0][0] != 0 {
		t.Fatalf("played %v from the stopped stream, want silence", buf[0][0])
	}
	r.pcm <- chunk(2, 2000)
	if n, _ := s.Stream(buf); n != len(buf) || buf[0][0] != 2000.0/32768 {
		t.Fatalf("first sample = %v, want the current stream's audio", buf[0][0])
	}
}

func TestClientIDIsPersisted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "id")
	a, err := loadClientID(path)
	if err != nil || !strings.HasPrefix(a, "cliamp-") {
		t.Fatalf("first id = %q, %v", a, err)
	}
	b, err := loadClientID(path)
	if err != nil || b != a {
		t.Fatalf("second id = %q, want %q (%v)", b, a, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("id file mode = %v, %v", info.Mode(), err)
	}
}

func TestRendererReconnects(t *testing.T) {
	f := newFakeServer(t)
	r := startRenderer(t, f.addr())
	waitFor(t, "first connection", r.Connected)
	f.mu.Lock()
	_ = f.conn.Close()
	f.mu.Unlock()
	waitFor(t, "disconnect", func() bool { return !r.Connected() })
	waitFor(t, "reconnection", r.Connected)
}
