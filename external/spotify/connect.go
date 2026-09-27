// connect.go — Spotify Connect receiver.
//
// When enabled (connect_enabled = true), cliamp advertises itself as a
// Spotify Connect device: Zeroconf registers the _spotify-connect._tcp
// service on the LAN and the dealer WebSocket carries commands and state
// between this device and the official Spotify apps.
//
// Remote commands are translated into the shared messages from
// internal/playback and sent to the TUI, so audio always goes through
// cliamp's own player; the go-librespot player inside Session stays
// dedicated to producing streams for NewStream. Playback state (current
// track, position, volume, shuffle/repeat) is pushed back through
// PutConnectState so the controlling app stays in sync.

package spotify

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	librespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/dealer"
	librespotPlayer "github.com/devgianlu/go-librespot/player"
	connectpb "github.com/devgianlu/go-librespot/proto/spotify/connectstate"
	devicespb "github.com/devgianlu/go-librespot/proto/spotify/connectstate/devices"
	"github.com/devgianlu/go-librespot/session"
	"github.com/devgianlu/go-librespot/spclient"
	"github.com/devgianlu/go-librespot/tracks"
	"github.com/devgianlu/go-librespot/zeroconf"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/internal/appmeta"
	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/playlist"
)

const (
	connectDefaultName = "cliamp"
	connectCommandURI  = "hm://connect-state/v1/player/command"
	// connectStateMinPut is the minimum spacing between connect-state PUTs,
	// matching go-librespot's daemon.
	connectStateMinPut = 200 * time.Millisecond
	// connectDealerRetryInterval is how often run() re-serves the dealer
	// after it dies on its own: the dealer's internal backoff gives up after
	// ~15min, after which nothing else would revive the connection.
	connectDealerRetryInterval = 30 * time.Second
	// connectEnrichBatch is the /v1/tracks page size for metadata enrichment.
	connectEnrichBatch = 50
)

// ConnectConfig configures the Spotify Connect receiver. Send forwards
// internal/playback messages into the TUI (typically tea.Program.Send).
type ConnectConfig struct {
	Name string // device name in the Spotify Connect picker
	Port int    // zeroconf listener port; 0 picks an ephemeral port
	Send func(msg any)
}

func (c ConnectConfig) deviceName() string {
	if strings.TrimSpace(c.Name) == "" {
		return connectDefaultName
	}
	return c.Name
}

// connectReceiver runs the Connect lifecycle for one Session: zeroconf
// advertisement, dealer connection, command handling, and state pushes.
// Run-goroutine-only fields are documented as such; the latest playback
// snapshot is written by the UI under mu.
type connectReceiver struct {
	s      *Session
	cfg    ConnectConfig
	ctx    context.Context
	cancel context.CancelFunc

	dirty chan struct{} // cap 1: playback snapshot changed

	mu         sync.Mutex
	playback   playback.State
	playbackAt time.Time

	// Owned by the run goroutine.
	zc         *zeroconf.Zeroconf
	zcDeviceID string

	spotConnID      string
	active          bool
	activeSince     time.Time
	sessionID       string
	playbackID      string
	ctxURI          string
	ctxURL          string
	ctxMeta         map[string]string
	ctxRestrictions *connectpb.Restrictions
	suppressions    *connectpb.Suppressions
	playOrigin      *connectpb.PlayOrigin
	opts            *connectpb.ContextPlayerOptions
	sleepTimer      *time.Timer
	sleepState      *connectpb.SleepTimer
	sleepEndOfTrack bool
	sleepTrackURL   string
	lastCmdID       uint32
	lastCmdDevice   string
	lastTransferTs  int64
	lastPut         time.Time
	putTimer        *time.Timer
	cooldownUntil   time.Time // 429 backoff expiry; schedulePut never PUTs before it
}

func newConnectReceiver(s *Session, cfg ConnectConfig) *connectReceiver {
	ctx, cancel := context.WithCancel(context.Background())
	return &connectReceiver{
		s:          s,
		cfg:        cfg,
		ctx:        ctx,
		cancel:     cancel,
		dirty:      make(chan struct{}, 1),
		putTimer:   stoppedTimer(),
		sleepTimer: stoppedTimer(),
	}
}

func stoppedTimer() *time.Timer {
	t := time.NewTimer(time.Hour)
	if !t.Stop() {
		<-t.C
	}
	return t
}

func (r *connectReceiver) start() { go r.run() }

// stop cancels the run goroutine without blocking; the goroutine tears down
// zeroconf and the dealer reads itself.
func (r *connectReceiver) stop() { r.cancel() }

// run binds to the current inner session and serves until the dealer dies or
// the receiver is stopped. Session.reconnect swaps the inner session: the old
// dealer's channels close, serve returns, and the loop rebinds.
func (r *connectReceiver) run() {
	defer func() {
		if r.zc != nil {
			r.zc.Close()
			r.zc = nil
		}
	}()

	var bound *session.Session
	for r.ctx.Err() == nil {
		changed, sess := r.s.sessionSwap()
		if sess == nil {
			select {
			case <-r.ctx.Done():
			case <-changed:
			}
			continue
		}
		if sess != bound {
			bound = sess
			r.bind(sess)
		}
		r.serve(sess)
		if r.ctx.Err() != nil {
			break
		}
		if r.s.innerSession() != bound {
			continue // session was swapped mid-serve: rebind to the new one
		}
		// The dealer under this session died on its own; wait for a swap,
		// but also re-serve periodically so a dealer whose backoff gave up
		// doesn't park the receiver until restart.
		changed, _ = r.s.sessionSwap()
		select {
		case <-r.ctx.Done():
		case <-changed:
		case <-time.After(connectDealerRetryInterval):
			applog.Warn("spotify: connect: dealer connection lost, retrying")
		}
	}
}

// bind resets per-connection state and (re)starts zeroconf for the session's
// device ID.
func (r *connectReceiver) bind(sess *session.Session) {
	r.spotConnID = ""
	r.active = false
	r.sessionID = ""
	r.playbackID = ""
	r.ctxURI, r.ctxURL = "", ""
	r.ctxMeta = nil
	r.ctxRestrictions = nil
	r.suppressions = nil
	r.playOrigin = nil
	r.opts = nil
	r.sleepEndOfTrack = false
	r.sleepTrackURL = ""
	r.sleepState = nil
	r.lastCmdID = 0
	r.lastCmdDevice = ""

	devID := r.s.deviceID()
	if r.zc != nil && r.zcDeviceID != devID {
		r.zc.Close()
		r.zc = nil
	}
	if r.zc == nil {
		zc, err := zeroconf.NewZeroconf(&librespot.NullLogger{}, r.cfg.Port, r.cfg.deviceName(), devID, devicespb.DeviceType_COMPUTER, nil, false)
		if err != nil {
			// Zeroconf failure is not fatal: the device still registers via
			// the cloud connect-state channel.
			applog.Warn("spotify: connect: zeroconf unavailable: %v", err)
		} else {
			r.zc = zc
			r.zcDeviceID = devID
			go func() {
				// Reject addUser: blob credentials lack the OAuth token the
				// Web API needs; the cloud path registers the device anyway.
				if err := zc.Serve(func(zeroconf.NewUserRequest) bool { return false }); err != nil && r.ctx.Err() == nil {
					applog.Warn("spotify: connect: zeroconf serve: %v", err)
				}
			}()
		}
	}
	if r.zc != nil {
		r.zc.SetCurrentUser(sess.Username())
	}
}

func (r *connectReceiver) serve(sess *session.Session) {
	if err := sess.Dealer().Connect(r.ctx); err != nil {
		applog.Warn("spotify: connect: dealer: %v", err)
		return
	}
	msgs := sess.Dealer().ReceiveMessage(
		"hm://pusher/v1/connections/",
		"hm://connect-state/v1/connect/volume",
		"hm://connect-state/v1/connect/logout",
		"hm://connect-state/v1/cluster",
	)
	cmds := sess.Dealer().ReceiveRequest(connectCommandURI)

	for {
		select {
		case <-r.ctx.Done():
			return
		case msg, ok := <-msgs:
			if !ok {
				return
			}
			r.handleMessage(sess, msg)
		case cmd, ok := <-cmds:
			if !ok {
				return
			}
			cmd.Reply(r.handleCommand(sess, cmd.Payload))
		case <-r.dirty:
			r.schedulePut(sess)
		case <-r.putTimer.C:
			r.flushState(sess, connectpb.PutStateReason_PLAYER_STATE_CHANGED)
		case <-r.sleepTimer.C:
			r.send(playback.PauseMsg{})
			r.sleepState = nil
			r.signalDirty()
		}
	}
}

func (r *connectReceiver) send(msg any) {
	if r.cfg.Send != nil {
		r.cfg.Send(msg)
	}
}

// notifyUpdate stores the latest playback snapshot from the TUI and asks the
// run goroutine to push it.
func (r *connectReceiver) notifyUpdate(st playback.State) {
	r.mu.Lock()
	// The TUI notifies on every tick, so only mark dirty on meaningful
	// change: position drift is extrapolated from playbackAt and needs no
	// PUT. Without this the receiver spams state PUTs ~1/sec, which can
	// get the device throttled or dropped.
	changed := r.playback.Status != st.Status ||
		r.playback.Track != st.Track ||
		r.playback.Shuffle != st.Shuffle ||
		r.playback.Repeat != st.Repeat ||
		r.playback.VolumeDB != st.VolumeDB ||
		r.playback.Seekable != st.Seekable ||
		r.playback.NextURL != st.NextURL
	r.playback = st
	r.playbackAt = time.Now()
	r.mu.Unlock()
	if changed {
		r.signalDirty()
	}
}

// notifySeek refreshes only the position; called on a completed seek so the
// controller sees the new spot immediately.
func (r *connectReceiver) notifySeek(pos time.Duration) {
	r.mu.Lock()
	r.playback.Position = pos
	r.playbackAt = time.Now()
	r.mu.Unlock()
	r.signalDirty()
}

func (r *connectReceiver) signalDirty() {
	select {
	case r.dirty <- struct{}{}:
	default:
	}
}

func (r *connectReceiver) snapshot() (playback.State, time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.playback, r.playbackAt
}

func (r *connectReceiver) handleMessage(sess *session.Session, msg dealer.Message) {
	switch {
	case strings.HasPrefix(msg.Uri, "hm://pusher/v1/connections/"):
		r.spotConnID = msg.Headers["Spotify-Connection-Id"]
		applog.Debug("spotify: connect: connection id %q", r.spotConnID)
		r.flushState(sess, connectpb.PutStateReason_NEW_DEVICE)

	case strings.HasPrefix(msg.Uri, "hm://connect-state/v1/connect/volume"):
		var cmd connectpb.SetVolumeCommand
		if err := proto.Unmarshal(msg.Payload, &cmd); err != nil {
			applog.Warn("spotify: connect: bad volume command: %v", err)
			return
		}
		r.send(playback.SetVolumeMsg{VolumeDB: playback.LinearToDB(float64(cmd.Volume) / librespotPlayer.MaxStateVolume)})

	case strings.HasPrefix(msg.Uri, "hm://connect-state/v1/connect/logout"):
		// The user asked to forget this device; unbinding is enough — the
		// session itself stays usable for local browsing.
		r.s.StopConnect()

	case strings.HasPrefix(msg.Uri, "hm://connect-state/v1/cluster"):
		var cluster connectpb.Cluster
		if err := proto.Unmarshal(msg.Payload, &cluster); err != nil {
			return
		}
		if !r.active || cluster.ActiveDeviceId == "" || cluster.ActiveDeviceId == r.s.deviceID() {
			return
		}
		if cluster.PlayerState == nil || cluster.PlayerState.Timestamp <= r.lastTransferTs {
			return
		}
		// Playback moved to another device.
		applog.Debug("spotify: connect: cluster moved active to %q, yielding", cluster.ActiveDeviceId)
		r.active = false
		r.send(playback.PauseMsg{})
		if err := sess.Spclient().PutConnectStateInactive(r.ctx, r.spotConnID, false); err != nil {
			applog.Warn("spotify: connect: put inactive: %v", err)
		}
	}
}

func (r *connectReceiver) handleCommand(sess *session.Session, req dealer.RequestPayload) (ok bool) {
	defer func() {
		if rec := recover(); rec != nil {
			applog.Warn("spotify: connect: command %q failed: %v", req.Command.Endpoint, rec)
			ok = false
		}
	}()

	r.lastCmdID = req.MessageId
	r.lastCmdDevice = req.SentByDeviceId
	applog.Debug("spotify: connect: command %s from %s", req.Command.Endpoint, req.SentByDeviceId)

	switch req.Command.Endpoint {
	case "transfer":
		return r.cmdTransfer(sess, req)
	case "play":
		return r.cmdPlay(sess, req)
	case "pause":
		r.send(playback.PauseMsg{})
	case "resume":
		r.send(playback.PlayMsg{})
	case "seek_to":
		return r.cmdSeek(req)
	case "skip_prev":
		r.send(playback.PrevMsg{})
	case "skip_next":
		r.send(playback.NextMsg{})
	case "update_context":
		r.cmdUpdateContext(req)
	case "set_repeating_context":
		if v, ok := req.Command.Value.(bool); ok {
			r.applyOptions(&v, nil, nil)
		}
	case "set_repeating_track":
		if v, ok := req.Command.Value.(bool); ok {
			r.applyOptions(nil, &v, nil)
		}
	case "set_shuffling_context":
		if v, ok := req.Command.Value.(bool); ok {
			r.applyOptions(nil, nil, &v)
		}
	case "set_options":
		r.applyOptions(req.Command.RepeatingContext, req.Command.RepeatingTrack, req.Command.ShufflingContext)
	case "set_queue":
		for _, t := range req.Command.NextTracks {
			r.enqueueContextTrack(t)
		}
	case "add_to_queue":
		r.enqueueContextTrack(req.Command.Track)
	case "set_sleep_timer":
		r.cmdSleepTimer(req)
	default:
		applog.Warn("spotify: connect: unhandled command %s", req.Command.Endpoint)
		return false
	}
	r.signalDirty()
	return true
}

// cmdTransfer handles a transfer of playback from another device: the sent
// TransferState carries the context, the current track, position, and queue.
func (r *connectReceiver) cmdTransfer(sess *session.Session, req dealer.RequestPayload) bool {
	if len(req.Command.Data) == 0 {
		// A device "picked" us without an explicit transfer payload.
		r.active = true
		r.signalDirty()
		return true
	}

	var ts connectpb.TransferState
	if err := proto.Unmarshal(req.Command.Data, &ts); err != nil {
		applog.Warn("spotify: connect: bad transfer state: %v", err)
		return false
	}
	r.lastTransferTs = ts.Playback.Timestamp
	r.active = true
	if r.activeSince.IsZero() {
		r.activeSince = time.Now()
	}

	tl, err := tracks.NewTrackListFromContext(r.ctx, &librespot.NullLogger{}, sess.Spclient(), ts.CurrentSession.Context)
	if err != nil {
		applog.Warn("spotify: connect: resolve context: %v", err)
		return false
	}

	if ts.CurrentSession.OriginalSessionId != nil {
		r.sessionID = *ts.CurrentSession.OriginalSessionId
	} else {
		r.sessionID = connectSessionID()
	}
	r.playbackID = connectPlaybackID()

	r.opts = ts.Options
	if r.opts == nil {
		r.opts = &connectpb.ContextPlayerOptions{}
	}
	r.ctxURI = ts.CurrentSession.Context.Uri
	r.ctxURL = ts.CurrentSession.Context.Url
	r.ctxMeta = contextMetadata(ts.CurrentSession.Context.Metadata, tl.Metadata())
	r.ctxRestrictions = ts.CurrentSession.Context.Restrictions
	r.suppressions = ts.CurrentSession.Suppressions
	r.playOrigin = ts.CurrentSession.PlayOrigin
	if r.playOrigin != nil {
		r.playOrigin.DeviceIdentifier = req.SentByDeviceId
	}

	if err := tl.TrySeekTo(r.ctx, ts.Playback.CurrentTrack); err != nil {
		applog.Warn("spotify: connect: seek to transferred track: %v", err)
		return false
	}
	cur := tl.CurrentTrack()

	paused := ts.Playback.IsPaused && req.Command.Options.RestorePaused != "resume"
	position := time.Duration(ts.Playback.PositionAsOfTimestamp) * time.Millisecond
	r.loadTrackList(sess, tl, cur, ts.Queue.Tracks, position, paused)
	return true
}

// cmdPlay handles the play command, which carries a fresh context plus
// optional skip-to and player options.
func (r *connectReceiver) cmdPlay(sess *session.Session, req dealer.RequestPayload) bool {
	if req.Command.Context == nil || req.Command.Context.Uri == "" {
		r.send(playback.PlayMsg{})
		return true
	}
	r.active = true
	if r.activeSince.IsZero() {
		r.activeSince = time.Now()
	}

	tl, err := tracks.NewTrackListFromContext(r.ctx, &librespot.NullLogger{}, sess.Spclient(), req.Command.Context)
	if err != nil {
		applog.Warn("spotify: connect: resolve context: %v", err)
		return false
	}

	if req.Command.Options.SessionId != "" {
		r.sessionID = req.Command.Options.SessionId
	} else {
		r.sessionID = connectSessionID()
	}
	r.playbackID = connectPlaybackID()

	r.playOrigin = req.Command.PlayOrigin
	if r.playOrigin != nil {
		r.playOrigin.DeviceIdentifier = req.SentByDeviceId
	}
	r.suppressions = req.Command.Options.Suppressions
	r.ctxURI = req.Command.Context.Uri
	r.ctxURL = req.Command.Context.Url
	r.ctxMeta = contextMetadata(req.Command.Context.Metadata, tl.Metadata())
	r.ctxRestrictions = req.Command.Context.Restrictions

	if r.opts == nil {
		r.opts = &connectpb.ContextPlayerOptions{}
	}
	if ov := req.Command.Options.PlayerOptionsOverride; ov != nil {
		r.opts.ShufflingContext = ov.ShufflingContext
		r.opts.RepeatingTrack = ov.RepeatingTrack
		r.opts.RepeatingContext = ov.RepeatingContext
	}

	skipTo := req.Command.Options.SkipTo
	if skipTo.TrackUid != "" || skipTo.TrackUri != "" || skipTo.TrackIndex > 0 {
		index := -1
		err := tl.Seek(r.ctx, func(track *connectpb.ContextTrack) bool {
			switch {
			case skipTo.TrackUid != "" && skipTo.TrackUid == track.Uid:
				return true
			case skipTo.TrackUri != "" && skipTo.TrackUri == track.Uri:
				return true
			case skipTo.TrackIndex != 0 && skipTo.TrackUri == "" && skipTo.TrackUid == "":
				index++
				return index == skipTo.TrackIndex
			default:
				return false
			}
		})
		if err != nil {
			applog.Warn("spotify: connect: skip_to failed: %v", err)
			return false
		}
	}

	r.loadTrackList(sess, tl, tl.CurrentTrack(), nil, 0, req.Command.Options.InitiallyPaused)
	return true
}

// loadTrackList flattens a resolved go-librespot track list into playlist
// tracks and hands it to the TUI via PlayTracksMsg.
func (r *connectReceiver) loadTrackList(sess *session.Session, tl *tracks.List, cur *connectpb.ProvidedTrack, queueCtx []*connectpb.ContextTrack, position time.Duration, paused bool) {
	typ := librespot.InferSpotifyIdTypeFromContextUri(r.ctxURI)
	provided := tl.AllTracks(r.ctx)

	curIdx := 0
	if cur != nil {
		curIdx = -1
		for i, pt := range provided {
			if pt.Uri == cur.Uri || (cur.Uid != "" && pt.Uid == cur.Uid) {
				curIdx = i
				break
			}
		}
		if curIdx < 0 {
			// The playing track isn't part of the context (e.g. a queued
			// item): prepend it so it can play.
			provided = append([]*connectpb.ProvidedTrack{cur}, provided...)
			curIdx = 0
		}
	}

	trs := make([]playlist.Track, len(provided))
	for i, pt := range provided {
		trs[i] = providedToPlaylist(pt)
	}
	queue := make([]playlist.Track, 0, len(queueCtx))
	for _, t := range queueCtx {
		queue = append(queue, providedToPlaylist(librespot.ContextTrackToProvidedTrack(typ, t)))
	}
	r.enrichTracks(trs)
	r.enrichTracks(queue)

	shuffle := r.opts != nil && r.opts.ShufflingContext
	repeat := repeatMode(r.opts)
	r.send(playback.PlayTracksMsg{
		Tracks:      trs,
		Index:       curIdx,
		Position:    position,
		Paused:      paused,
		Shuffle:     &shuffle,
		Repeat:      &repeat,
		Queue:       queue,
		ContextName: r.ctxMeta["context_description"],
	})
}

func (r *connectReceiver) cmdSeek(req dealer.RequestPayload) bool {
	switch req.Command.Relative {
	case "current":
		r.send(playback.SeekMsg{Offset: time.Duration(req.Command.Position) * time.Millisecond})
	case "beginning":
		r.send(playback.SetPositionMsg{Position: time.Duration(req.Command.Position) * time.Millisecond})
	case "":
		v, ok := req.Command.Value.(float64)
		if !ok {
			applog.Warn("spotify: connect: unsupported seek_to value %T", req.Command.Value)
			return false
		}
		r.send(playback.SetPositionMsg{Position: time.Duration(v) * time.Millisecond})
	default:
		applog.Warn("spotify: connect: unsupported seek_to relative %q", req.Command.Relative)
		return false
	}
	return true
}

func (r *connectReceiver) cmdUpdateContext(req dealer.RequestPayload) {
	ctx := req.Command.Context
	if ctx == nil || ctx.Uri != r.ctxURI {
		return // update for a context we don't have; ignore like the daemon does
	}
	r.ctxRestrictions = ctx.Restrictions
	if r.ctxMeta == nil {
		r.ctxMeta = map[string]string{}
	}
	for k, v := range ctx.Metadata {
		r.ctxMeta[k] = v
	}
}

// applyOptions updates shuffle/repeat bookkeeping and forwards changes to the
// TUI, which owns the real playlist state.
func (r *connectReceiver) applyOptions(repeatCtx, repeatTrack, shuffle *bool) {
	if r.opts == nil {
		r.opts = &connectpb.ContextPlayerOptions{}
	}
	if repeatCtx != nil {
		r.opts.RepeatingContext = *repeatCtx
	}
	if repeatTrack != nil {
		r.opts.RepeatingTrack = *repeatTrack
	}
	if shuffle != nil {
		r.opts.ShufflingContext = *shuffle
		r.send(playback.SetShuffleMsg{On: *shuffle})
	}
	if repeatCtx != nil || repeatTrack != nil {
		r.send(playback.SetRepeatMsg{Mode: repeatMode(r.opts)})
	}
}

func repeatMode(opts *connectpb.ContextPlayerOptions) playlist.RepeatMode {
	switch {
	case opts != nil && opts.RepeatingTrack:
		return playlist.RepeatOne
	case opts != nil && opts.RepeatingContext:
		return playlist.RepeatAll
	default:
		return playlist.RepeatOff
	}
}

func (r *connectReceiver) enqueueContextTrack(t *connectpb.ContextTrack) {
	if t == nil {
		return
	}
	typ := librespot.InferSpotifyIdTypeFromContextUri(t.Uri)
	tr := providedToPlaylist(librespot.ContextTrackToProvidedTrack(typ, t))
	if tr.Path == "" {
		return
	}
	trs := []playlist.Track{tr}
	r.enrichTracks(trs)
	r.send(playback.EnqueueMsg{Track: trs[0]})
}

func (r *connectReceiver) cmdSleepTimer(req dealer.RequestPayload) {
	tt := req.Command.TimerType
	switch {
	case tt != nil && tt.Type == "duration" && tt.DurationS > 0:
		d := time.Duration(tt.DurationS) * time.Second
		r.sleepTimer.Reset(d)
		r.sleepEndOfTrack = false
		r.sleepState = &connectpb.SleepTimer{TimerType: &connectpb.SleepTimer_Timestamp_{
			Timestamp: &connectpb.SleepTimer_Timestamp{Timestamp: time.Now().Add(d).UnixMilli()},
		}}
	case tt != nil && tt.Type == "end_of_track":
		st, _ := r.snapshot()
		r.sleepTrackURL = st.Track.URL
		r.sleepEndOfTrack = true
		r.sleepState = &connectpb.SleepTimer{TimerType: &connectpb.SleepTimer_EndOfTrack_{
			EndOfTrack: &connectpb.SleepTimer_EndOfTrack{},
		}}
	default:
		r.sleepEndOfTrack = false
		r.sleepState = &connectpb.SleepTimer{TimerType: &connectpb.SleepTimer_None_{
			None: &connectpb.SleepTimer_None{},
		}}
	}
}

// schedulePut coalesces playback updates into PUTs at most
// connectStateMinPut apart. It also owns the end-of-track sleep-timer edge
// (detected on the track-change snapshot).
func (r *connectReceiver) schedulePut(sess *session.Session) {
	if r.spotConnID == "" {
		applog.Debug("spotify: connect: skip PUT, no connection id yet")
		return // cannot PUT until the connection-id message arrives
	}
	st, _ := r.snapshot()
	if r.sleepEndOfTrack && r.sleepTrackURL != "" && st.Track.URL != "" && st.Track.URL != r.sleepTrackURL {
		r.sleepEndOfTrack = false
		r.sleepState = nil
		r.send(playback.PauseMsg{})
	}

	if d := putDelay(r.lastPut, r.cooldownUntil, time.Now()); d > 0 {
		r.putTimer.Reset(d)
		return
	}
	r.flushState(sess, connectpb.PutStateReason_PLAYER_STATE_CHANGED)
}

// putDelay returns how long to wait before the next connect-state PUT: at
// least connectStateMinPut after lastPut, and never before the 429 backoff
// expires. Zero means PUT now. Pure so the cooldown interaction is testable
// without network.
func putDelay(lastPut, cooldownUntil, now time.Time) time.Duration {
	d := connectStateMinPut - now.Sub(lastPut)
	if rem := cooldownUntil.Sub(now); rem > d {
		d = rem
	}
	return max(d, 0)
}

func (r *connectReceiver) flushState(sess *session.Session, reason connectpb.PutStateReason) {
	if r.spotConnID == "" {
		applog.Debug("spotify: connect: skip PUT %s, no connection id yet", reason)
		return
	}
	applog.Debug("spotify: connect: PUT %s active=%t track=%q", reason, r.active, r.playerState().GetTrack().GetUri())
	r.lastPut = time.Now()

	req := &connectpb.PutStateRequest{
		ClientSideTimestamp:       uint64(time.Now().UnixMilli()),
		MemberType:                connectpb.MemberType_CONNECT_STATE,
		PutStateReason:            reason,
		IsActive:                  r.active,
		Device:                    &connectpb.Device{DeviceInfo: r.deviceInfo(), PlayerState: r.playerState()},
		LastCommandMessageId:      r.lastCmdID,
		LastCommandSentByDeviceId: r.lastCmdDevice,
	}
	if !r.activeSince.IsZero() {
		req.StartedPlayingAt = uint64(r.activeSince.UnixMilli())
	}

	ctx, cancel := context.WithTimeout(r.ctx, 10*time.Second)
	defer cancel()
	if _, err := sess.Spclient().PutConnectState(ctx, r.spotConnID, req); err != nil {
		var rl *spclient.RateLimitedError
		if errors.As(err, &rl) {
			// Coalesce: resend once after the cooldown, like the daemon.
			// The timestamp (not just the timer) carries the backoff so a
			// playback update arriving mid-cooldown can't shorten the wait.
			applog.Debug("spotify: connect: PUT rate-limited, retry in %s", rl.RetryAfter)
			r.cooldownUntil = time.Now().Add(rl.RetryAfter)
			r.putTimer.Reset(rl.RetryAfter)
			return
		}
		applog.Warn("spotify: connect: put state: %v", err)
	}
}

// playerState builds the connect-state PlayerState from the latest TUI
// snapshot plus receiver-owned context bookkeeping.
func (r *connectReceiver) playerState() *connectpb.PlayerState {
	st, at := r.snapshot()

	pos := st.Position.Milliseconds()
	var speed float64
	isPaused := true
	switch st.Status {
	case playback.StatusPlaying:
		speed = 1
		isPaused = false
		pos += time.Since(at).Milliseconds()
	}

	// Shuffle/repeat are echoed from the TUI snapshot, which owns the real
	// playlist state.
	opts := &connectpb.ContextPlayerOptions{
		ShufflingContext: st.Shuffle,
		RepeatingTrack:   st.Repeat == playlist.RepeatOne,
		RepeatingContext: st.Repeat == playlist.RepeatAll,
	}

	state := &connectpb.PlayerState{
		Timestamp:             time.Now().UnixMilli(),
		IsPlaying:             st.Status != playback.StatusStopped,
		IsPaused:              isPaused,
		PlaybackSpeed:         speed,
		PositionAsOfTimestamp: pos,
		Position:              pos,
		Duration:              st.Track.Duration.Milliseconds(),
		Options:               opts,
		Suppressions:          r.suppressions,
		ContextRestrictions:   r.ctxRestrictions,
		ContextUri:            r.ctxURI,
		ContextUrl:            r.ctxURL,
		ContextMetadata:       r.ctxMeta,
		PlayOrigin:            r.playOrigin,
		SessionId:             r.sessionID,
		PlaybackId:            r.playbackID,
		SleepTimer:            r.sleepState,
	}

	if uri := st.Track.URL; strings.HasPrefix(uri, "spotify:") {
		state.Track = &connectpb.ProvidedTrack{
			Uri:      uri,
			Provider: "context",
			Metadata: stateTrackMetadata(st),
		}
		if next := st.NextURL; strings.HasPrefix(next, "spotify:") {
			state.NextTracks = []*connectpb.ProvidedTrack{{Uri: next, Provider: "context"}}
		}
	}
	return state
}

func stateTrackMetadata(st playback.State) map[string]string {
	md := map[string]string{}
	if st.Track.Title != "" {
		md["title"] = st.Track.Title
	}
	if st.Track.Artist != "" {
		md["artist_name"] = st.Track.Artist
	}
	if st.Track.Album != "" {
		md["album_title"] = st.Track.Album
	}
	if st.Track.Duration > 0 {
		md["duration_ms"] = strconv.FormatInt(st.Track.Duration.Milliseconds(), 10)
	}
	return md
}

func (r *connectReceiver) deviceInfo() *connectpb.DeviceInfo {
	st, _ := r.snapshot()
	vol := uint32(math.Round(playback.DBToLinear(st.VolumeDB) * librespotPlayer.MaxStateVolume))

	di := &connectpb.DeviceInfo{
		CanPlay:               true,
		Volume:                vol,
		Name:                  r.cfg.deviceName(),
		DeviceId:              r.s.deviceID(),
		DeviceType:            devicespb.DeviceType_COMPUTER,
		DeviceSoftwareVersion: appmeta.Version(),
		ClientId:              librespot.ClientIdHex,
		SpircVersion:          "3.2.6",
		Brand:                 "cliamp",
		Model:                 "cliamp",
		License:               "premium",
		Capabilities: &connectpb.Capabilities{
			CanBePlayer:                true,
			RestrictToLocal:            false,
			GaiaEqConnectId:            true,
			SupportsLogout:             true,
			IsObservable:               true,
			VolumeSteps:                int32(librespotPlayer.MaxStateVolume),
			SupportedTypes:             []string{"audio/track", "audio/episode", "audio/media"},
			CommandAcks:                true,
			SupportsRename:             false,
			Hidden:                     false,
			DisableVolume:              false,
			ConnectDisabled:            false,
			SupportsPlaylistV2:         true,
			IsControllable:             true,
			SupportsExternalEpisodes:   false,
			SupportsSetBackendMetadata: true,
			SupportsTransferCommand:    true,
			SupportsCommandRequest:     true,
			IsVoiceEnabled:             false,
			NeedsFullPlayerState:       false,
			SupportsGzipPushes:         true,
			SupportsSetOptionsCommand:  true,
			SupportsDj:                 true,
			SupportsRemoteSleepTimer:   true,
		},
	}
	di.MetadataMap = map[string]string{"tier1_port": "0"}
	if mask := deviceAddressMask(); mask != "" {
		di.MetadataMap["device_address_mask"] = mask
	}
	return di
}

// deviceAddressMask reports this device's own address in CIDR form — the
// interface address and prefix length, not the network address — matching the
// official client. Only IPv4; empty when nothing suitable is found.
func deviceAddressMask() string {
	var local net.IP
	if conn, err := net.Dial("udp4", "192.0.2.1:9"); err == nil {
		if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
			local = addr.IP
		}
		_ = conn.Close()
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var fallback string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP.To4() == nil {
				continue
			}
			ones, _ := ipNet.Mask.Size()
			cidr := fmt.Sprintf("%s/%d", ipNet.IP.To4(), ones)
			if local != nil && ipNet.IP.Equal(local) {
				return cidr
			} else if fallback == "" {
				fallback = cidr
			}
		}
	}
	return fallback
}

// providedToPlaylist converts a resolved context track into a playlist.Track.
// Path stays the canonical spotify: URI so the player routes it through
// go-librespot streaming.
func providedToPlaylist(t *connectpb.ProvidedTrack) playlist.Track {
	md := t.Metadata
	dur, _ := strconv.Atoi(md["duration_ms"])
	return playlist.Track{
		Path:         t.Uri,
		Title:        md["title"],
		Artist:       md["artist_name"],
		Album:        md["album_title"],
		DurationSecs: dur / 1000,
		Unplayable:   len(t.Removed) > 0 || len(t.Blocked) > 0 || len(t.DisallowReasons) > 0,
	}
}

// enrichTracks fills missing title/artist/album for spotify: tracks and
// episodes via the Web API (which the dealer context pages omit). Best-effort:
// on failure the tracks stay playable, just sparse.
func (r *connectReceiver) enrichTracks(trs []playlist.Track) {
	var trackIDs, epIDs []string
	var trackIdx, epIdx []int
	for i := range trs {
		if trs[i].Title != "" {
			continue
		}
		typ, id, ok := splitSpotifyURI(trs[i].Path)
		if !ok {
			continue
		}
		switch typ {
		case "track":
			trackIDs = append(trackIDs, id)
			trackIdx = append(trackIdx, i)
		case "episode":
			epIDs = append(epIDs, id)
			epIdx = append(epIdx, i)
		}
	}
	r.enrichBatch(trs, trackIDs, trackIdx, "/v1/tracks", "tracks")
	r.enrichBatch(trs, epIDs, epIdx, "/v1/episodes", "episodes")
}

func (r *connectReceiver) enrichBatch(trs []playlist.Track, ids []string, idx []int, path, key string) {
	for start := 0; start < len(ids); start += connectEnrichBatch {
		end := min(start+connectEnrichBatch, len(ids))
		chunk := ids[start:end]
		q := url.Values{"ids": {strings.Join(chunk, ",")}}
		resp, err := r.s.webApiWithBody(r.ctx, http.MethodGet, path, q, nil, "")
		if err != nil {
			return
		}
		var out map[string][]*spotifyItem
		err = json.NewDecoder(http.MaxBytesReader(nil, resp.Body, maxResponseBody)).Decode(&out)
		resp.Body.Close()
		if err != nil {
			return
		}
		byID := map[string]playlist.Track{}
		for _, item := range out[key] {
			if item == nil {
				continue
			}
			byID[item.ID] = trackFromItem(item)
		}
		for j, id := range chunk {
			t, ok := byID[id]
			if !ok {
				continue
			}
			dst := &trs[idx[start+j]]
			if dst.Title == "" {
				dst.Title = t.Title
			}
			if dst.Artist == "" {
				dst.Artist = t.Artist
			}
			if dst.Album == "" {
				dst.Album = t.Album
			}
			if dst.AlbumArtURL == "" {
				dst.AlbumArtURL = t.AlbumArtURL
			}
			if dst.DurationSecs == 0 {
				dst.DurationSecs = t.DurationSecs
			}
			dst.Unplayable = dst.Unplayable || t.Unplayable
		}
	}
}

// splitSpotifyURI splits "spotify:<type>:<base62>" URIs.
func splitSpotifyURI(uri string) (typ, id string, ok bool) {
	rest, ok := strings.CutPrefix(uri, "spotify:")
	if !ok {
		return "", "", false
	}
	t, id, ok := strings.Cut(rest, ":")
	if !ok || id == "" {
		return "", "", false
	}
	return t, id, true
}

// contextMetadata merges the context's own metadata with the resolved track
// list's metadata (context name, image, owner).
func contextMetadata(ctxMeta, listMeta map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range listMeta {
		out[k] = v
	}
	for k, v := range ctxMeta {
		out[k] = v
	}
	return out
}

func connectSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

func connectPlaybackID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// connectNotifier is the playback.Notifier handed to the TUI; it forwards
// snapshots into the Session's receiver when one is running.
type connectNotifier struct {
	p *SpotifyProvider
}

func (n *connectNotifier) session() *Session {
	n.p.mu.Lock()
	defer n.p.mu.Unlock()
	return n.p.session
}

func (n *connectNotifier) Update(st playback.State) {
	if s := n.session(); s != nil {
		s.notifyConnect(st)
	}
}

func (n *connectNotifier) Seeked(pos time.Duration) {
	if s := n.session(); s != nil {
		s.connectSeeked(pos)
	}
}

// EnableConnect turns on the Connect receiver with cfg and returns the
// notifier the TUI should attach via model.AttachNotifier. It is safe to call
// before a session exists: the receiver starts when one materializes, and an
// eager silent sign-in is attempted so the device appears without the user
// first opening the Spotify provider pane.
func (p *SpotifyProvider) EnableConnect(cfg ConnectConfig) playback.Notifier {
	p.mu.Lock()
	p.connectCfg = &cfg
	sess := p.session
	p.mu.Unlock()

	if sess != nil {
		sess.StartConnect(cfg)
	} else {
		go func() {
			if err := p.ensureSession(); err != nil {
				applog.Debug("spotify: connect: waiting for sign-in (%v)", err)
			}
		}()
	}
	return &connectNotifier{p: p}
}
