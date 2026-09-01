package model

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/theme"
	"github.com/bjarneo/cliamp/ui"
)

const (
	ipcRuntimeEventState    = "runtime.state"
	ipcRuntimeEventPlayback = "runtime.playback"
	ipcRuntimeEventPlaylist = "runtime.playlist"
	ipcRuntimeEventSettings = "runtime.settings"
)

// ipcRuntimeState is deliberately owned by the Bubble Tea update loop. Its
// pointer survives Model's value-receiver copies and keeps event revisions
// monotonic regardless of whether a change originated from IPC, MPRIS, Lua, or
// a keypress.
type ipcRuntimeState struct {
	broker      *ipc.Broker
	revision    uint64
	initialized bool
	last        ipcRuntimeFingerprint
}

type ipcRuntimeFingerprint struct {
	playlistRevision uint64
	state            string
	trackPath        string
	logicalPath      string
	detached         bool
	index            int
	total            int
	playNextTotal    int
	volume           float64
	shuffle          bool
	repeat           string
	mono             bool
	speed            float64
	eq               [10]float64
	eqPreset         string
	visualizer       string
	theme            string
	streamTitle      string
	streamError      string
}

// SetIPCBroker enables GUI-facing V2 state events. It must be called before
// constructing the Bubble Tea program.
func (m *Model) SetIPCBroker(broker *ipc.Broker) {
	if broker == nil {
		m.ipcRuntime = nil
		return
	}
	m.ipcRuntime = &ipcRuntimeState{broker: broker}
}

// V2RequestMsg is sent by the IPC server to the Bubble Tea owner. JobID is
// empty for read-only requests that need an immediate reply.
type V2RequestMsg struct {
	Request ipc.V2Request
	Jobs    *ipc.JobStore
	JobID   string
	Reply   chan V2RequestResult
}

// V2RequestResult carries an owner-produced response back to the IPC socket.
type V2RequestResult struct {
	Result ipc.V2Result
	Error  *ipc.V2Error
}

type ipcV2ResponseMsg struct {
	Jobs      *ipc.JobStore
	JobID     string
	Operation string
	Response  ipc.Response
}

func (m *Model) handleV2Request(msg V2RequestMsg) tea.Cmd {
	switch strings.ToLower(strings.TrimSpace(msg.Request.Method)) {
	case "state.get":
		snapshot := m.runtimeSnapshot()
		m.replyV2(msg.Reply, ipc.V2Result{Snapshot: &snapshot}, nil)
		return nil
	case "spectrum.get":
		response := m.v2BandsResponse()
		m.replyV2(msg.Reply, ipc.V2Result{Result: marshalV2Result(response)}, nil)
		return nil
	}

	if msg.Jobs == nil || msg.JobID == "" {
		m.replyV2(msg.Reply, ipc.V2Result{}, v2InternalError())
		return nil
	}
	ctx, err := msg.Jobs.Start(msg.JobID)
	if err != nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return nil
	}

	request, protocolErr := v2OperationRequest(msg.Request)
	if protocolErr != nil {
		m.failV2Job(msg.Jobs, msg.JobID, protocolErr)
		return nil
	}
	if request.Revision != 0 && m.playlist != nil && request.Revision != m.playlist.Revision() && v2MutatesLivePlaylist(request.Cmd) {
		m.failV2Job(msg.Jobs, msg.JobID, v2ConflictError())
		return nil
	}

	switch request.Cmd {
	case "play":
		var cmd tea.Cmd
		if !m.player.IsPlaying() || m.player.IsPaused() {
			cmd = m.togglePlayPause()
		}
		m.notifyAll()
		m.completeV2Job(msg.Jobs, msg.JobID, ipc.Response{OK: true})
		return cmd
	case "pause":
		if m.player.IsPlaying() && !m.player.IsPaused() {
			m.togglePlayerPause()
			m.notifyAll()
		}
		m.completeV2Job(msg.Jobs, msg.JobID, ipc.Response{OK: true})
		return nil
	case "toggle":
		cmd := m.togglePlayPause()
		m.notifyAll()
		m.completeV2Job(msg.Jobs, msg.JobID, ipc.Response{OK: true})
		return cmd
	case "stop":
		m.player.Stop()
		m.clearPlaybackTrack()
		m.notifyAll()
		m.completeV2Job(msg.Jobs, msg.JobID, ipc.Response{OK: true})
		return nil
	case "next":
		m.scrobbleCurrent()
		cmd := m.nextTrack()
		m.notifyAll()
		m.completeV2Job(msg.Jobs, msg.JobID, ipc.Response{OK: true})
		return cmd
	case "prev":
		m.scrobbleCurrent()
		cmd := m.prevTrack()
		m.notifyAll()
		m.completeV2Job(msg.Jobs, msg.JobID, ipc.Response{OK: true})
		return cmd
	case "volume":
		m.player.SetVolume(request.Value)
		m.notifyAll()
		m.completeV2Job(msg.Jobs, msg.JobID, ipc.Response{OK: true, Volume: m.player.Volume()})
		return nil
	case "volume.adjust":
		m.player.SetVolume(m.player.Volume() + request.Value)
		m.notifyAll()
		m.completeV2Job(msg.Jobs, msg.JobID, ipc.Response{OK: true, Volume: m.player.Volume()})
		return nil
	case "seek":
		_ = m.player.Seek(secondsDuration(request.Value))
		m.notifyAll()
		m.completeV2Job(msg.Jobs, msg.JobID, ipc.Response{OK: true})
		return nil
	case "seek.absolute":
		position, _ := m.player.PositionAndDuration()
		_ = m.player.Seek(secondsDuration(request.Value) - position)
		m.notifyAll()
		m.completeV2Job(msg.Jobs, msg.JobID, ipc.Response{OK: true})
		return nil
	case "speed":
		if request.Value <= 0 || math.IsNaN(request.Value) || math.IsInf(request.Value, 0) {
			m.failV2Job(msg.Jobs, msg.JobID, v2InvalidParamsError())
			return nil
		}
		m.player.SetSpeed(request.Value)
		m.saveSpeed()
		m.completeV2Job(msg.Jobs, msg.JobID, ipc.Response{OK: true, Speed: m.player.Speed()})
		return nil
	case "speed.adjust":
		m.player.SetSpeed(m.player.Speed() + request.Value)
		m.saveSpeed()
		m.completeV2Job(msg.Jobs, msg.JobID, ipc.Response{OK: true, Speed: m.player.Speed()})
		return nil
	case "queue", "track.play", "track.queue", "queue.play", "queue.enqueue", "queue.remove", "queue.move", "queue.clear", "queue.list":
		return m.handleV2QueueRequest(msg.Jobs, msg.JobID, request)
	case "playnext.list", "playnext.remove", "playnext.move", "playnext.clear":
		return m.handleV2PlayNext(msg.Jobs, msg.JobID, request)
	case "theme":
		return m.handleV2Theme(msg.Jobs, msg.JobID, request)
	case "vis":
		return m.handleV2Visualizer(msg.Jobs, msg.JobID, request)
	case "device":
		return m.handleV2Device(msg.Jobs, msg.JobID, request)
	case "eq":
		return m.handleV2EQ(msg.Jobs, msg.JobID, request)
	case "shuffle":
		return m.handleV2Mode(msg.Jobs, msg.JobID, request)
	case "repeat", "mono":
		return m.handleV2Mode(msg.Jobs, msg.JobID, request)
	case "load":
		request.Cmd = "provider.load"
		request.Provider = "local"
		return m.handleV2LibraryRequest(ctx, msg.Jobs, msg.JobID, request)
	case "url.load", "save", "lyrics", "history", "history.clear":
		return m.handleV2DeferredRequest(ctx, msg.Jobs, msg.JobID, request)
	}
	if isV2LibraryOperation(request.Cmd) {
		return m.handleV2LibraryRequest(ctx, msg.Jobs, msg.JobID, request)
	}
	m.failV2Job(msg.Jobs, msg.JobID, v2UnavailableError())
	return nil
}

func (m *Model) handleV2QueueRequest(jobs *ipc.JobStore, jobID string, request ipc.Request) tea.Cmd {
	if request.Cmd == "queue" {
		if request.Path == "" {
			m.failV2Job(jobs, jobID, v2InvalidParamsError())
			return nil
		}
		track := playlist.TrackFromPath(request.Path)
		m.playlist.Add(track)
		m.loadedPlaylist = ""
		m.addToHeaderState([]playlist.Track{track})
		m.completeV2Job(jobs, jobID, m.v2PlaylistResponse())
		return nil
	}
	if request.Cmd == "queue.list" {
		m.completeV2Job(jobs, jobID, m.v2PlaylistResponsePage(request.Offset, request.Limit))
		return nil
	}
	if request.Cmd == "track.play" || request.Cmd == "track.queue" {
		if request.Track == nil || request.Track.Path == "" {
			m.failV2Job(jobs, jobID, v2InvalidParamsError())
			return nil
		}
		track := ipcTrackFromInfo(*request.Track)
		if request.Cmd == "track.play" {
			cmd := m.playTrackImmediate(track)
			m.completeV2Job(jobs, jobID, m.v2PlaylistResponse())
			return cmd
		}
		cmd := m.queueTrackNext(track)
		m.completeV2Job(jobs, jobID, m.v2PlaylistResponse())
		return cmd
	}

	switch request.Cmd {
	case "queue.play":
		if request.Index < 0 || request.Index >= m.playlist.Len() {
			m.failV2Job(jobs, jobID, v2InvalidParamsError())
			return nil
		}
		m.playlist.SetIndex(request.Index)
		m.plCursor = request.Index
		cmd := m.playCurrentTrack()
		m.completeV2Job(jobs, jobID, m.v2PlaylistResponse())
		return cmd
	case "queue.enqueue":
		if request.Index < 0 || request.Index >= m.playlist.Len() {
			m.failV2Job(jobs, jobID, v2InvalidParamsError())
			return nil
		}
		m.playlist.Queue(request.Index)
		m.normalizeQueueOverlay()
	case "queue.remove":
		if request.Index < 0 || request.Index >= m.playlist.Len() {
			m.failV2Job(jobs, jobID, v2InvalidParamsError())
			return nil
		}
		if request.Index == m.playlist.Index() {
			m.player.Stop()
			m.clearPlaybackTrack()
		}
		if !m.playlist.Remove(request.Index) {
			m.failV2Job(jobs, jobID, v2InvalidParamsError())
			return nil
		}
		m.setHeaderStateFromTracks(m.playlist.Tracks())
		m.normalizeQueueOverlay()
	case "queue.move":
		if !m.playlist.Move(request.Index, request.To) {
			m.failV2Job(jobs, jobID, v2InvalidParamsError())
			return nil
		}
		m.setHeaderStateFromTracks(m.playlist.Tracks())
		m.normalizeQueueOverlay()
	case "queue.clear":
		m.player.Stop()
		m.replacePlaylist(nil)
		m.clearPlaybackTrack()
		m.loadedPlaylist = ""
		m.setHeaderStateFromTracks(nil)
	}
	m.completeV2Job(jobs, jobID, m.v2PlaylistResponse())
	return nil
}

func (m *Model) handleV2PlayNext(jobs *ipc.JobStore, jobID string, request ipc.Request) tea.Cmd {
	switch request.Cmd {
	case "playnext.list":
		m.completeV2Job(jobs, jobID, m.v2PlayNextResponsePage(request.Offset, request.Limit))
		return nil
	case "playnext.remove":
		if request.Index < 0 || request.Index >= m.playlist.QueueLen() {
			m.failV2Job(jobs, jobID, v2InvalidParamsError())
			return nil
		}
		m.playlist.RemoveQueueAt(request.Index)
	case "playnext.move":
		if !m.playlist.MoveQueue(request.Index, request.To) {
			m.failV2Job(jobs, jobID, v2InvalidParamsError())
			return nil
		}
	case "playnext.clear":
		m.playlist.ClearQueue()
	}
	m.normalizeQueueOverlay()
	m.completeV2Job(jobs, jobID, m.v2PlayNextResponse())
	return nil
}

func (m *Model) handleV2Theme(jobs *ipc.JobStore, jobID string, request ipc.Request) tea.Cmd {
	if strings.EqualFold(request.Name, "list") {
		items := make([]string, 0, len(m.themes)+1)
		items = append(items, theme.DefaultName)
		for _, entry := range m.themes {
			items = append(items, entry.Name)
		}
		m.completeV2Job(jobs, jobID, ipc.Response{OK: true, Items: items})
		return nil
	}
	m.themes = theme.LoadAll()
	if !m.SetTheme(request.Name) {
		m.failV2Job(jobs, jobID, v2NotFoundError())
		return nil
	}
	themeName := request.Name
	if strings.EqualFold(themeName, theme.DefaultName) {
		themeName = ""
	}
	if err := m.configSaver.Save("theme", fmt.Sprintf("%q", themeName)); err != nil {
		m.failV2Job(jobs, jobID, v2InternalError())
		return nil
	}
	m.completeV2Job(jobs, jobID, ipc.Response{OK: true})
	return nil
}

func (m *Model) handleV2Visualizer(jobs *ipc.JobStore, jobID string, request ipc.Request) tea.Cmd {
	if strings.EqualFold(request.Name, "list") {
		m.completeV2Job(jobs, jobID, ipc.Response{OK: true, Items: ui.VisModeNames()})
		return nil
	}
	if m.vis == nil {
		m.failV2Job(jobs, jobID, v2UnavailableError())
		return nil
	}
	if strings.EqualFold(request.Name, "next") {
		m.vis.CycleMode()
		m.vis.RequestRefresh()
		m.refreshChrome()
		m.completeV2Job(jobs, jobID, ipc.Response{OK: true, Visualizer: m.vis.ModeName()})
		return nil
	}
	if !m.SetVisualizer(request.Name) {
		m.failV2Job(jobs, jobID, v2NotFoundError())
		return nil
	}
	m.completeV2Job(jobs, jobID, ipc.Response{OK: true, Visualizer: m.vis.ModeName()})
	return nil
}

func (m *Model) handleV2Device(jobs *ipc.JobStore, jobID string, request ipc.Request) tea.Cmd {
	if strings.EqualFold(request.Name, "list") {
		return func() tea.Msg {
			devices, err := player.ListAudioDevices()
			if err != nil {
				return ipcV2ResponseMsg{Jobs: jobs, JobID: jobID, Operation: "device", Response: ipc.Response{OK: false, Error: err.Error()}}
			}
			items := make([]ipc.DeviceInfo, len(devices))
			for i, device := range devices {
				items[i] = ipc.DeviceInfo{Name: device.Name, Active: device.Active}
			}
			return ipcV2ResponseMsg{Jobs: jobs, JobID: jobID, Operation: "device", Response: ipc.Response{OK: true, Devices: items}}
		}
	}
	return func() tea.Msg {
		err := player.SwitchAudioDevice(request.Name)
		response := ipc.Response{OK: true, Device: request.Name}
		if err != nil {
			response = ipc.Response{OK: false, Error: err.Error()}
		}
		return ipcV2ResponseMsg{Jobs: jobs, JobID: jobID, Operation: "device", Response: response}
	}
}

func (m *Model) handleV2EQ(jobs *ipc.JobStore, jobID string, request ipc.Request) tea.Cmd {
	if request.Band > 0 || (request.Band == 0 && request.Name == "") {
		if request.Band >= eqBandCount {
			m.failV2Job(jobs, jobID, v2InvalidParamsError())
			return nil
		}
		m.setCustomEQBand(request.Band, request.Value)
	} else if request.Name != "" {
		m.SetEQPreset(request.Name, nil)
		m.scheduleEQSave()
	} else {
		m.failV2Job(jobs, jobID, v2InvalidParamsError())
		return nil
	}
	m.completeV2Job(jobs, jobID, ipc.Response{OK: true, EQPreset: m.EQPresetName()})
	return nil
}

func (m *Model) handleV2Mode(jobs *ipc.JobStore, jobID string, request ipc.Request) tea.Cmd {
	name := strings.ToLower(request.Name)
	switch request.Cmd {
	case "shuffle":
		if (name == "on" && !m.playlist.Shuffled()) || (name == "off" && m.playlist.Shuffled()) || (name != "on" && name != "off") {
			m.playlist.ToggleShuffle()
		}
		value := m.playlist.Shuffled()
		_ = m.configSaver.Save("shuffle", fmt.Sprintf("%v", value))
		m.player.ClearPreload()
		m.completeV2Job(jobs, jobID, ipc.Response{OK: true, Shuffle: &value})
	case "repeat":
		switch name {
		case "off":
			m.playlist.SetRepeat(playlist.RepeatOff)
		case "all":
			m.playlist.SetRepeat(playlist.RepeatAll)
		case "one":
			m.playlist.SetRepeat(playlist.RepeatOne)
		default:
			m.playlist.CycleRepeat()
		}
		_ = m.configSaver.Save("repeat", fmt.Sprintf("%q", m.playlist.Repeat().String()))
		m.player.ClearPreload()
		m.completeV2Job(jobs, jobID, ipc.Response{OK: true, Repeat: m.playlist.Repeat().String()})
	case "mono":
		if (name == "on" && !m.player.Mono()) || (name == "off" && m.player.Mono()) || (name != "on" && name != "off") {
			m.player.ToggleMono()
		}
		value := m.player.Mono()
		m.completeV2Job(jobs, jobID, ipc.Response{OK: true, Mono: &value})
	}
	return nil
}

func (m *Model) handleV2LibraryRequest(ctx context.Context, jobs *ipc.JobStore, jobID string, request ipc.Request) tea.Cmd {
	reply := make(chan ipc.Response, 1)
	cmd := m.handleIPCLibrary(ipc.LibraryRequestMsg{
		Op: request.Cmd, Provider: request.Provider, Playlist: request.Playlist, Query: request.Query,
		Artist: request.Artist, Album: request.Album, Sort: request.Sort, Offset: request.Offset,
		Limit: request.Limit, Index: request.Index, NewName: request.NewName, Track: request.Track, Tracks: request.Tracks, Context: ctx, Reply: reply,
	})
	return tea.Batch(cmd, waitV2ResponseCmd(ctx, jobs, jobID, reply))
}

func (m *Model) handleV2DeferredRequest(ctx context.Context, jobs *ipc.JobStore, jobID string, request ipc.Request) tea.Cmd {
	reply := make(chan ipc.Response, 1)
	var cmd tea.Cmd
	switch request.Cmd {
	case "url.load":
		cmd = m.handleIPCURL(ipc.URLRequestMsg{URL: request.Path, Play: request.Play, Context: ctx, Reply: reply})
	case "save":
		cmd = m.handleIPCSave(ipc.SaveRequestMsg{Reply: reply})
	case "lyrics":
		cmd = m.handleIPCLyrics(ipc.LyricsRequestMsg{Reply: reply})
	default:
		cmd = m.handleIPCHistory(ipc.HistoryRequestMsg{Op: request.Cmd, Limit: request.Limit, Reply: reply})
	}
	return tea.Batch(cmd, waitV2ResponseCmd(ctx, jobs, jobID, reply))
}

func waitV2ResponseCmd(ctx context.Context, jobs *ipc.JobStore, jobID string, reply <-chan ipc.Response) tea.Cmd {
	return func() tea.Msg {
		select {
		case response := <-reply:
			return ipcV2ResponseMsg{Jobs: jobs, JobID: jobID, Response: response}
		case <-ctx.Done():
			return nil
		}
	}
}

func (m *Model) completeV2Job(jobs *ipc.JobStore, jobID string, result any) {
	if jobs == nil || jobID == "" {
		return
	}
	ctx, ok := jobs.Context(jobID)
	if !ok || ctx.Err() != nil {
		return
	}
	m.publishIPCRuntimeState()
	_ = jobs.SucceedWithSnapshot(jobID, marshalV2Result(result), m.runtimeSnapshot())
}

func (m *Model) failV2Job(jobs *ipc.JobStore, jobID string, err *ipc.V2Error) {
	if jobs == nil || jobID == "" || err == nil {
		return
	}
	_ = jobs.Fail(jobID, *err)
}

func (m *Model) replyV2(reply chan V2RequestResult, result ipc.V2Result, err *ipc.V2Error) {
	if reply != nil {
		reply <- V2RequestResult{Result: result, Error: err}
	}
}

func (m *Model) runtimeSnapshot() ipc.RuntimeSnapshot {
	snapshot := ipc.RuntimeSnapshot{}
	if m.ipcRuntime != nil {
		snapshot.Revision = m.ipcRuntime.revision
	}
	if m.playlist != nil {
		snapshot.PlaylistRevision = m.playlist.Revision()
		snapshot.Index = m.playlist.Index()
		snapshot.Total = m.playlist.Len()
		snapshot.PlayNextTotal = m.playlist.QueueLen()
		shuffled := m.playlist.Shuffled()
		snapshot.Shuffle = &shuffled
		snapshot.Repeat = m.playlist.Repeat().String()
		if track, index := m.playlist.Current(); index >= 0 {
			info := ipcTrackInfo(track, index, m.playlist.QueuePosition(index))
			snapshot.LogicalTrack = &info
		}
	}
	if m.player == nil {
		return snapshot
	}
	switch {
	case m.player.IsPlaying() && !m.player.IsPaused():
		snapshot.State = "playing"
	case m.player.IsPaused():
		snapshot.State = "paused"
	default:
		snapshot.State = "stopped"
	}
	if track, _ := m.currentPlaybackTrack(); track.Path != "" {
		index := snapshot.Index
		queuePosition := 0
		if m.playlist != nil && index >= 0 {
			queuePosition = m.playlist.QueuePosition(index)
		}
		info := ipcTrackInfo(track, index, queuePosition)
		artist, title := m.resolveTrackDisplay(track)
		if title != "" {
			if track.Stream && title != track.Title {
				info.Station = track.Title
			}
			info.Artist, info.Title = artist, title
		}
		if track.Stream {
			info.StreamTitle = m.streamTitle
		}
		snapshot.Track = &info
	}
	snapshot.PlaybackDetached = m.playbackDetached
	position, duration := m.player.PositionAndDuration()
	snapshot.Position = position.Seconds()
	snapshot.Duration = duration.Seconds()
	snapshot.Seekable = m.player.Seekable()
	snapshot.Volume = m.player.Volume()
	mono := m.player.Mono()
	snapshot.Mono = &mono
	snapshot.Speed = m.player.Speed()
	snapshot.EQPreset = m.EQPresetName()
	bands := m.player.EQBands()
	snapshot.EQBands = append([]float64(nil), bands[:]...)
	if m.vis != nil {
		snapshot.Visualizer = m.vis.ModeName()
	}
	if m.themeIdx >= 0 && m.themeIdx < len(m.themes) {
		entry := m.themes[m.themeIdx]
		snapshot.Theme = &ipc.ThemeInfo{Name: entry.Name, BG: entry.BG, Accent: entry.Accent, Fg: entry.FG, BrightFg: entry.BrightFG, Green: entry.Green, Yellow: entry.Yellow, Red: entry.Red}
	} else {
		snapshot.Theme = &ipc.ThemeInfo{Name: theme.DefaultName}
	}
	if err := m.player.StreamErr(); err != nil {
		snapshot.StreamError = err.Error()
	}
	return snapshot
}

func (m *Model) publishIPCRuntimeState() {
	if m.ipcRuntime == nil || m.ipcRuntime.broker == nil || m.player == nil || m.playlist == nil {
		return
	}
	fingerprint := m.runtimeFingerprint()
	if m.ipcRuntime.initialized && fingerprint == m.ipcRuntime.last {
		return
	}
	m.ipcRuntime.initialized = true
	m.ipcRuntime.last = fingerprint
	m.ipcRuntime.revision++
	snapshot := m.runtimeSnapshot()
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	for _, topic := range []string{ipcRuntimeEventState, ipcRuntimeEventPlayback, ipcRuntimeEventPlaylist, ipcRuntimeEventSettings} {
		_ = m.ipcRuntime.broker.Publish(topic, payload, true)
	}
}

func (m *Model) runtimeFingerprint() ipcRuntimeFingerprint {
	var fingerprint ipcRuntimeFingerprint
	fingerprint.playlistRevision = m.playlist.Revision()
	fingerprint.index = m.playlist.Index()
	fingerprint.total = m.playlist.Len()
	fingerprint.playNextTotal = m.playlist.QueueLen()
	fingerprint.shuffle = m.playlist.Shuffled()
	fingerprint.repeat = m.playlist.Repeat().String()
	fingerprint.volume = m.player.Volume()
	fingerprint.mono = m.player.Mono()
	fingerprint.speed = m.player.Speed()
	fingerprint.eq = m.player.EQBands()
	fingerprint.eqPreset = m.EQPresetName()
	fingerprint.detached = m.playbackDetached
	fingerprint.streamTitle = m.streamTitle
	if m.player.IsPlaying() && !m.player.IsPaused() {
		fingerprint.state = "playing"
	} else if m.player.IsPaused() {
		fingerprint.state = "paused"
	} else {
		fingerprint.state = "stopped"
	}
	if track, _ := m.currentPlaybackTrack(); track.Path != "" {
		fingerprint.trackPath = track.Path
	}
	if track, _ := m.playlist.Current(); track.Path != "" {
		fingerprint.logicalPath = track.Path
	}
	if m.vis != nil {
		fingerprint.visualizer = m.vis.ModeName()
	}
	fingerprint.theme = m.ThemeName()
	if err := m.player.StreamErr(); err != nil {
		fingerprint.streamError = err.Error()
	}
	return fingerprint
}

func (m *Model) v2BandsResponse() ipc.Response {
	response := ipc.Response{OK: true}
	if m.vis != nil {
		response.Visualizer = m.vis.ModeName()
		response.Bands = append([]float64(nil), m.vis.SmoothedBands()...)
	}
	return response
}

func (m *Model) v2PlaylistResponse() ipc.Response {
	return m.v2PlaylistResponsePage(0, 0)
}

func (m *Model) v2PlaylistResponsePage(offset, limit int) ipc.Response {
	tracks := m.playlist.Tracks()
	total := len(tracks)
	if offset < 0 || offset >= total {
		return ipc.Response{OK: true, Index: m.playlist.Index(), Total: total}
	}
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	end := min(total, offset+limit)
	items := make([]ipc.TrackInfo, end-offset)
	for i, track := range tracks[offset:end] {
		index := offset + i
		items[i] = ipcTrackInfo(track, index, m.playlist.QueuePosition(index))
	}
	return ipc.Response{OK: true, Tracks: items, Index: m.playlist.Index(), Total: total}
}

func (m *Model) v2PlayNextResponse() ipc.Response {
	return m.v2PlayNextResponsePage(0, 0)
}

func (m *Model) v2PlayNextResponsePage(offset, limit int) ipc.Response {
	entries := m.playlist.QueueEntries()
	total := len(entries)
	if offset < 0 || offset >= total {
		return ipc.Response{OK: true, Total: total}
	}
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	end := min(total, offset+limit)
	items := make([]ipc.TrackInfo, end-offset)
	for i, entry := range entries[offset:end] {
		items[i] = ipcTrackInfo(entry.Track, entry.TrackIndex, offset+i+1)
	}
	return ipc.Response{OK: true, Tracks: items, Total: total}
}

func v2OperationRequest(request ipc.V2Request) (ipc.Request, *ipc.V2Error) {
	var result ipc.Request
	if len(request.Params) > 0 {
		if err := json.Unmarshal(request.Params, &result); err != nil {
			return ipc.Request{}, v2InvalidParamsError()
		}
	}
	result.Cmd = normalizeV2Operation(request.Operation)
	if result.Cmd == "" {
		return ipc.Request{}, v2InvalidParamsError()
	}
	return result, nil
}

func normalizeV2Operation(operation string) string {
	operation = strings.ToLower(strings.TrimSpace(operation))
	switch operation {
	case "player.play", "runtime.play":
		return "play"
	case "player.pause", "runtime.pause":
		return "pause"
	case "player.toggle", "runtime.toggle":
		return "toggle"
	case "player.stop", "runtime.stop":
		return "stop"
	case "player.next", "runtime.next":
		return "next"
	case "player.prev", "player.previous", "runtime.prev":
		return "prev"
	case "player.volume.set", "runtime.volume":
		return "volume"
	case "player.volume.adjust":
		return "volume.adjust"
	case "player.seek.relative", "runtime.seek":
		return "seek"
	case "player.seek.absolute":
		return "seek.absolute"
	case "player.speed.set", "runtime.speed":
		return "speed"
	case "player.speed.adjust":
		return "speed.adjust"
	case "runtime.playlist.get":
		return "queue.list"
	case "runtime.playlist.play":
		return "queue.play"
	case "runtime.playlist.remove":
		return "queue.remove"
	case "runtime.playlist.move":
		return "queue.move"
	case "runtime.playlist.clear":
		return "queue.clear"
	case "runtime.queue.list":
		return "queue.list"
	case "runtime.queue.play":
		return "queue.play"
	case "runtime.queue.enqueue":
		return "queue.enqueue"
	case "runtime.queue.remove":
		return "queue.remove"
	case "runtime.queue.move":
		return "queue.move"
	case "runtime.queue.clear":
		return "queue.clear"
	case "runtime.library.search":
		return "provider.search"
	case "runtime.history":
		return "history"
	default:
		return operation
	}
}

func isV2LibraryOperation(operation string) bool {
	return strings.HasPrefix(operation, "provider.") || strings.HasPrefix(operation, "playlist.")
}

func v2MutatesLivePlaylist(operation string) bool {
	switch operation {
	case "queue", "queue.play", "queue.enqueue", "queue.remove", "queue.move", "queue.clear", "track.play", "track.queue", "playnext.remove", "playnext.move", "playnext.clear":
		return true
	default:
		return false
	}
}

func secondsDuration(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}

func marshalV2Result(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return data
}

func v2InvalidParamsError() *ipc.V2Error {
	return &ipc.V2Error{Code: ipc.V2ErrorCodeInvalidParams, Message: ipc.V2MessageInvalidParams}
}

func v2NotFoundError() *ipc.V2Error {
	return &ipc.V2Error{Code: ipc.V2ErrorCodeNotFound, Message: ipc.V2MessageNotFound}
}

func v2ConflictError() *ipc.V2Error {
	return &ipc.V2Error{Code: ipc.V2ErrorCodeConflict, Message: ipc.V2MessageConflict}
}

func v2UnavailableError() *ipc.V2Error {
	return &ipc.V2Error{Code: ipc.V2ErrorCodeUnavailable, Message: ipc.V2MessageUnavailable}
}

func v2InternalError() *ipc.V2Error {
	return &ipc.V2Error{Code: ipc.V2ErrorCodeInternal, Message: ipc.V2MessageInternal}
}
