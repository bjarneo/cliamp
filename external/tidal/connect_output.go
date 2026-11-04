package tidal

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/player"
)

type outputWork struct {
	ctx    context.Context
	run    func(context.Context) error
	result chan error
}

// ConnectOutput routes the existing player.Engine to an optional Tidal
// receiver. The worker orders slow controls; getters only read cached state.
// Local EQ/speed/volume remain on Engine and survive switching back.
type ConnectOutput struct {
	player.Engine
	provider        *TidalProvider
	mu              sync.Mutex
	session         *connectSession
	name            string
	path            string
	duration        time.Duration
	queue           string
	pausedIntent    bool
	stopped         bool
	generation      atomic.Uint64
	operationCancel context.CancelFunc
	work            chan outputWork
	ctx             context.Context
	cancel          context.CancelFunc
	done            chan struct{}
	closeOnce       sync.Once
}

var _ player.Engine = (*ConnectOutput)(nil)
var _ player.OutputSwitcher = (*ConnectOutput)(nil)

func NewConnectOutput(local player.Engine, p *TidalProvider) *ConnectOutput {
	ctx, cancel := context.WithCancel(context.Background())
	o := &ConnectOutput{Engine: local, provider: p, ctx: ctx, cancel: cancel, done: make(chan struct{}), work: make(chan outputWork, 64)}
	go o.run()
	return o
}
func (o *ConnectOutput) run() {
	defer close(o.done)
	defer func() {
		o.mu.Lock()
		s := o.session
		o.mu.Unlock()
		if s != nil {
			s.close()
		}
	}()
	for {
		select {
		case <-o.ctx.Done():
			return
		case task := <-o.work:
			ctx, cancel := context.WithTimeout(o.ctx, connectTimeout)
			stop := context.AfterFunc(task.ctx, cancel)
			o.mu.Lock()
			o.operationCancel = cancel
			o.mu.Unlock()
			var err error
			if ctx.Err() != nil {
				err = ctx.Err()
			} else if task.ctx.Err() != nil {
				err = task.ctx.Err()
			} else {
				err = task.run(ctx)
			}
			o.mu.Lock()
			o.operationCancel = nil
			o.mu.Unlock()
			if err != nil && task.result == nil && !errors.Is(err, context.Canceled) {
				applog.UserWarn("Tidal Connect control failed: %v", err)
			}
			stop()
			cancel()
			if task.result != nil {
				task.result <- err
			}
		}
	}
}
func (o *ConnectOutput) submit(ctx context.Context, run func(context.Context) error, wait bool) error {
	task := outputWork{ctx: ctx, run: run}
	if wait {
		task.result = make(chan error, 1)
	}
	select {
	case <-o.ctx.Done():
		return errors.New("tidal connect: output closed")
	case <-ctx.Done():
		return ctx.Err()
	case o.work <- task:
	default:
		return errors.New("tidal connect: control queue busy")
	}
	if !wait {
		return nil
	}
	select {
	case err := <-task.result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-o.ctx.Done():
		return context.Canceled
	}
}
func (o *ConnectOutput) async(run func(context.Context) error) {
	if err := o.submit(o.ctx, run, false); err != nil {
		applog.UserWarn("Tidal Connect control failed: %v", err)
	}
}
func (o *ConnectOutput) SupportsOutputTrack(path string) bool {
	return !o.RemoteOutput() || strings.HasPrefix(path, TrackURIPrefix)
}

func (o *ConnectOutput) RemoteOutput() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.session != nil
}
func (o *ConnectOutput) remote() (*connectSession, connectState, bool) {
	o.mu.Lock()
	s, queue, path, stopped := o.session, o.queue, o.path, o.stopped
	o.mu.Unlock()
	if s == nil {
		return nil, connectState{}, false
	}
	st := s.snapshot()
	owned := queue != "" && !stopped
	if owned && (st.QueueID != queue || st.TrackID != strings.TrimPrefix(path, TrackURIPrefix)) {
		st.Err = errors.New("tidal connect: playback changed in another controller; select a track in cliamp to take control")
	}
	return s, st, owned
}
func (o *ConnectOutput) OutputDevices(ctx context.Context) ([]player.AudioDevice, error) {
	local, localErr := player.ListAudioDevices()
	remote, err := discoverConnect(ctx)
	if err != nil && ctx.Err() != nil {
		return nil, err
	}
	o.mu.Lock()
	name := o.name
	active := o.session != nil
	o.mu.Unlock()
	if active {
		for i := range local {
			local[i].Active = false
		}
	}
	var out []player.AudioDevice
	if active || len(local) == 0 {
		out = append(out, player.AudioDevice{Name: "local", Description: "This computer", Active: !active})
	}
	if localErr == nil {
		out = append(out, local...)
	}
	for i := range remote {
		remote[i].Active = remote[i].Name == name
	}
	return append(out, remote...), nil
}

func (o *ConnectOutput) SwitchOutput(ctx context.Context, name, path string) error {
	gen := o.generation.Load()
	return o.submit(ctx, func(ctx context.Context) error {
		if gen != o.generation.Load() {
			return context.Canceled
		}
		return o.switchOutput(ctx, name, path)
	}, true)
}
func (o *ConnectOutput) switchOutput(ctx context.Context, name, trackPath string) error {
	o.mu.Lock()
	old := o.session
	oldName := o.name
	path, duration := o.path, o.duration
	o.mu.Unlock()
	if old != nil && name == oldName && old.snapshot().Err == nil {
		return nil
	}
	playing, paused := o.IsPlaying(), o.IsPaused()
	position := o.Position()
	if old == nil && trackPath != "" {
		path = trackPath
		duration = o.Engine.Duration()
	}
	gen := o.generation.Load()
	if !strings.HasPrefix(name, connectDevicePrefix) {
		if name != "local" {
			if err := player.SwitchAudioDevice(name); err != nil {
				return err
			}
		}
		if old == nil {
			return nil
		}
		if playing {
			if err := old.command(ctx, "pause", nil); err != nil {
				return err
			}
			if err := o.Engine.PlayAtForGeneration(path, duration, position, gen); err != nil {
				if !paused && gen == o.generation.Load() {
					cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					_ = old.command(cleanup, "play", nil)
					cancel()
				}
				return err
			}
			if paused && !o.Engine.IsPaused() {
				o.Engine.TogglePause()
			}
		}
		o.mu.Lock()
		if gen != o.generation.Load() || ctx.Err() != nil {
			o.mu.Unlock()
			o.Engine.Stop()
			return context.Canceled
		}
		o.session = nil
		o.name = ""
		o.queue = ""
		o.mu.Unlock()
		old.close()
		return nil
	}
	address := strings.TrimPrefix(name, connectDevicePrefix)
	if _, _, err := net.SplitHostPort(address); err != nil {
		return errors.New("tidal connect: use a discovered device ID or tidal:host:port")
	}
	if playing && !strings.HasPrefix(path, TrackURIPrefix) {
		return errors.New("tidal connect: select a Tidal track before handing off playback")
	}
	c, err := o.provider.ensureClient()
	if err != nil {
		return err
	}
	c.mu.Lock()
	userID := c.userID
	c.mu.Unlock()
	next, err := dialConnect(ctx, address, userID)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			next.close()
		}
	}()
	queue := ""
	if playing {
		if old != nil {
			err = old.command(ctx, "pause", nil)
		} else if !paused {
			o.Engine.TogglePause()
		}
		if err != nil {
			return err
		}
		queue, err = o.provider.loadConnectTrack(ctx, next, path, position, paused)
		if err == nil && gen != o.generation.Load() {
			err = context.Canceled
		}
		if err != nil {
			// Reconcile the receiver before restoring local audio. If it cannot be
			// paused, stay paused locally rather than risk two simultaneous outputs.
			cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			pauseErr := next.command(cleanup, "pause", nil)
			cancel()
			if pauseErr == nil && !paused && gen == o.generation.Load() {
				if old != nil {
					cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					_ = old.command(cleanup, "play", nil)
					cancel()
				} else if o.Engine.IsPaused() {
					o.Engine.TogglePause()
				}
			}
			return err
		}
	}
	o.mu.Lock()
	if gen != o.generation.Load() || ctx.Err() != nil {
		o.mu.Unlock()
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if playing {
			_ = next.command(cleanup, "pause", nil)
		}
		return context.Canceled
	}
	o.Engine.ClearPreload()
	o.Engine.Stop()
	o.session = next
	o.name = name
	o.path = path
	o.duration = duration
	o.queue = queue
	o.pausedIntent = paused
	o.stopped = !playing
	o.mu.Unlock()
	committed = true
	if old != nil {
		old.close()
	}
	return nil
}

func (o *ConnectOutput) SetPlaybackGeneration(gen uint64) {
	o.mu.Lock()
	o.generation.Store(gen)
	if o.operationCancel != nil {
		o.operationCancel()
	}
	o.mu.Unlock()
	o.Engine.SetPlaybackGeneration(gen)
}
func (o *ConnectOutput) PlayAt(path string, duration, offset time.Duration) error {
	return o.PlayAtForGeneration(path, duration, offset, 0)
}
func (o *ConnectOutput) PlayAtForGeneration(path string, duration, offset time.Duration, gen uint64) error {
	return o.submit(o.ctx, func(ctx context.Context) error {
		if gen != 0 && gen != o.generation.Load() {
			return context.Canceled
		}
		o.mu.Lock()
		s := o.session
		o.mu.Unlock()
		if s == nil {
			err := o.Engine.PlayAtForGeneration(path, duration, offset, gen)
			if err == nil {
				o.mu.Lock()
				o.path = path
				o.duration = duration
				o.mu.Unlock()
			}
			return err
		}
		if !strings.HasPrefix(path, TrackURIPrefix) {
			return errors.New("tidal connect: this output only plays Tidal tracks; select This computer for other sources")
		}
		o.mu.Lock()
		o.stopped = true
		o.mu.Unlock()
		queue, err := o.provider.loadConnectTrack(ctx, s, path, offset, false)
		if err != nil {
			return err
		}
		o.mu.Lock()
		if (gen != 0 && gen != o.generation.Load()) || ctx.Err() != nil {
			o.mu.Unlock()
			cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = s.command(cleanup, "pause", nil)
			return context.Canceled
		}
		o.path = path
		o.duration = duration
		o.queue = queue
		o.stopped = false
		o.pausedIntent = false
		o.mu.Unlock()
		return nil
	}, true)
}
func (o *ConnectOutput) Stop() {
	if !o.RemoteOutput() {
		o.Engine.Stop()
		return
	}
	o.mu.Lock()
	s, owned := o.session, o.queue != "" && !o.stopped
	o.stopped = true
	o.mu.Unlock()
	if owned {
		o.async(func(ctx context.Context) error { return s.command(ctx, "stop", nil) })
	}
}
func (o *ConnectOutput) TogglePause() {
	if !o.RemoteOutput() {
		o.Engine.TogglePause()
		return
	}
	o.async(func(ctx context.Context) (err error) {
		s, st, owned := o.remote()
		if !owned || st.Err != nil {
			return st.Err
		}
		pause := st.State == "PLAYING"
		o.mu.Lock()
		previous := o.pausedIntent
		o.pausedIntent = pause
		o.mu.Unlock()
		defer func() {
			if err != nil {
				o.mu.Lock()
				o.pausedIntent = previous
				o.mu.Unlock()
			}
		}()
		cmd := "play"
		if pause {
			cmd = "pause"
		}
		if err := s.command(ctx, cmd, nil); err != nil {
			return err
		}
		return s.wait(ctx, func(st connectState) bool { return pause && st.State == "PAUSED" || !pause && st.State == "PLAYING" })
	})
}
func (o *ConnectOutput) Seek(delta time.Duration) error {
	if !o.RemoteOutput() {
		return o.Engine.Seek(delta)
	}
	return o.submit(o.ctx, func(ctx context.Context) error {
		s, st, owned := o.remote()
		if st.Err != nil {
			return st.Err
		}
		if !owned {
			return errors.New("tidal connect: no cliamp track is playing")
		}
		target := max(time.Duration(0), min(st.Duration, st.Position+delta))
		return s.seek(ctx, target)
	}, true)
}
func (o *ConnectOutput) SeekYTDL(delta time.Duration) error {
	if o.RemoteOutput() {
		return o.Seek(delta)
	}
	return o.Engine.SeekYTDL(delta)
}
func (o *ConnectOutput) CancelSeekYTDL() {
	if !o.RemoteOutput() {
		o.Engine.CancelSeekYTDL()
	}
}
func (o *ConnectOutput) Close() { o.closeOnce.Do(func() { o.cancel(); <-o.done; o.Engine.Close() }) }
func (o *ConnectOutput) IsPlaying() bool {
	if s, st, owned := o.remote(); s != nil {
		return owned && st.Err == nil && (st.State == "PLAYING" || st.State == "PAUSED" || o.Drained())
	}
	return o.Engine.IsPlaying()
}
func (o *ConnectOutput) IsPaused() bool {
	if s, st, owned := o.remote(); s != nil {
		return owned && st.State == "PAUSED" && !o.Drained()
	}
	return o.Engine.IsPaused()
}
func (o *ConnectOutput) Drained() bool {
	if s, st, owned := o.remote(); s != nil {
		o.mu.Lock()
		intent := o.pausedIntent
		o.mu.Unlock()
		return owned && st.Err == nil && !intent && st.Duration > 0 && st.Position >= st.Duration && (st.State == "PAUSED" || st.State == "STOPPED")
	}
	return o.Engine.Drained()
}
func (o *ConnectOutput) PositionAndDuration() (time.Duration, time.Duration) {
	if s, st, owned := o.remote(); s != nil {
		if !owned {
			return 0, 0
		}
		return st.Position, st.Duration
	}
	return o.Engine.PositionAndDuration()
}
func (o *ConnectOutput) Position() time.Duration { p, _ := o.PositionAndDuration(); return p }
func (o *ConnectOutput) Duration() time.Duration { _, d := o.PositionAndDuration(); return d }
func (o *ConnectOutput) Seekable() bool {
	if s, st, owned := o.remote(); s != nil {
		return owned && st.Err == nil && st.Duration > 0
	}
	return o.Engine.Seekable()
}
func (o *ConnectOutput) IsYTDLSeek() bool {
	if o.RemoteOutput() {
		return false
	}
	return o.Engine.IsYTDLSeek()
}
func (o *ConnectOutput) IsLiveStream() bool {
	if o.RemoteOutput() {
		return false
	}
	return o.Engine.IsLiveStream()
}
func (o *ConnectOutput) HasPreload() bool {
	if o.RemoteOutput() {
		return false
	}
	return o.Engine.HasPreload()
}
func (o *ConnectOutput) BeginPreload() uint64 {
	if o.RemoteOutput() {
		return 0
	}
	return o.Engine.BeginPreload()
}
func (o *ConnectOutput) PreloadForGeneration(path string, d time.Duration, g uint64) error {
	if o.RemoteOutput() {
		return errors.New("tidal connect: gapless preload unavailable")
	}
	return o.Engine.PreloadForGeneration(path, d, g)
}
func (o *ConnectOutput) GaplessAdvanced() bool {
	if o.RemoteOutput() {
		return false
	}
	return o.Engine.GaplessAdvanced()
}
func (o *ConnectOutput) LastPlayedDuration() time.Duration {
	if o.RemoteOutput() {
		return 0
	}
	return o.Engine.LastPlayedDuration()
}
func (o *ConnectOutput) StreamErr() error {
	if s, st, _ := o.remote(); s != nil {
		return st.Err
	}
	return o.Engine.StreamErr()
}
func (o *ConnectOutput) StreamTitle() string {
	if o.RemoteOutput() {
		return ""
	}
	return o.Engine.StreamTitle()
}
func (o *ConnectOutput) StreamBytes() (int64, int64) {
	if o.RemoteOutput() {
		return 0, 0
	}
	return o.Engine.StreamBytes()
}
func (o *ConnectOutput) SamplesInto(dst []float64) int {
	if o.RemoteOutput() {
		clear(dst)
		return 0
	}
	return o.Engine.SamplesInto(dst)
}
func (o *ConnectOutput) WaveformSamplesInto(dst []float64) int {
	if o.RemoteOutput() {
		clear(dst)
		return 0
	}
	return o.Engine.WaveformSamplesInto(dst)
}
func (o *ConnectOutput) StereoSamplesInto(dst [][2]float64) int {
	if o.RemoteOutput() {
		clear(dst)
		return 0
	}
	return o.Engine.StereoSamplesInto(dst)
}
func (o *ConnectOutput) HasSourceResolver(path string) bool {
	r, ok := o.Engine.(interface{ HasSourceResolver(string) bool })
	return ok && r.HasSourceResolver(path)
}
func (o *ConnectOutput) SetVolume(db float64) {
	if !o.RemoteOutput() {
		o.Engine.SetVolume(db)
		return
	}
	o.async(func(ctx context.Context) error {
		s, _, _ := o.remote()
		if s == nil {
			return nil
		}
		level := min(1.0, math.Pow(10, db/20))
		if db <= o.Engine.VolumeMin() {
			level = 0
		}
		return s.command(ctx, "setVolume", map[string]any{"level": level})
	})
}

// VolumeKnown reports whether relative adjustments can use the current level.
// Local volume is always known; a receiver must first report a valid level.
func (o *ConnectOutput) VolumeKnown() bool {
	if s, st, _ := o.remote(); s != nil {
		return st.VolumeKnown
	}
	return true
}

func (o *ConnectOutput) Volume() float64 {
	if s, st, _ := o.remote(); s != nil {
		if st.Muted || st.Volume <= 0 {
			return o.Engine.VolumeMin()
		}
		return max(o.Engine.VolumeMin(), 20*math.Log10(st.Volume))
	}
	return o.Engine.Volume()
}
func (o *ConnectOutput) SetSpeed(v float64) {
	if !o.RemoteOutput() {
		o.Engine.SetSpeed(v)
	}
}
func (o *ConnectOutput) SetEQBand(b int, v float64) {
	if !o.RemoteOutput() {
		o.Engine.SetEQBand(b, v)
	}
}
func (o *ConnectOutput) ToggleMono() {
	if !o.RemoteOutput() {
		o.Engine.ToggleMono()
	}
}

// String is diagnostic only and deliberately omits tokens and queue IDs.
func (o *ConnectOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return fmt.Sprintf("Tidal Connect output %q", o.name)
}
