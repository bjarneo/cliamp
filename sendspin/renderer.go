// Package sendspin makes cliamp a Sendspin player. Music Assistant streams
// timestamped PCM to it, and cliamp plays that PCM through its own audio
// engine, so the equalizer, visualizer and output device apply.
//
// Only the pure-Go protocol and clock-sync packages of sendspin-go are used.
// Its high-level Player links libopus and miniaudio through cgo, and cliamp
// needs neither: the renderer asks for PCM and writes into beep.
package sendspin

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	mrand "math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sendspin/sendspin-go/pkg/protocol"
	sssync "github.com/Sendspin/sendspin-go/pkg/sync"
	"github.com/gorilla/websocket"

	"github.com/bjarneo/cliamp/applog"
)

const (
	// SampleRate is the only rate the renderer accepts. Music Assistant
	// resamples to it, so the beep format never changes mid-stream.
	SampleRate = 48000
	frameBytes = 4 // stereo s16le

	playWindow = 50 * time.Millisecond // chunks inside ±window of their play time are played
	schedTick  = 10 * time.Millisecond

	syncBurst       = 8
	syncInterval    = 10 * time.Second
	syncReplyWait   = 500 * time.Millisecond
	metadataTick    = 50 * time.Millisecond
	dialTimeout     = 10 * time.Second
	reconnectFirst  = 500 * time.Millisecond
	reconnectMax    = 30 * time.Second
	pcmQueueChunks  = 256 // ~5 s of 20 ms chunks between the scheduler and beep
	bufferCapacity  = 1 << 20
	defaultPlayName = "cliamp"
)

// supportedFormats lists what the renderer accepts. PCM needs no decoder, and
// one fixed format means a streamer opened before stream/start is correct.
var supportedFormats = []protocol.AudioFormat{
	{Codec: "pcm", Channels: 2, SampleRate: SampleRate, BitDepth: 16},
}

// Config configures a Renderer.
type Config struct {
	// Addr is the Sendspin server, host:port. Music Assistant listens on 8927.
	Addr string
	// Name is the player name shown in Music Assistant.
	Name string
	// ClientIDPath persists the client id so Music Assistant keeps the same
	// player (and its settings) across restarts.
	ClientIDPath string
	// OnStream runs on stream/start while no sendspin: streamer owns the
	// output, so the caller can start one. It runs on the renderer's
	// goroutine and must not block.
	OnStream func()
	// SoftwareVersion is reported in client/hello.
	SoftwareVersion string
}

// TrackInfo is the now-playing snapshot from Music Assistant.
type TrackInfo struct {
	Title        string
	Artist       string
	Album        string
	ArtworkURL   string
	DurationSecs int
}

// Renderer is a Sendspin player that reconnects until Close.
type Renderer struct {
	cfg      Config
	clientID string
	pcm      chan []byte

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu        sync.Mutex
	now       TrackInfo
	progress  progressAnchor
	connected bool

	volume atomic.Int32 // 0-100, set by Music Assistant
	muted  atomic.Bool
	pos    atomic.Int64 // frames played since the last stream/start
	owner  atomic.Int64 // generation of the streamer that owns output, 0 = none
	gen    atomic.Int64
	// epoch changes on every stream/start, stream/clear and stop so a
	// streamer discards PCM queued for the previous stream.
	epoch atomic.Int64
}

// progressAnchor is the track position Music Assistant reported, and when.
type progressAnchor struct {
	set   bool
	pos   time.Duration // track position at local time at
	at    time.Time
	speed float64 // 1 = normal, 0 = paused
}

// Start resolves the client id and connects in the background. It does not
// wait for the network: a missing server only delays playback.
func Start(cfg Config) (*Renderer, error) {
	if cfg.Addr == "" {
		return nil, errors.New("sendspin: server address is required")
	}
	if cfg.Name == "" {
		cfg.Name = defaultPlayName
	}
	id, err := loadClientID(cfg.ClientIDPath)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &Renderer{
		cfg:      cfg,
		clientID: id,
		pcm:      make(chan []byte, pcmQueueChunks),
		ctx:      ctx,
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	r.volume.Store(100)
	go r.run()
	return r, nil
}

// ClientID is the id this renderer announces. Music Assistant lists it as the
// Sendspin output protocol id of the player that wraps the renderer.
func (r *Renderer) ClientID() string { return r.clientID }

// Name is the player name announced to Music Assistant.
func (r *Renderer) Name() string { return r.cfg.Name }

// Connected reports whether the server connection is up.
func (r *Renderer) Connected() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.connected
}

// NowPlaying returns the latest metadata from the server.
func (r *Renderer) NowPlaying() TrackInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.now
}

// Position is the track position: Music Assistant's last reported progress,
// advanced by the time since. It stays right across a resume or a seek in
// another app, which both restart the stream. Without progress metadata it is
// the time played since the stream started.
func (r *Renderer) Position() time.Duration {
	r.mu.Lock()
	p, dur := r.progress, time.Duration(r.now.DurationSecs)*time.Second
	r.mu.Unlock()
	if !p.set {
		return time.Duration(r.pos.Load()) * time.Second / SampleRate
	}
	pos := p.pos + time.Duration(float64(time.Since(p.at))*p.speed)
	if dur > 0 {
		pos = min(pos, dur)
	}
	return max(pos, 0)
}

// Streaming reports whether a sendspin: streamer currently owns the output.
func (r *Renderer) Streaming() bool { return r.owner.Load() != 0 }

// Close sends goodbye and stops reconnecting.
func (r *Renderer) Close() error {
	r.cancel()
	<-r.done
	return nil
}

func (r *Renderer) setConnected(v bool) {
	r.mu.Lock()
	r.connected = v
	r.mu.Unlock()
}

func (r *Renderer) run() {
	defer close(r.done)
	delay := reconnectFirst
	for {
		connected, err := r.session()
		r.setConnected(false)
		if r.ctx.Err() != nil {
			return
		}
		if connected {
			delay = reconnectFirst
		}
		applog.Warn("sendspin: %v; reconnecting in %v", err, delay)
		select {
		case <-r.ctx.Done():
			return
		case <-time.After(jitter(delay)):
		}
		delay = min(delay*2, reconnectMax)
	}
}

func jitter(d time.Duration) time.Duration {
	return time.Duration(float64(d) * (0.8 + 0.4*mrand.Float64()))
}

func (r *Renderer) dial() (*protocol.Client, error) {
	ctx, cancel := context.WithTimeout(r.ctx, dialTimeout)
	defer cancel()
	u := url.URL{Scheme: "ws", Host: r.cfg.Addr, Path: "/sendspin"}
	d := websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: dialTimeout}
	conn, _, err := d.DialContext(ctx, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", r.cfg.Addr, err)
	}
	c := protocol.NewClientFromConn(protocol.Config{
		ServerAddr: r.cfg.Addr,
		ClientID:   r.clientID,
		Name:       r.cfg.Name,
		Version:    1,
		DeviceInfo: protocol.DeviceInfo{
			ProductName:     "cliamp",
			Manufacturer:    "cliamp",
			SoftwareVersion: r.cfg.SoftwareVersion,
		},
		PlayerV1Support: protocol.PlayerV1Support{
			SupportedFormats:  supportedFormats,
			BufferCapacity:    bufferCapacity,
			SupportedCommands: []string{"volume", "mute"},
		},
		SupportedRoles: []string{"player@v1", "metadata@v1"},
	}, conn)
	if err := c.Start(); err != nil {
		return nil, fmt.Errorf("handshake with %s: %w", r.cfg.Addr, err)
	}
	return c, nil
}

// session runs one connection. connected reports whether the handshake
// succeeded, so the caller can reset its backoff.
func (r *Renderer) session() (connected bool, err error) {
	c, err := r.dial()
	if err != nil {
		return false, err
	}
	defer c.Close()
	r.setConnected(true)
	applog.Info("sendspin: connected to %s as %q", r.cfg.Addr, r.cfg.Name)

	clock := sssync.NewClockSync()
	syncOnce(r.ctx, c, clock)

	ctx, cancel := context.WithCancel(r.ctx)
	defer cancel()
	go func() {
		t := time.NewTicker(syncInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				syncOnce(ctx, c, clock)
			}
		}
	}()

	s := &session{r: r, c: c, clock: clock}
	sched := time.NewTicker(schedTick)
	defer sched.Stop()
	meta := time.NewTicker(metadataTick)
	defer meta.Stop()
	for {
		select {
		case <-r.ctx.Done():
			_ = c.SendGoodbye("shutdown")
			return true, nil
		case <-c.Done():
			return true, errors.New("server closed the connection")
		case start := <-c.StreamStart:
			s.start(start)
		case <-c.StreamClear:
			s.clear()
		case <-c.StreamEnd:
			s.clear()
		case g := <-c.GroupUpdate:
			if g.PlaybackState != nil && (*g.PlaybackState == "paused" || *g.PlaybackState == "stopped") {
				s.clear()
			}
		case chunk := <-c.AudioChunks:
			s.queue(chunk)
		case st := <-c.ServerState:
			if st.Metadata != nil {
				s.metadata(st.Metadata)
			}
		case cmd := <-c.ControlMsgs:
			s.command(cmd)
		case <-sched.C:
			s.pump()
		case <-meta.C:
			s.applyDueMetadata()
		}
	}
}

// syncOnce sends a burst of client/time messages and feeds the reply with
// the lowest round trip to the clock filter.
func syncOnce(ctx context.Context, c *protocol.Client, clock *sssync.ClockSync) {
	for {
		select {
		case <-c.TimeSyncResp:
			continue
		default:
		}
		break
	}
	best := int64(math.MaxInt64)
	var t1, t2, t3, t4 int64
	for range syncBurst {
		if err := c.SendTimeSync(time.Now().UnixMicro()); err != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(syncReplyWait):
			continue
		case resp := <-c.TimeSyncResp:
			now := time.Now().UnixMicro()
			rtt := (now - resp.ClientTransmitted) - (resp.ServerTransmitted - resp.ServerReceived)
			if rtt < best {
				best, t1, t2, t3, t4 = rtt, resp.ClientTransmitted, resp.ServerReceived, resp.ServerTransmitted, now
			}
		}
	}
	if best != math.MaxInt64 {
		clock.ProcessSyncResponse(t1, t2, t3, t4)
	}
}

// session is the per-connection state. Only the session goroutine touches it.
type session struct {
	r     *Renderer
	c     *protocol.Client
	clock *sssync.ClockSync

	// rejected is set by a stream/start in a format the renderer did not ask
	// for; its audio is dropped until the next stream/start.
	rejected bool
	queued   []protocol.AudioChunk // ascending timestamps
	pending  []*protocol.MetadataState
	meta     TrackInfo
}

// start begins a new stream. Chunks that were queued are kept: the client
// delivers stream/start and audio on separate channels, so the first chunks
// of this stream may already be queued. Chunks of an earlier stream are past
// their play time and are dropped by pump.
func (s *session) start(start protocol.StreamStart) {
	p := start.Player
	if p == nil {
		return
	}
	s.rejected = p.Codec != "pcm" || p.BitDepth != 16 || p.SampleRate != SampleRate || p.Channels != 2
	if s.rejected {
		applog.Warn("sendspin: unsupported stream %s %d Hz %d-bit %dch", p.Codec, p.SampleRate, p.BitDepth, p.Channels)
		s.queued = s.queued[:0]
		return
	}
	s.r.pos.Store(0)
	s.r.epoch.Add(1)
	s.r.setProgressRunning(true)
	if s.r.cfg.OnStream != nil && !s.r.Streaming() {
		s.r.cfg.OnStream()
	}
}

// clear drops queued audio. The stream stopped (pause, stop, seek or end), so
// the track position holds until the next stream/start.
func (s *session) clear() {
	s.queued = s.queued[:0]
	s.r.epoch.Add(1)
	s.r.setProgressRunning(false)
}

// setProgressRunning freezes or resumes the reported position. Music
// Assistant sends no progress update when it pauses, so without this the
// position would run on through a pause.
func (r *Renderer) setProgressRunning(running bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := &r.progress
	if !p.set || (p.speed != 0) == running {
		return
	}
	now := time.Now()
	if running {
		p.speed = 1
	} else {
		p.pos += time.Duration(float64(now.Sub(p.at)) * p.speed)
		p.speed = 0
	}
	p.at = now
}

func (s *session) queue(chunk protocol.AudioChunk) {
	if s.rejected || len(chunk.Data) < frameBytes {
		return
	}
	i := len(s.queued)
	for i > 0 && s.queued[i-1].Timestamp > chunk.Timestamp {
		i--
	}
	s.queued = append(s.queued, protocol.AudioChunk{})
	copy(s.queued[i+1:], s.queued[i:])
	s.queued[i] = chunk
}

// pump hands every chunk whose play time has come to the beep streamer and
// drops chunks that are already too late to play in sync.
func (s *session) pump() {
	now := time.Now()
	n := 0
	for _, chunk := range s.queued {
		delay := s.clock.ServerToLocalTime(chunk.Timestamp).Sub(now)
		if delay > playWindow {
			break
		}
		n++
		if delay < -playWindow {
			continue
		}
		select {
		case s.r.pcm <- s.render(chunk.Data):
		default:
			// beep is not draining (paused or not started); drop rather
			// than block the protocol loop.
		}
	}
	s.queued = s.queued[:copy(s.queued, s.queued[n:])]
}

// render applies Music Assistant's volume to a stereo s16le chunk. The
// result is prefixed with the stream epoch so the streamer can discard audio
// from a stream that has since been replaced.
func (s *session) render(data []byte) []byte {
	data = data[:len(data)/frameBytes*frameBytes]
	out := make([]byte, 8+len(data))
	binary.LittleEndian.PutUint64(out, uint64(s.r.epoch.Load()))
	pcm := out[8:]
	gain := float64(s.r.volume.Load()) / 100
	if s.r.muted.Load() {
		gain = 0
	}
	if gain >= 1 {
		copy(pcm, data)
		return out
	}
	for i := 0; i < len(data); i += 2 {
		v := int16(binary.LittleEndian.Uint16(data[i:]))
		binary.LittleEndian.PutUint16(pcm[i:], uint16(int16(float64(v)*gain)))
	}
	return out
}

func (s *session) command(cmd protocol.PlayerCommand) {
	switch cmd.Command {
	case "volume":
		s.r.volume.Store(int32(max(0, min(100, cmd.Volume))))
	case "mute":
		s.r.muted.Store(cmd.Mute)
	default:
		return
	}
	if err := s.c.SendState(protocol.PlayerState{
		State:  "synchronized",
		Volume: int(s.r.volume.Load()),
		Muted:  s.r.muted.Load(),
	}); err != nil {
		applog.Warn("sendspin: report state: %v", err)
	}
}

// metadata applies an update now, or when the server clock reaches its
// timestamp, so the title changes when the new track becomes audible.
func (s *session) metadata(m *protocol.MetadataState) {
	if m.Timestamp <= 0 || m.Timestamp <= s.clock.ServerMicrosNow() {
		s.applyMetadata(m)
		return
	}
	i := len(s.pending)
	for i > 0 && s.pending[i-1].Timestamp > m.Timestamp {
		i--
	}
	s.pending = append(s.pending, nil)
	copy(s.pending[i+1:], s.pending[i:])
	s.pending[i] = m
}

func (s *session) applyDueMetadata() {
	now := s.clock.ServerMicrosNow()
	n := 0
	for _, m := range s.pending {
		if m.Timestamp > now {
			break
		}
		s.applyMetadata(m)
		n++
	}
	s.pending = s.pending[:copy(s.pending, s.pending[n:])]
}

func (s *session) applyMetadata(m *protocol.MetadataState) {
	str := func(key string, v *string, dst *string) {
		if m.HasField(key) {
			*dst = ""
			if v != nil {
				*dst = *v
			}
		}
	}
	prev := s.meta
	str("title", m.Title, &s.meta.Title)
	str("artist", m.Artist, &s.meta.Artist)
	str("album", m.Album, &s.meta.Album)
	str("artwork_url", m.ArtworkURL, &s.meta.ArtworkURL)
	progress := s.r.progress
	if m.HasField("progress") {
		s.meta.DurationSecs = 0
		progress = progressAnchor{}
		if p := m.Progress; p != nil {
			s.meta.DurationSecs = p.TrackDuration / 1000
			at := time.Now()
			if m.Timestamp > 0 {
				at = s.clock.ServerToLocalTime(m.Timestamp)
			}
			progress = progressAnchor{
				set:   true,
				pos:   time.Duration(p.TrackProgress) * time.Millisecond,
				at:    at,
				speed: float64(p.PlaybackSpeed) / 1000,
			}
		}
	}
	if s.meta.Title != prev.Title || s.meta.Artist != prev.Artist {
		s.r.pos.Store(0)
	}
	s.r.mu.Lock()
	s.r.now = s.meta
	s.r.progress = progress
	s.r.mu.Unlock()
}

// loadClientID returns the persisted client id, creating one on first use.
// A random id (rather than a MAC address) keeps two cliamp instances on the
// same machine from colliding when they use different config directories.
func loadClientID(path string) (string, error) {
	if path == "" {
		return "", errors.New("sendspin: client id path is required")
	}
	if b, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(b)); id != "" {
			return id, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("sendspin: read client id: %w", err)
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("sendspin: generate client id: %w", err)
	}
	id := "cliamp-" + hex.EncodeToString(b[:])
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("sendspin: save client id: %w", err)
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("sendspin: save client id: %w", err)
	}
	return id, nil
}
