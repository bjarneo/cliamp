package model

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/external/spotify"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
)

type sonosDevicesMsg struct {
	gen     uint64
	devices []player.AudioDevice
	sonos   []spotify.SonosDevice
	err     error
}

func (m Model) sonosTrack() playlist.Track {
	if m.sonosOutput.state == nil {
		return playlist.Track{}
	}
	track := m.sonosOutput.state.Track
	return playlist.Track{Title: track.Title, Artist: track.Artist, Album: track.Album}
}

func sonosDuration(clock string) time.Duration {
	parts := strings.Split(clock, ":")
	if len(parts) != 3 {
		return 0
	}
	hours, err1 := strconv.Atoi(parts[0])
	minutes, err2 := strconv.Atoi(parts[1])
	seconds, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return 0
	}
	return time.Duration(hours*3600+minutes*60+seconds) * time.Second
}

type sonosStateMsg struct {
	gen   uint64
	state spotify.SonosState
	err   error
}

type sonosQueueMsg struct {
	gen   uint64
	state spotify.SonosState
	err   error
}

type sonosControlMsg struct {
	gen        uint64
	request    sonosControlRequest
	pickerGen  uint64
	devices    []spotify.SonosDevice
	refreshErr error
	err        error
}

type sonosHandoffPlan struct {
	target      string
	currentPath string
	position    time.Duration
	trackCount  int
	playlistRev uint64
	streamGen   uint64
	queueGen    uint64
	outputGen   uint64
}

type sonosHandoffAppendedMsg struct {
	gen     uint64
	ctx     context.Context
	plan    sonosHandoffPlan
	segment spotify.SonosQueueRange
	err     error
}

type sonosHandoffStartedMsg struct {
	gen              uint64
	plan             sonosHandoffPlan
	appended         int
	started          bool
	positionRestored bool
	err              error
}

type sonosControlRequest struct {
	action   string
	volume   int
	rooms    []string
	playback string
	ctx      context.Context
	jobs     *ipc.JobStore
	jobID    string
	result   ipc.Response
}

type sonosBridge interface {
	devices(context.Context) ([]spotify.SonosDevice, error)
	state(context.Context, string) (spotify.SonosState, error)
	queue(context.Context, string) (spotify.SonosState, error)
	playQueue(context.Context, string, int) error
	control(context.Context, string, string) error
	volume(context.Context, string, int) error
	setGroup(context.Context, []string, string) error
	separateGroup(context.Context, string) error
	appendSpotifyTracks(context.Context, string, []string) (spotify.SonosQueueRange, error)
	startSpotifyQueue(context.Context, spotify.SonosQueueRange, time.Duration) (bool, bool, error)
}

type nativeSonosBridge struct{}

func (nativeSonosBridge) devices(ctx context.Context) ([]spotify.SonosDevice, error) {
	return spotify.SonosDevices(ctx)
}
func (nativeSonosBridge) state(ctx context.Context, address string) (spotify.SonosState, error) {
	return spotify.ReadSonosTransportState(ctx, address)
}
func (nativeSonosBridge) queue(ctx context.Context, address string) (spotify.SonosState, error) {
	return spotify.ReadSonosState(ctx, address)
}
func (nativeSonosBridge) playQueue(ctx context.Context, address string, index int) error {
	return spotify.SonosPlayFromQueue(ctx, address, index)
}
func (nativeSonosBridge) control(ctx context.Context, action, address string) error {
	return spotify.SonosControl(ctx, action, address, "")
}
func (nativeSonosBridge) volume(ctx context.Context, address string, value int) error {
	return spotify.SonosSetVolume(ctx, address, value)
}
func (nativeSonosBridge) setGroup(ctx context.Context, rooms []string, playback string) error {
	return spotify.SonosSetGroup(ctx, rooms, playback)
}
func (nativeSonosBridge) separateGroup(ctx context.Context, room string) error {
	return spotify.SonosSeparateGroup(ctx, room)
}
func (nativeSonosBridge) appendSpotifyTracks(ctx context.Context, address string, tracks []string) (spotify.SonosQueueRange, error) {
	return spotify.SonosAppendSpotifyTracks(ctx, address, tracks)
}
func (nativeSonosBridge) startSpotifyQueue(ctx context.Context, segment spotify.SonosQueueRange, position time.Duration) (bool, bool, error) {
	return spotify.SonosStartSpotifyQueue(ctx, segment, position)
}

func (m Model) outputBridge() sonosBridge {
	if m.sonosBridge != nil {
		return m.sonosBridge
	}
	return nativeSonosBridge{}
}

func (m *Model) rejectSonosLocalControl(name string) bool {
	if m.sonosOutput.selected == "" {
		return false
	}
	m.status.Warningf(statusTTLDefault, "%s is unavailable for Sonos output", name)
	return true
}

func (m *Model) openOutputPicker() tea.Cmd {
	m.devicePicker.visible = true
	m.devicePicker.loading = true
	m.devicePicker.cursor = 0
	m.devicePicker.scroll = 0
	m.devicePicker.checked = nil
	m.devicePicker.sonos = nil
	m.devicePicker.gen++
	selected := m.sonosOutput.selected
	bridge := m.outputBridge()
	listDevices := m.listAudioDevices
	if listDevices == nil {
		listDevices = player.ListAudioDevices
	}
	m.cancelSonosDiscovery()
	gen := nextRequest(&m.sonosOutput.discoveryGen)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	m.sonosOutput.discoveryCancel = cancel
	return func() tea.Msg {
		defer cancel()
		local, localErr := listDevices()
		devices, remoteErr := bridge.devices(ctx)
		rows := []player.AudioDevice{{Name: "", Description: "Local (system default)"}}
		rows = append(rows, local...)
		for _, device := range devices {
			if device.Satellite {
				continue
			}
			address := device.ID
			if address == "" {
				address = device.Coordinator
			}
			name := device.Name
			if name == "" {
				name = address
			}
			rows = append(rows, player.AudioDevice{Name: "sonos:" + address, Description: "Sonos · " + name, Active: selected == address})
		}
		if remoteErr != nil && localErr != nil {
			return sonosDevicesMsg{gen: gen, devices: rows, err: fmt.Errorf("local: %v; Sonos: %w", localErr, remoteErr)}
		}
		return sonosDevicesMsg{gen: gen, devices: rows, sonos: devices, err: remoteErr}
	}
}

func (m *Model) selectOutput(device player.AudioDevice) tea.Cmd {
	if m.sonosOutput.handoffCancel != nil && !strings.HasPrefix(device.Name, "sonos:") {
		m.cancelSonosHandoff()
	}
	if strings.HasPrefix(device.Name, "sonos:") {
		id := strings.TrimPrefix(device.Name, "sonos:")
		if id == "" {
			return nil
		}
		if m.sonosOutput.selected == "" && m.localMusicActive() {
			return m.beginSonosHandoff(id)
		}
		m.cancelSonosHandoff()
		nextRequest(&m.requests.output)
		m.cancelOutputRequests()
		m.retireLocalPlayback()
		m.sonosOutput.selected = id
		m.sonosOutput.name = strings.TrimPrefix(device.Description, "Sonos · ")
		m.sonosOutput.state = nil
		m.sonosOutput.queue = nil
		m.sonosOutput.queueCursor = 0
		m.sonosOutput.queueStale = false
		m.sonosOutput.refreshed = time.Time{}
		m.sonosOutput.attempted = time.Time{}
		m.fullVis = false
		m.devicePicker.visible = false
		m.recomputeLayout()
		return m.refreshSonosQueue()
	}
	if m.sonosOutput.selected != "" {
		m.cancelSonosHandoff()
		nextRequest(&m.requests.output)
		m.cancelOutputRequests()
		m.retireLocalPlayback()
		m.sonosOutput.selected = ""
		m.sonosOutput.name = ""
		m.sonosOutput.state = nil
		m.cancelSonosQueueRead()
		m.sonosOutput.queue = nil
		m.sonosOutput.queueCursor = 0
		m.sonosOutput.queueStale = false
		m.devicePicker.visible = false
		return switchDeviceCmd(device.Name)
	}
	m.devicePicker.visible = false
	return switchDeviceCmd(device.Name)
}

func (m *Model) localMusicActive() bool {
	return m.buffering || m.player != nil && m.player.IsPlaying()
}

func spotifyTrackURI(path string) bool {
	parts := strings.Split(path, ":")
	if len(parts) != 3 || parts[0] != "spotify" || parts[1] != "track" || len(parts[2]) != 22 {
		return false
	}
	for _, r := range parts[2] {
		if !(r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') {
			return false
		}
	}
	return true
}

func (m *Model) beginSonosHandoff(target string) tea.Cmd {
	tracks := m.playlist.PlaybackTracks()
	current, _ := m.currentPlaybackTrack()
	if len(tracks) == 0 || current.Path == "" || tracks[0].Path != current.Path {
		m.status.Warning("The current track is not in the transferable queue; choose a Spotify track or wait for it to finish. Local playback continues", statusTTLLong)
		return nil
	}
	paths := make([]string, len(tracks))
	for i, track := range tracks {
		if !spotifyTrackURI(track.Path) {
			m.status.Warningf(statusTTLLong, "%s cannot be transferred to Sonos; choose Spotify tracks. Local playback continues", track.DisplayName())
			return nil
		}
		paths[i] = track.Path
	}
	plan := sonosHandoffPlan{
		target: target, currentPath: current.Path, trackCount: len(paths),
		playlistRev: m.playlist.Revision(), streamGen: m.requests.stream,
		queueGen: m.requests.queue, outputGen: m.requests.output,
	}
	if m.sonosOutput.busy {
		m.status.Warning("Sonos is busy; try selecting it again", statusTTLDefault)
		return nil
	}
	m.sonosOutput.busy, m.sonosOutput.busyKind = true, "handoff"
	gen := nextRequest(&m.sonosOutput.handoffGen)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	m.sonosOutput.handoffCancel = cancel
	bridge := m.outputBridge()
	m.devicePicker.visible = false
	return func() tea.Msg {
		segment, err := bridge.appendSpotifyTracks(ctx, target, paths)
		return sonosHandoffAppendedMsg{gen: gen, ctx: ctx, plan: plan, segment: segment, err: err}
	}
}

func (m *Model) handoffCurrent(plan sonosHandoffPlan) bool {
	current, _ := m.currentPlaybackTrack()
	return m.sonosOutput.selected == "" && plan.outputGen == m.requests.output && plan.streamGen == m.requests.stream && plan.queueGen == m.requests.queue && plan.playlistRev == m.playlist.Revision() && current.Path == plan.currentPath && m.localMusicActive()
}

func (m *Model) handleSonosHandoffAppended(msg sonosHandoffAppendedMsg) tea.Cmd {
	if msg.gen != m.sonosOutput.handoffGen {
		if msg.segment.Appended > 0 {
			m.status.Warningf(statusTTLLong, "Handoff canceled; %d appended tracks remain in the Sonos queue", msg.segment.Appended)
		}
		return m.nextSonosControl()
	}
	if msg.err != nil {
		m.finishSonosHandoff()
		m.status.Errorf(statusTTLDefault, "Sonos appended %d of %d tracks; Local playback continues: %s", msg.segment.Appended, msg.plan.trackCount, msg.err)
		return m.nextSonosControl()
	}
	if msg.ctx.Err() != nil || !m.handoffCurrent(msg.plan) {
		m.finishSonosHandoff()
		m.status.Warningf(statusTTLLong, "%d tracks remain appended to Sonos; handoff canceled because local playback changed", msg.segment.Appended)
		return m.nextSonosControl()
	}
	bridge := m.outputBridge()
	plan := msg.plan
	if m.player != nil {
		plan.position = max(0, m.player.Position())
	}
	return func() tea.Msg {
		if err := msg.ctx.Err(); err != nil {
			return sonosHandoffStartedMsg{gen: msg.gen, plan: plan, appended: msg.segment.Appended, err: err}
		}
		started, restored, err := bridge.startSpotifyQueue(msg.ctx, msg.segment, plan.position)
		return sonosHandoffStartedMsg{gen: msg.gen, plan: plan, appended: msg.segment.Appended, started: started, positionRestored: restored, err: err}
	}
}

func (m *Model) finishSonosHandoff() {
	if m.sonosOutput.handoffCancel != nil {
		m.sonosOutput.handoffCancel()
	}
	m.sonosOutput.handoffCancel = nil
	m.sonosOutput.busy, m.sonosOutput.busyKind = false, ""
}

func (m *Model) handleSonosHandoffStarted(msg sonosHandoffStartedMsg) tea.Cmd {
	if msg.gen != m.sonosOutput.handoffGen {
		if msg.started {
			m.status.Warningf(statusTTLLong, "Sonos started %d transferred tracks after the handoff was canceled; newer output selection was kept", msg.appended)
		} else if msg.appended > 0 {
			m.status.Warningf(statusTTLLong, "Handoff canceled; %d appended tracks remain in the Sonos queue", msg.appended)
		}
		return m.nextSonosControl()
	}
	if !m.handoffCurrent(msg.plan) {
		m.finishSonosHandoff()
		if msg.started {
			m.status.Warning("Sonos started the transferred tracks after local playback changed", statusTTLLong)
		} else {
			m.status.Warningf(statusTTLLong, "%d tracks remain appended to Sonos; Local playback continues", msg.appended)
		}
		return m.nextSonosControl()
	}
	if !msg.started {
		m.finishSonosHandoff()
		err := msg.err
		if err == nil {
			err = fmt.Errorf("playback start was not confirmed")
		}
		m.status.Errorf(statusTTLDefault, "Sonos playback was not confirmed; %d tracks remain appended and Local playback continues: %s", msg.appended, err)
		return m.nextSonosControl()
	}
	m.finishSonosHandoff()
	nextRequest(&m.requests.output)
	m.cancelSonosDiscovery()
	m.cancelSonosQueueRead()
	m.retireLocalPlayback()
	m.sonosOutput.selected, m.sonosOutput.name = msg.plan.target, msg.plan.target
	m.sonosOutput.state, m.sonosOutput.queue = nil, nil
	m.sonosOutput.queueStale = false
	m.devicePicker.visible = false
	m.recomputeLayout()
	if msg.err != nil || !msg.positionRestored {
		m.status.Warningf(statusTTLLong, "Transferred %d tracks to Sonos; it could not restore the current position", msg.appended)
	} else {
		m.status.Showf(statusTTLLong, "Transferred %d tracks to Sonos", msg.appended)
	}
	if cmd := m.nextSonosControl(); cmd != nil {
		return cmd
	}
	return m.refreshSonosQueue()
}

func (m *Model) cancelSonosHandoff() {
	if m.sonosOutput.handoffCancel == nil {
		return
	}
	m.sonosOutput.handoffCancel()
	m.sonosOutput.handoffCancel = nil
	m.sonosOutput.handoffGen++
	m.sonosOutput.busy, m.sonosOutput.busyKind = false, ""
}

func (m *Model) nextSonosControl() tea.Cmd {
	if m.sonosOutput.busy {
		return nil
	}
	for len(m.sonosOutput.pending) > 0 {
		request := m.sonosOutput.pending[0]
		m.sonosOutput.pending = m.sonosOutput.pending[1:]
		if request.ctx == nil || request.ctx.Err() == nil {
			return m.startSonosControl(request)
		}
	}
	return nil
}

func (m *Model) retireLocalPlayback() {
	m.stopPlayback()
	m.player.ClearPreload()
	m.preloading = false
}

func (m *Model) cancelSonosRequest() {
	if m.sonosOutput.cancel != nil {
		m.sonosOutput.cancel()
		m.sonosOutput.cancel = nil
	}
	m.sonosOutput.gen++
	m.sonosOutput.busy = false
	m.sonosOutput.busyKind = ""
	if active := m.sonosOutput.active; active != nil && active.jobs != nil {
		m.failV2Job(active.jobs, active.jobID, v2UnavailableControlError("Sonos output changed before control completed"))
	}
	m.sonosOutput.active = nil
	for _, request := range m.sonosOutput.pending {
		if request.jobs != nil {
			m.failV2Job(request.jobs, request.jobID, v2UnavailableControlError("Sonos output changed before control completed"))
		}
	}
	m.sonosOutput.pending = nil
}

func (m *Model) cancelOutputRequests() {
	m.cancelSonosHandoff()
	m.cancelSonosRequest()
	m.cancelSonosDiscovery()
	m.cancelSonosQueueRead()
}

func (m *Model) cancelSonosQueueRead() {
	if m.sonosOutput.queueCancel != nil {
		m.sonosOutput.queueCancel()
		m.sonosOutput.queueCancel = nil
	}
	m.sonosOutput.queueGen++
	m.sonosOutput.queueLoading = false
}

func (m *Model) refreshSonosQueue() tea.Cmd {
	if m.sonosOutput.selected == "" || m.sonosOutput.busy || m.sonosOutput.queueLoading {
		return nil
	}
	m.cancelSonosQueueRead()
	gen, address := m.sonosOutput.queueGen, m.sonosOutput.selected
	bridge := m.outputBridge()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	m.sonosOutput.queueCancel = cancel
	m.sonosOutput.queueLoading = true
	return func() tea.Msg {
		defer cancel()
		state, err := bridge.queue(ctx, address)
		return sonosQueueMsg{gen: gen, state: state, err: err}
	}
}

func (m *Model) handleSonosQueue(msg sonosQueueMsg) {
	if msg.gen != m.sonosOutput.queueGen || m.sonosOutput.selected == "" {
		return
	}
	m.sonosOutput.queueCancel = nil
	m.sonosOutput.queueLoading = false
	if msg.err != nil {
		m.sonosOutput.queueStale = true
		m.status.Errorf(statusTTLDefault, "Sonos queue: %s", msg.err)
		return
	}
	hadSnapshot := m.sonosOutput.queue != nil
	m.sonosOutput.queue = &msg.state
	m.sonosOutput.queueStale = false
	if m.sonosOutput.state == nil && !m.sonosOutput.busy {
		m.handleSonosState(sonosStateMsg{gen: m.sonosOutput.gen, state: msg.state})
	}
	if !hadSnapshot && msg.state.Source == "Sonos queue" && msg.state.QueueIndex >= 0 && msg.state.QueueIndex < len(msg.state.Queue) {
		m.sonosOutput.queueCursor = msg.state.QueueIndex
	} else if m.sonosOutput.queueCursor >= len(msg.state.Queue) {
		m.sonosOutput.queueCursor = max(0, len(msg.state.Queue)-1)
	}
}

func (m *Model) playSonosQueueItem(index int) tea.Cmd {
	state := m.sonosOutput.queue
	if m.sonosOutput.busy || m.sonosOutput.selected == "" || m.sonosOutput.state == nil || m.sonosOutput.state.Source != "Sonos queue" || state == nil || state.Source != "Sonos queue" || m.sonosOutput.queueStale || index < 0 || index >= len(state.Queue) {
		m.status.Warning("Saved Sonos queue item is unavailable", statusTTLDefault)
		return nil
	}
	if m.sonosOutput.cancel != nil {
		m.sonosOutput.cancel()
	}
	m.cancelSonosQueueRead()
	gen, address, bridge := m.sonosOutput.gen, m.sonosOutput.selected, m.outputBridge()
	m.sonosOutput.busy = true
	m.sonosOutput.busyKind = "control"
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	m.sonosOutput.cancel = cancel
	return func() tea.Msg {
		defer cancel()
		err := bridge.playQueue(ctx, address, index)
		return sonosControlMsg{gen: gen, request: sonosControlRequest{action: "play saved queue item"}, err: err}
	}
}

func (m *Model) cancelSonosDiscovery() {
	if m.sonosOutput.discoveryCancel != nil {
		m.sonosOutput.discoveryCancel()
		m.sonosOutput.discoveryCancel = nil
	}
	m.sonosOutput.discoveryGen++
}

func (m *Model) refreshSonosState() tea.Cmd {
	if m.sonosOutput.selected == "" || m.sonosOutput.busy || m.sonosOutput.queueLoading {
		return nil
	}
	if m.sonosOutput.cancel != nil {
		m.sonosOutput.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	m.sonosOutput.cancel = cancel
	gen, address := m.sonosOutput.gen, m.sonosOutput.selected
	bridge := m.outputBridge()
	m.sonosOutput.busy = true
	m.sonosOutput.busyKind = "read"
	m.sonosOutput.attempted = time.Now()
	return func() tea.Msg {
		defer cancel()
		state, err := bridge.state(ctx, address)
		return sonosStateMsg{gen: gen, state: state, err: err}
	}
}

func (m *Model) controlSonos(action string) tea.Cmd {
	return m.controlSonosJob(sonosControlRequest{action: action})
}

func (m *Model) controlSonosJob(request sonosControlRequest) tea.Cmd {
	grouping := request.action == "group" || request.action == "separate"
	if m.sonosOutput.selected == "" && !grouping {
		return nil
	}
	if request.ctx == nil && request.jobs != nil {
		request.ctx, _ = request.jobs.Context(request.jobID)
	}
	m.cancelSonosQueueRead()
	if m.sonosOutput.busy && m.sonosOutput.busyKind == "read" {
		m.cancelSonosRequest()
	}
	if m.sonosOutput.busy {
		m.sonosOutput.pending = append(m.sonosOutput.pending, request)
		return nil
	}
	return m.startSonosControl(request)
}

func (m *Model) setSonosVolume(delta int) tea.Cmd {
	return m.setSonosVolumeJob(delta, nil, "", ipc.Response{})
}

func (m *Model) setSonosVolumeJob(delta int, jobs *ipc.JobStore, jobID string, result ipc.Response) tea.Cmd {
	if m.sonosOutput.selected == "" || m.sonosOutput.state == nil || m.sonosOutput.state.Volume == nil {
		return nil
	}
	m.cancelSonosQueueRead()
	value := m.intendedSonosVolume()
	value = max(0, min(100, value+delta))
	if jobs != nil {
		result.Volume = sonosVolumeDB(value)
	}
	var ctx context.Context
	if jobs != nil {
		ctx, _ = jobs.Context(jobID)
	}
	request := sonosControlRequest{action: "volume", volume: value, ctx: ctx, jobs: jobs, jobID: jobID, result: result}
	if m.sonosOutput.busy && m.sonosOutput.busyKind == "read" {
		m.cancelSonosRequest()
	}
	if m.sonosOutput.busy {
		m.sonosOutput.pending = append(m.sonosOutput.pending, request)
		return nil
	}
	return m.startSonosControl(request)
}

func (m *Model) startSonosControl(request sonosControlRequest) tea.Cmd {
	parent := request.ctx
	if parent == nil {
		parent = context.Background()
	}
	timeout := 8 * time.Second
	if request.action == "group" || request.action == "separate" {
		timeout = 25 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	m.sonosOutput.cancel = cancel
	gen, address := m.sonosOutput.gen, m.sonosOutput.selected
	bridge := m.outputBridge()
	m.sonosOutput.busy = true
	m.sonosOutput.busyKind = "control"
	m.sonosOutput.active = &request
	pickerGen := m.devicePicker.gen
	return func() tea.Msg {
		defer cancel()
		if err := ctx.Err(); err != nil {
			return sonosControlMsg{gen: gen, request: request, err: err}
		}
		var err error
		if request.action == "volume" {
			err = bridge.volume(ctx, address, request.volume)
		} else if request.action == "group" {
			err = bridge.setGroup(ctx, request.rooms, request.playback)
		} else if request.action == "separate" {
			err = bridge.separateGroup(ctx, request.playback)
		} else {
			err = bridge.control(ctx, request.action, address)
		}
		msg := sonosControlMsg{gen: gen, request: request, pickerGen: pickerGen, err: err}
		if request.action == "group" || request.action == "separate" {
			msg.devices, msg.refreshErr = bridge.devices(ctx)
		}
		return msg
	}
}

func (m *Model) handleSonosDevices(msg sonosDevicesMsg) {
	if msg.gen != m.sonosOutput.discoveryGen {
		return
	}
	m.sonosOutput.discoveryCancel = nil
	m.devicePicker.loading = false
	if msg.err != nil {
		m.status.Errorf(statusTTLDefault, "Output discovery: %s", msg.err)
	}
	m.devicePicker.devices = msg.devices
	m.devicePicker.sonos = make(map[string]spotify.SonosDevice, len(msg.sonos))
	for _, device := range msg.sonos {
		if !device.Satellite && device.ID != "" && device.RoomID != "" {
			m.devicePicker.sonos[device.ID] = device
		}
	}
	if m.devicePicker.checked == nil {
		m.devicePicker.checked = m.currentSonosGroup()
	}
}

func (m Model) currentSonosGroup() map[string]bool {
	checked := map[string]bool{}
	for _, device := range m.devicePicker.sonos {
		if device.ID == m.sonosOutput.selected {
			for _, id := range device.MemberIDs {
				for _, room := range m.devicePicker.sonos {
					if room.RoomID == id {
						checked[id] = true
					}
				}
			}
			break
		}
	}
	return checked
}

func (m *Model) toggleSonosRoom(device player.AudioDevice) {
	room, ok := m.devicePicker.sonos[strings.TrimPrefix(device.Name, "sonos:")]
	if !ok || room.RoomID == "" || room.Satellite || m.devicePicker.loading || m.sonosOutput.busy {
		return
	}
	if m.devicePicker.checked == nil {
		m.devicePicker.checked = map[string]bool{}
	}
	if m.devicePicker.checked[room.RoomID] {
		delete(m.devicePicker.checked, room.RoomID)
	} else {
		m.devicePicker.checked[room.RoomID] = true
	}
}

func (m *Model) applySonosGroup() tea.Cmd {
	if m.devicePicker.loading || len(m.devicePicker.checked) == 0 {
		return nil
	}
	var rooms []string
	for id := range m.devicePicker.checked {
		for _, device := range m.devicePicker.sonos {
			if device.RoomID == id && !device.Satellite {
				rooms = append(rooms, id)
				break
			}
		}
	}
	if len(rooms) == 0 {
		return nil
	}
	sort.Strings(rooms)
	playback := m.sonosPlaybackRoom(rooms)
	if playback == "" {
		playback = rooms[0]
	}
	return m.controlSonosJob(sonosControlRequest{action: "group", rooms: rooms, playback: playback})
}

func (m Model) sonosPlaybackRoom(rooms []string) string {
	checked := make(map[string]bool, len(rooms))
	for _, id := range rooms {
		checked[id] = true
	}
	for _, device := range m.devicePicker.sonos {
		if device.ID == m.sonosOutput.selected {
			for _, candidate := range m.devicePicker.sonos {
				if candidate.ID == device.Coordinator && checked[candidate.RoomID] {
					return candidate.RoomID
				}
			}
			if checked[device.RoomID] {
				return device.RoomID
			}
		}
	}
	var candidates []spotify.SonosDevice
	for _, device := range m.devicePicker.sonos {
		if checked[device.RoomID] && !device.Satellite {
			candidates = append(candidates, device)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if strings.ToLower(candidates[i].Name) == strings.ToLower(candidates[j].Name) {
			return candidates[i].ID < candidates[j].ID
		}
		return strings.ToLower(candidates[i].Name) < strings.ToLower(candidates[j].Name)
	})
	if len(candidates) > 0 {
		return candidates[0].RoomID
	}
	return ""
}

func (m *Model) separateSonosGroup() tea.Cmd {
	if m.sonosOutput.selected == "" || m.devicePicker.loading {
		return nil
	}
	device, ok := m.devicePicker.sonos[m.sonosOutput.selected]
	if !ok || device.RoomID == "" {
		return nil
	}
	return m.controlSonosJob(sonosControlRequest{action: "separate", playback: device.RoomID})
}

func (m *Model) handleSonosState(msg sonosStateMsg) {
	if msg.gen != m.sonosOutput.gen || m.sonosOutput.selected == "" {
		return
	}
	m.sonosOutput.busy = false
	m.sonosOutput.busyKind = ""
	m.sonosOutput.cancel = nil
	if msg.err != nil {
		m.sonosOutput.state = nil
		m.status.Errorf(statusTTLDefault, "Sonos state: %s", msg.err)
		return
	}
	m.sonosOutput.state = &msg.state
	m.sonosOutput.refreshed = time.Now()
}

func (m *Model) handleSonosControl(msg sonosControlMsg) tea.Cmd {
	grouping := msg.request.action == "group" || msg.request.action == "separate"
	if msg.gen != m.sonosOutput.gen || (m.sonosOutput.selected == "" && !grouping) {
		return nil
	}
	m.sonosOutput.busy = false
	m.sonosOutput.busyKind = ""
	m.sonosOutput.cancel = nil
	m.sonosOutput.active = nil
	if msg.err != nil {
		if !grouping {
			m.status.Errorf(statusTTLDefault, "Sonos %s: %s", msg.request.action, msg.err)
		}
	} else if msg.request.action == "volume" && m.sonosOutput.state != nil {
		volume := msg.request.volume
		m.sonosOutput.state.Volume = &volume
	}
	if msg.request.action == "group" || msg.request.action == "separate" {
		if msg.refreshErr == nil {
			for _, device := range msg.devices {
				if msg.err == nil && m.sonosOutput.selected != "" && device.RoomID == msg.request.playback && device.ID != "" {
					m.sonosOutput.selected, m.sonosOutput.name = device.ID, device.Name
					break
				}
			}
			if msg.pickerGen == m.devicePicker.gen && m.devicePicker.visible {
				m.devicePicker.sonos = make(map[string]spotify.SonosDevice, len(msg.devices))
				for _, device := range msg.devices {
					if !device.Satellite {
						m.devicePicker.sonos[device.ID] = device
					}
				}
				m.devicePicker.checked = m.currentSonosGroup()
			}
		}
		if msg.err != nil || msg.refreshErr != nil {
			var details []string
			if msg.err != nil {
				details = append(details, "change may be partial: "+msg.err.Error())
			}
			if msg.refreshErr != nil {
				details = append(details, "topology refresh failed: "+msg.refreshErr.Error())
			}
			m.status.Errorf(statusTTLDefault, "Sonos grouping: %s", strings.Join(details, "; "))
		} else {
			m.status.Show("Sonos grouping updated", statusTTLDefault)
		}
	}
	if msg.request.jobs != nil {
		if msg.err != nil {
			m.failV2Job(msg.request.jobs, msg.request.jobID, v2UnavailableControlError("Sonos control failed: "+msg.err.Error()))
		} else {
			m.completeV2Job(msg.request.jobs, msg.request.jobID, msg.request.result)
		}
	}
	if cmd := m.nextSonosControl(); cmd != nil {
		return cmd
	}
	if grouping && m.sonosOutput.selected != "" {
		m.sonosOutput.state = nil
		return m.refreshSonosQueue()
	}
	return m.refreshSonosState()
}
