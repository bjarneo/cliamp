package main

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/external/local"
	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/internal/resume"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/lyrics"
	"github.com/bjarneo/cliamp/mediactl"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	providerapi "github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/resolve"
	"github.com/bjarneo/cliamp/tracksave"
	"github.com/bjarneo/cliamp/ui"
	"github.com/bjarneo/cliamp/ui/model"
)

const daemonControlQueueCapacity = 64

// runDaemon runs cliamp without a TUI: serves IPC against the shared
// player+playlist, auto-advances tracks, exits on SIGINT/SIGTERM.
func runDaemon(p *player.Player, pl *playlist.Playlist, localProv *local.Provider, providers []model.ProviderEntry, autoPlay bool, eqPreset string) error {
	fmt.Fprintf(os.Stderr, "cliamp: running headless (socket: %s)\n", ipc.DefaultSocketPath())
	applog.Info("daemon: starting headless mode")

	d := &daemon{
		player:       p,
		playlist:     pl,
		localProv:    localProv,
		providers:    providers,
		vis:          ui.NewVisualizer(float64(p.SampleRate())),
		historyStore: history.New(),
		eqPreset:     eqPreset,
		quit:         make(chan struct{}, 1),
		// All external control surfaces feed this bounded queue. The daemon loop
		// is the sole owner of command ordering and playlist mutations.
		control: make(chan any, daemonControlQueueCapacity),
	}
	if d.eqPreset == "" {
		d.eqPreset = "Custom"
	}

	// Wire MPRIS (Linux) / NowPlaying (macOS) so playerctl and OS media
	// keys see the daemon. mediactl callbacks dispatch back through d.Send.
	svc, mcErr := mediactl.New(func(msg tea.Msg) { d.Send(msg) })
	if mcErr != nil {
		fmt.Fprintf(os.Stderr, "media controls: %v\n", mcErr)
		applog.Warn("daemon: media controls unavailable: %v", mcErr)
	}
	if svc != nil {
		defer svc.Close()
		d.notifier = svc
	}

	if autoPlay && pl.Len() > 0 {
		d.mu.Lock()
		d.playCurrent()
		d.mu.Unlock()
	}

	srv, err := ipc.NewServer(ipc.DefaultSocketPath())
	if err != nil {
		return fmt.Errorf("ipc: %w", err)
	}
	defer srv.Close()
	d.broker = srv.Broker()
	srv.SetV2Dispatcher(newDaemonV2Dispatcher(d, srv.JobStore()))
	operations := ipc.DefaultOperationRegistry()
	operations.Unregister("theme", "vis", "plugin.call", "plugin.commands")
	srv.SetOperationRegistry(operations)
	go publishDaemonV2JobEvents(srv.Done(), srv.JobStore(), srv.Broker())
	d.publishRuntimeState()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-sigCh:
			applog.Info("daemon: signal received, shutting down")
			d.saveResume()
			return nil
		case <-d.quit:
			applog.Info("daemon: quit requested via media control, shutting down")
			d.saveResume()
			return nil
		case msg := <-d.control:
			d.handleMessage(msg)
		case <-ticker.C:
			d.tick()
		}
	}
}

// daemon implements ipc.Dispatcher for headless mode. The mutex covers
// playlist state and "what plays next" decisions; the player itself is
// internally thread-safe so blocking I/O (Play, PlayYTDL) runs without it.
type daemon struct {
	mu              sync.Mutex
	player          player.Engine
	playlist        *playlist.Playlist
	localProv       *local.Provider
	providers       []model.ProviderEntry
	vis             *ui.Visualizer
	historyStore    *history.Store
	historyTrack    string
	historyRecorded bool
	loadedPlaylist  string
	eqPreset        string
	notifier        playback.Notifier
	quit            chan struct{}
	control         chan any
	broker          *ipc.Broker

	playbackTrack    playlist.Track
	hasPlaybackTrack bool
	device           string
	runtimeRevision  uint64
	runtimeReady     bool
	runtimeLast      daemonRuntimeFingerprint
}

func (d *daemon) Send(msg any) {
	if d.control != nil {
		d.control <- msg
		return
	}
	d.handleMessage(msg)
}

// handleMessage runs only on the daemon control loop, except in focused tests
// that construct a daemon without a control channel.
func (d *daemon) handleMessage(msg any) {
	switch m := msg.(type) {
	case daemonV2ReadRequest:
		d.handleV2ReadRequest(m)
		return
	case daemonV2JobRequest:
		d.handleV2JobRequest(m)
		return
	}

	defer d.publishRuntimeState()

	switch m := msg.(type) {
	case ipc.LibraryRequestMsg:
		d.handleLibrary(m)
		return
	case ipc.LyricsRequestMsg:
		d.handleLyrics(m)
		return
	case ipc.HistoryRequestMsg:
		d.handleHistory(m)
		return
	case ipc.URLRequestMsg:
		d.handleURL(m)
		return
	case ipc.SaveRequestMsg:
		d.handleSave(m)
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	switch m := msg.(type) {
	case playback.PlayMsg:
		if d.player.IsPaused() {
			d.resume()
		} else if !d.player.IsPlaying() && d.playlist.Len() > 0 {
			d.playCurrent()
		}

	case playback.PauseMsg:
		if d.player.IsPlaying() && !d.player.IsPaused() {
			d.player.TogglePause()
		}

	case playback.PlayPauseMsg:
		d.toggle()

	case playback.StopMsg:
		d.player.Stop()
		d.clearPlaybackTrack()

	case playback.NextMsg:
		d.nextTrack()

	case playback.PrevMsg:
		d.prevTrack()

	case playback.QuitMsg:
		select {
		case d.quit <- struct{}{}:
		default:
		}

	case playback.SetVolumeMsg:
		d.player.SetVolume(m.VolumeDB)

	case playback.SeekMsg:
		_ = d.player.Seek(m.Offset)

	case playback.SetPositionMsg:
		cur := d.player.Position()
		_ = d.player.Seek(m.Position - cur)

	case ipc.LoadMsg:
		d.handleLoad(m)

	case ipc.QueueMsg:
		d.playlist.Add(playlist.TrackFromPath(m.Path))

	case ipc.ThemeMsg:
		reply(m.Reply, ipc.Response{OK: false, Error: "theme not available in headless mode"})

	case ipc.VisMsg:
		reply(m.Reply, ipc.Response{OK: false, Error: "visualizer not available in headless mode"})

	case ipc.ShuffleMsg:
		d.handleShuffle(m)

	case ipc.RepeatMsg:
		d.handleRepeat(m)

	case ipc.MonoMsg:
		d.handleMono(m)

	case ipc.SpeedMsg:
		d.player.SetSpeed(m.Speed)
		reply(m.Reply, ipc.Response{OK: true, Speed: d.player.Speed()})

	case ipc.EQMsg:
		d.handleEQ(m)

	case ipc.DeviceMsg:
		d.handleDevice(m)

	case ipc.QueueRequestMsg:
		d.handleQueue(m)
	}
}

func (d *daemon) handleURL(m ipc.URLRequestMsg) {
	tracks, err := resolve.URL(m.URL)
	if err != nil {
		replyError(m.Reply, err)
		return
	}
	if len(tracks) == 0 {
		reply(m.Reply, ipc.Response{OK: false, Error: "no tracks found at URL"})
		return
	}
	d.mu.Lock()
	// A Play request jumps to the first newly added track, so a caller that
	// asked to play a URL hears it even when something is already playing.
	// Without it the tracks are appended and only start when the player is
	// idle, which is the right default for a plain append.
	start := d.playlist.Len()
	wasStopped := !d.player.IsPlaying()
	d.playlist.Add(tracks...)
	d.loadedPlaylist = ""
	switch {
	case m.Play:
		d.playlist.SetIndex(start)
		d.playCurrent()
	case wasStopped:
		d.playCurrent()
	}
	d.mu.Unlock()
	reply(m.Reply, ipc.Response{OK: true, Tracks: trackInfos(tracks), Total: len(tracks)})
}

func (d *daemon) handleSave(m ipc.SaveRequestMsg) {
	d.mu.Lock()
	track, index := d.playlist.Current()
	d.mu.Unlock()
	if index < 0 {
		reply(m.Reply, ipc.Response{OK: false, Error: "nothing to save"})
		return
	}
	path, err := tracksave.Save(track)
	if err != nil {
		replyError(m.Reply, err)
		return
	}
	reply(m.Reply, ipc.Response{OK: true, Output: path})
}

// tick advances to the next track when the current one has drained, and
// republishes playback state to the media-control notifier. Daemon mode
// skips gapless preloading; small inter-track gaps are fine.
func (d *daemon) tick() {
	d.mu.Lock()
	if d.player.IsPlaying() && !d.player.IsPaused() && d.player.Drained() {
		track, idx := d.playlist.Current()
		if idx >= 0 && d.playbackIsLive(track) {
			d.player.Stop()
			d.playTrack(track)
		} else {
			d.nextTrack()
		}
	}
	d.recordHistory()
	state := d.snapshotState()
	d.publishRuntimeStateLocked()
	d.mu.Unlock()
	if d.notifier != nil {
		d.notifier.Update(state)
	}
}

// snapshotState builds a playback.State for OS media-control notifiers.
// Caller must hold d.mu.
func (d *daemon) snapshotState() playback.State {
	status := playback.StatusStopped
	if d.player.IsPlaying() {
		if d.player.IsPaused() {
			status = playback.StatusPaused
		} else {
			status = playback.StatusPlaying
		}
	}
	track, _ := d.playlist.Current()
	return playback.State{
		Status: status,
		Track: playback.Track{
			Title:       track.Title,
			Artist:      track.Artist,
			Album:       track.Album,
			Genre:       track.Genre,
			TrackNumber: track.TrackNumber,
			URL:         track.Path,
			Duration:    d.player.Duration(),
		},
		VolumeDB: d.player.Volume(),
		Position: d.player.Position(),
		Seekable: d.player.Seekable(),
	}
}

func (d *daemon) playCurrent() {
	track, idx := d.playlist.Current()
	if idx < 0 {
		return
	}
	d.playTrack(track)
}

// playTrack runs while d.mu is held so playback decisions commit in request
// order. Releasing it during setup lets a slow older request overwrite a newer
// next/load request after that request has already updated the playlist.
func (d *daemon) playTrack(track playlist.Track) {
	dur := time.Duration(track.DurationSecs) * time.Second
	var err error
	if playlist.IsYTDL(track.Path) {
		err = d.player.PlayYTDL(track.Path, dur)
	} else {
		err = d.player.Play(track.Path, dur)
	}
	if err != nil {
		applog.Warn("daemon: play %q: %v", track.Path, err)
		d.player.Stop()
		d.clearPlaybackTrack()
		return
	}
	d.playbackTrack = track
	d.hasPlaybackTrack = true
}

func (d *daemon) clearPlaybackTrack() {
	d.playbackTrack = playlist.Track{}
	d.hasPlaybackTrack = false
}

func (d *daemon) playbackIsLive(track playlist.Track) bool {
	if track.IsLive() {
		return true
	}
	reporter, ok := d.player.(interface{ IsLiveStream() bool })
	return ok && reporter.IsLiveStream()
}

func (d *daemon) resume() {
	track, idx := d.playlist.Current()
	if idx >= 0 && d.playbackIsLive(track) {
		d.player.Stop()
		d.playTrack(track)
		return
	}
	d.player.TogglePause()
}

func (d *daemon) toggle() {
	if !d.player.IsPlaying() {
		if d.playlist.Len() > 0 {
			d.playCurrent()
		}
		return
	}
	if d.player.IsPaused() {
		d.resume()
		return
	}
	d.player.TogglePause()
}

func (d *daemon) nextTrack() {
	track, ok := d.playlist.Next()
	if !ok {
		d.player.Stop()
		d.clearPlaybackTrack()
		return
	}
	d.playTrack(track)
}

func (d *daemon) prevTrack() {
	if d.player.Position() > 3*time.Second {
		track, idx := d.playlist.Current()
		if idx < 0 {
			return
		}
		if d.player.Seekable() {
			_ = d.player.Seek(-d.player.Position())
			return
		}
		d.playTrack(track)
		return
	}
	track, ok := d.playlist.Prev()
	if !ok {
		return
	}
	d.playTrack(track)
}

func (d *daemon) handleLoad(m ipc.LoadMsg) {
	if d.localProv == nil {
		reply(m.Reply, ipc.Response{OK: false, Error: "local provider unavailable"})
		return
	}
	tracks, err := d.localProv.Tracks(m.Playlist)
	if err != nil {
		reply(m.Reply, ipc.Response{OK: false, Error: fmt.Sprintf("playlist %q: %v", m.Playlist, err)})
		return
	}
	d.playlist.Replace(tracks)
	d.loadedPlaylist = m.Playlist
	d.playCurrent()
	reply(m.Reply, ipc.Response{OK: true, Playlist: m.Playlist, Total: len(tracks)})
}

func (d *daemon) handleShuffle(m ipc.ShuffleMsg) {
	switch strings.ToLower(m.Name) {
	case "on":
		if !d.playlist.Shuffled() {
			d.playlist.ToggleShuffle()
		}
	case "off":
		if d.playlist.Shuffled() {
			d.playlist.ToggleShuffle()
		}
	default:
		d.playlist.ToggleShuffle()
	}
	shuffled := d.playlist.Shuffled()
	reply(m.Reply, ipc.Response{OK: true, Shuffle: &shuffled})
}

func (d *daemon) handleRepeat(m ipc.RepeatMsg) {
	switch strings.ToLower(m.Name) {
	case "off":
		d.playlist.SetRepeat(playlist.RepeatOff)
	case "all":
		d.playlist.SetRepeat(playlist.RepeatAll)
	case "one":
		d.playlist.SetRepeat(playlist.RepeatOne)
	default:
		d.playlist.CycleRepeat()
	}
	reply(m.Reply, ipc.Response{OK: true, Repeat: d.playlist.Repeat().String()})
}

func (d *daemon) handleMono(m ipc.MonoMsg) {
	switch strings.ToLower(m.Name) {
	case "on":
		if !d.player.Mono() {
			d.player.ToggleMono()
		}
	case "off":
		if d.player.Mono() {
			d.player.ToggleMono()
		}
	default:
		d.player.ToggleMono()
	}
	mono := d.player.Mono()
	reply(m.Reply, ipc.Response{OK: true, Mono: &mono})
}

func (d *daemon) handleEQ(m ipc.EQMsg) {
	if m.Band > 0 || (m.Band == 0 && m.Name == "") {
		d.player.SetEQBand(m.Band, m.Value)
		d.eqPreset = "Custom"
		reply(m.Reply, ipc.Response{OK: true, EQPreset: "Custom"})
		return
	}
	if m.Name == "" {
		reply(m.Reply, ipc.Response{OK: false, Error: "eq requires a preset name or --band"})
		return
	}
	preset, ok := model.EQPresetByName(m.Name)
	if !ok {
		reply(m.Reply, ipc.Response{OK: false, Error: fmt.Sprintf("unknown EQ preset %q", m.Name)})
		return
	}
	for i, gain := range preset.Bands {
		d.player.SetEQBand(i, gain)
	}
	d.eqPreset = preset.Name
	reply(m.Reply, ipc.Response{OK: true, EQPreset: preset.Name})
}

func (d *daemon) handleDevice(m ipc.DeviceMsg) {
	if strings.EqualFold(m.Name, "list") {
		devices, err := player.ListAudioDevices()
		if err != nil {
			reply(m.Reply, ipc.Response{OK: false, Error: fmt.Sprintf("list devices: %v", err)})
			return
		}
		var lines []string
		items := make([]ipc.DeviceInfo, 0, len(devices))
		for _, dev := range devices {
			marker := "  "
			if dev.Active {
				marker = "* "
				d.device = dev.Name
			}
			lines = append(lines, fmt.Sprintf("%s%s", marker, dev.Name))
			items = append(items, ipc.DeviceInfo{Name: dev.Name, Active: dev.Active})
		}
		reply(m.Reply, ipc.Response{OK: true, Device: strings.Join(lines, "\n"), Devices: items})
		return
	}
	if err := player.SwitchAudioDevice(m.Name); err != nil {
		reply(m.Reply, ipc.Response{OK: false, Error: fmt.Sprintf("switch device: %v", err)})
		return
	}
	d.device = m.Name
	reply(m.Reply, ipc.Response{OK: true, Device: m.Name})
}

func (d *daemon) handleQueue(m ipc.QueueRequestMsg) {
	switch m.Op {
	case "queue.list":
		reply(m.Reply, d.queueResponse())
	case "queue.play":
		if m.Index < 0 || m.Index >= d.playlist.Len() {
			reply(m.Reply, ipc.Response{OK: false, Error: "queue index out of range"})
			return
		}
		d.playlist.SetIndex(m.Index)
		d.playCurrent()
		reply(m.Reply, d.queueResponse())
	case "queue.enqueue":
		if m.Index < 0 || m.Index >= d.playlist.Len() {
			reply(m.Reply, ipc.Response{OK: false, Error: "queue index out of range"})
			return
		}
		d.playlist.Queue(m.Index)
		reply(m.Reply, d.queueResponse())
	case "queue.remove":
		if m.Index == d.playlist.Index() {
			d.player.Stop()
			d.clearPlaybackTrack()
		}
		if !d.playlist.Remove(m.Index) {
			reply(m.Reply, ipc.Response{OK: false, Error: "queue index out of range"})
			return
		}
		reply(m.Reply, d.queueResponse())
	case "queue.move":
		if !d.playlist.Move(m.Index, m.To) {
			reply(m.Reply, ipc.Response{OK: false, Error: "invalid queue move"})
			return
		}
		reply(m.Reply, d.queueResponse())
	case "queue.clear":
		d.player.Stop()
		d.clearPlaybackTrack()
		d.playlist.Replace(nil)
		d.loadedPlaylist = ""
		reply(m.Reply, d.queueResponse())
	case "track.play", "track.queue":
		if m.Track == nil || m.Track.Path == "" {
			reply(m.Reply, ipc.Response{OK: false, Error: "track is required"})
			return
		}
		track := trackFromInfo(*m.Track)
		d.playlist.Add(track)
		idx := d.playlist.Len() - 1
		if m.Op == "track.queue" && d.player.IsPlaying() {
			d.playlist.Queue(idx)
		} else {
			d.playlist.SetIndex(idx)
			d.playCurrent()
		}
		reply(m.Reply, d.queueResponse())
	default:
		reply(m.Reply, ipc.Response{OK: false, Error: "unknown queue operation"})
	}
}

func (d *daemon) queueResponse() ipc.Response {
	tracks := d.playlist.Tracks()
	items := make([]ipc.TrackInfo, len(tracks))
	for i, track := range tracks {
		items[i] = trackInfo(track, i, d.playlist.QueuePosition(i))
	}
	return ipc.Response{OK: true, Tracks: items, Index: d.playlist.Index(), Total: len(items)}
}

func (d *daemon) handleLibrary(m ipc.LibraryRequestMsg) {
	switch m.Op {
	case "provider.list":
		items := make([]ipc.ProviderInfo, 0, len(d.providers))
		for _, entry := range d.providers {
			_, searchable := entry.Provider.(providerapi.Searcher)
			if _, ok := entry.Provider.(providerapi.CatalogSearcher); ok {
				searchable = true
			}
			_, browseArtists := entry.Provider.(providerapi.ArtistBrowser)
			_, browseAlbums := entry.Provider.(providerapi.AlbumBrowser)
			_, catalog := entry.Provider.(providerapi.CatalogLoader)
			items = append(items, ipc.ProviderInfo{Key: entry.Key, Name: entry.Name, Searchable: searchable, BrowseArtists: browseArtists, BrowseAlbums: browseAlbums, Catalog: catalog})
		}
		reply(m.Reply, ipc.Response{OK: true, Providers: items})
		return
	}

	entry, ok := d.provider(m.Provider)
	if !ok {
		reply(m.Reply, ipc.Response{OK: false, Error: fmt.Sprintf("unknown provider %q", m.Provider)})
		return
	}

	switch m.Op {
	case "playlist.create":
		creator, ok := entry.Provider.(providerapi.PlaylistCreator)
		if !ok {
			reply(m.Reply, ipc.Response{OK: false, Error: "provider does not support playlist creation"})
			return
		}
		_, err := creator.CreatePlaylist(context.Background(), m.Playlist)
		replyError(m.Reply, err)
	case "playlist.rename":
		renamer, ok := entry.Provider.(providerapi.PlaylistRenamer)
		if !ok {
			reply(m.Reply, ipc.Response{OK: false, Error: "provider does not support playlist renaming"})
			return
		}
		replyError(m.Reply, renamer.RenamePlaylist(m.Playlist, m.NewName))
	case "playlist.delete":
		deleter, ok := entry.Provider.(providerapi.PlaylistDeleter)
		if !ok {
			reply(m.Reply, ipc.Response{OK: false, Error: "provider does not support playlist deletion"})
			return
		}
		replyError(m.Reply, deleter.DeletePlaylist(m.Playlist))
	case "playlist.remove":
		deleter, ok := entry.Provider.(providerapi.PlaylistDeleter)
		if !ok {
			reply(m.Reply, ipc.Response{OK: false, Error: "provider does not support removing tracks"})
			return
		}
		replyError(m.Reply, deleter.RemoveTrack(m.Playlist, m.Index))
	case "playlist.add":
		if m.Track == nil {
			reply(m.Reply, ipc.Response{OK: false, Error: "track is required"})
			return
		}
		writer, ok := entry.Provider.(providerapi.PlaylistWriter)
		if !ok {
			reply(m.Reply, ipc.Response{OK: false, Error: "provider does not support adding tracks"})
			return
		}
		replyError(m.Reply, writer.AddTrackToPlaylist(context.Background(), m.Playlist, trackFromInfo(*m.Track)))
	case "playlist.add_many":
		if len(m.Tracks) == 0 {
			reply(m.Reply, ipc.Response{OK: false, Error: "tracks are required"})
			return
		}
		tracks := make([]playlist.Track, len(m.Tracks))
		for i, info := range m.Tracks {
			tracks[i] = trackFromInfo(info)
		}
		if writer, ok := entry.Provider.(providerapi.PlaylistBatchWriter); ok {
			added, skipped, err := writer.AddTracksToPlaylist(context.Background(), m.Playlist, tracks)
			if err != nil {
				replyError(m.Reply, err)
			} else {
				reply(m.Reply, ipc.Response{OK: true, Total: added, Items: []string{fmt.Sprintf("skipped:%d", skipped)}})
			}
			return
		}
		writer, ok := entry.Provider.(providerapi.PlaylistWriter)
		if !ok {
			reply(m.Reply, ipc.Response{OK: false, Error: "provider does not support adding tracks"})
			return
		}
		for _, track := range tracks {
			if err := writer.AddTrackToPlaylist(context.Background(), m.Playlist, track); err != nil {
				replyError(m.Reply, err)
				return
			}
		}
		reply(m.Reply, ipc.Response{OK: true, Total: len(tracks)})
	case "playlist.replace":
		saver, ok := entry.Provider.(providerapi.PlaylistSaver)
		if !ok {
			reply(m.Reply, ipc.Response{OK: false, Error: "provider does not support replacing playlists"})
			return
		}
		tracks := make([]playlist.Track, len(m.Tracks))
		for i, info := range m.Tracks {
			tracks[i] = trackFromInfo(info)
		}
		replyError(m.Reply, saver.SavePlaylist(m.Playlist, tracks))
	case "playlist.bookmark":
		if m.Track == nil {
			reply(m.Reply, ipc.Response{OK: false, Error: "track is required"})
			return
		}
		bookmarks, ok := entry.Provider.(providerapi.BookmarkSetter)
		if !ok {
			reply(m.Reply, ipc.Response{OK: false, Error: "provider does not support bookmarks"})
			return
		}
		replyError(m.Reply, bookmarks.SetBookmarkByPath(m.Playlist, m.Track.Path))
	case "provider.playlists":
		items, err := providerPlaylistInfos(entry)
		if err != nil {
			reply(m.Reply, ipc.Response{OK: false, Error: err.Error()})
			return
		}
		page, total := daemonPage(items, m.Offset, m.Limit, 200)
		reply(m.Reply, ipc.Response{OK: true, Playlists: page, Total: total})
	case "provider.catalog":
		loader, ok := entry.Provider.(providerapi.CatalogLoader)
		if !ok {
			reply(m.Reply, ipc.Response{OK: false, Error: "provider does not support catalog paging"})
			return
		}
		limit := m.Limit
		if limit <= 0 || limit > 200 {
			limit = 50
		}
		added, err := loader.LoadCatalogPage(m.Offset, limit)
		if err != nil {
			replyError(m.Reply, err)
			return
		}
		items, err := providerPlaylistInfos(entry)
		if err != nil {
			replyError(m.Reply, err)
			return
		}
		reply(m.Reply, ipc.Response{OK: true, Playlists: items, Total: added})
	case "provider.tracks", "provider.load":
		tracks, err := entry.Provider.Tracks(m.Playlist)
		if err != nil {
			reply(m.Reply, ipc.Response{OK: false, Error: err.Error()})
			return
		}
		if m.Op == "provider.load" {
			d.mu.Lock()
			d.playlist.Replace(tracks)
			d.loadedPlaylist = entry.Key + ":" + m.Playlist
			d.playCurrent()
			d.mu.Unlock()
		}
		if m.Op == "provider.tracks" {
			page, total := daemonPage(tracks, m.Offset, m.Limit, 200)
			reply(m.Reply, ipc.Response{OK: true, Tracks: trackInfos(page), Playlist: m.Playlist, Total: total})
			return
		}
		reply(m.Reply, ipc.Response{OK: true, Tracks: trackInfos(tracks), Playlist: m.Playlist, Total: len(tracks)})
	case "provider.search":
		limit := m.Limit
		if limit <= 0 || limit > 100 {
			limit = 25
		}
		fetchLimit := min(100, m.Offset+limit)
		tracks, err := searchProvider(entry.Provider, m.Query, fetchLimit)
		if err != nil {
			reply(m.Reply, ipc.Response{OK: false, Error: err.Error()})
			return
		}
		page, total := daemonPage(tracks, m.Offset, limit, 100)
		reply(m.Reply, ipc.Response{OK: true, Tracks: trackInfos(page), Total: total})
	case "provider.artists":
		browser, ok := entry.Provider.(providerapi.ArtistBrowser)
		if !ok {
			reply(m.Reply, ipc.Response{OK: false, Error: "provider does not support artist browsing"})
			return
		}
		artists, err := browser.Artists()
		if err != nil {
			replyError(m.Reply, err)
			return
		}
		items := make([]ipc.ArtistInfo, len(artists))
		for i, artist := range artists {
			items[i] = ipc.ArtistInfo{ID: artist.ID, Name: artist.Name, AlbumCount: artist.AlbumCount}
		}
		page, total := daemonPage(items, m.Offset, m.Limit, 200)
		reply(m.Reply, ipc.Response{OK: true, Artists: page, Total: total})
	case "provider.artist_albums":
		browser, ok := entry.Provider.(providerapi.ArtistBrowser)
		if !ok {
			reply(m.Reply, ipc.Response{OK: false, Error: "provider does not support artist browsing"})
			return
		}
		albums, err := browser.ArtistAlbums(m.Artist)
		if err != nil {
			replyError(m.Reply, err)
			return
		}
		items := ipcAlbumInfos(albums)
		page, total := daemonPage(items, m.Offset, m.Limit, 200)
		reply(m.Reply, ipc.Response{OK: true, Albums: page, Total: total})
	case "provider.albums":
		browser, ok := entry.Provider.(providerapi.AlbumBrowser)
		if !ok {
			reply(m.Reply, ipc.Response{OK: false, Error: "provider does not support album browsing"})
			return
		}
		sortType := m.Sort
		if sortType == "" {
			sortType = browser.DefaultAlbumSort()
		}
		limit := m.Limit
		if limit <= 0 || limit > 200 {
			limit = 100
		}
		albums, err := browser.AlbumList(sortType, m.Offset, limit)
		if err != nil {
			replyError(m.Reply, err)
			return
		}
		sorts := browser.AlbumSortTypes()
		sortItems := make([]ipc.SortInfo, len(sorts))
		for i, item := range sorts {
			sortItems[i] = ipc.SortInfo{ID: item.ID, Label: item.Label}
		}
		reply(m.Reply, ipc.Response{OK: true, Albums: ipcAlbumInfos(albums), Sorts: sortItems, Total: len(albums)})
	case "provider.album_tracks", "provider.load_album":
		loader, ok := entry.Provider.(providerapi.AlbumTrackLoader)
		if !ok {
			reply(m.Reply, ipc.Response{OK: false, Error: "provider does not support album tracks"})
			return
		}
		tracks, err := loader.AlbumTracks(m.Album)
		if err != nil {
			replyError(m.Reply, err)
			return
		}
		if m.Op == "provider.load_album" {
			d.mu.Lock()
			d.playlist.Replace(tracks)
			d.loadedPlaylist = entry.Key + ":album:" + m.Album
			d.playCurrent()
			d.mu.Unlock()
		}
		if m.Op == "provider.album_tracks" {
			page, total := daemonPage(tracks, m.Offset, m.Limit, 200)
			reply(m.Reply, ipc.Response{OK: true, Tracks: trackInfos(page), Total: total})
			return
		}
		reply(m.Reply, ipc.Response{OK: true, Tracks: trackInfos(tracks), Total: len(tracks)})
	case "provider.favorite":
		favorites, ok := entry.Provider.(providerapi.FavoriteToggler)
		if !ok {
			reply(m.Reply, ipc.Response{OK: false, Error: "provider does not support favorites"})
			return
		}
		_, _, err := favorites.ToggleFavorite(m.Playlist)
		replyError(m.Reply, err)
	default:
		reply(m.Reply, ipc.Response{OK: false, Error: "unknown provider operation"})
	}
}

func daemonPage[T any](items []T, offset, limit, max int) ([]T, int) {
	total := len(items)
	if offset < 0 || offset >= total {
		return nil, total
	}
	if limit <= 0 || limit > max {
		limit = max
	}
	end := min(total, offset+limit)
	return items[offset:end], total
}

func providerPlaylistInfos(entry model.ProviderEntry) ([]ipc.PlaylistInfo, error) {
	lists, err := entry.Provider.Playlists()
	if err != nil {
		return nil, err
	}
	items := make([]ipc.PlaylistInfo, len(lists))
	for i, list := range lists {
		items[i] = ipc.PlaylistInfo{ID: list.ID, Name: list.Name, Provider: entry.Key, Section: list.Section, TrackCount: list.TrackCount, DurationSecs: list.DurationSecs}
		if sectioned, ok := entry.Provider.(providerapi.SectionedList); ok {
			items[i].Favoritable = sectioned.IsFavoritableID(list.ID)
			items[i].Favorite = strings.HasPrefix(list.ID, "f:")
		}
	}
	return items, nil
}

func ipcAlbumInfos(albums []providerapi.AlbumInfo) []ipc.AlbumInfo {
	items := make([]ipc.AlbumInfo, len(albums))
	for i, album := range albums {
		items[i] = ipc.AlbumInfo{ID: album.ID, Name: album.Name, Artist: album.Artist, ArtistID: album.ArtistID, Year: album.Year, TrackCount: album.TrackCount, Genre: album.Genre}
	}
	return items
}

func searchProvider(source playlist.Provider, query string, limit int) ([]playlist.Track, error) {
	if searcher, ok := source.(providerapi.Searcher); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return searcher.SearchTracks(ctx, query, limit)
	}
	catalog, ok := source.(providerapi.CatalogSearcher)
	if !ok {
		return nil, fmt.Errorf("provider does not support search")
	}
	if _, err := catalog.SearchCatalog(query); err != nil {
		return nil, err
	}
	defer catalog.ClearSearch()
	lists, err := source.Playlists()
	if err != nil {
		return nil, err
	}
	if len(lists) > limit {
		lists = lists[:limit]
	}
	tracks := make([]playlist.Track, 0, len(lists))
	for _, list := range lists {
		items, err := source.Tracks(list.ID)
		if err != nil || len(items) == 0 {
			continue
		}
		tracks = append(tracks, items[0])
	}
	return tracks, nil
}

func (d *daemon) provider(key string) (model.ProviderEntry, bool) {
	for _, entry := range d.providers {
		if strings.EqualFold(entry.Key, key) {
			return entry, true
		}
	}
	return model.ProviderEntry{}, false
}

func (d *daemon) handleLyrics(m ipc.LyricsRequestMsg) {
	track, idx := d.playlist.Current()
	if idx < 0 {
		reply(m.Reply, ipc.Response{OK: false, Error: "no current track"})
		return
	}
	lines := lyrics.ParseEmbedded(track.EmbeddedLyrics)
	var err error
	if len(lines) == 0 {
		lines, err = lyrics.Fetch(track.Artist, track.Title)
	}
	if err != nil {
		reply(m.Reply, ipc.Response{OK: false, Error: err.Error()})
		return
	}
	items := make([]ipc.LyricLine, len(lines))
	for i, line := range lines {
		items[i] = ipc.LyricLine{Start: line.Start.Seconds(), Text: line.Text}
	}
	reply(m.Reply, ipc.Response{OK: true, Lyrics: items})
}

func (d *daemon) handleHistory(m ipc.HistoryRequestMsg) {
	if d.historyStore == nil {
		reply(m.Reply, ipc.Response{OK: true})
		return
	}
	if m.Op == "history.clear" {
		replyError(m.Reply, d.historyStore.Clear())
		return
	}
	entries, err := d.historyStore.Recent(m.Limit)
	if err != nil {
		reply(m.Reply, ipc.Response{OK: false, Error: err.Error()})
		return
	}
	items := make([]ipc.HistoryInfo, len(entries))
	for i, entry := range entries {
		items[i] = ipc.HistoryInfo{Track: trackInfo(entry.Track, i, 0), PlayedAt: entry.PlayedAt.Format(time.RFC3339)}
	}
	reply(m.Reply, ipc.Response{OK: true, History: items})
}

func (d *daemon) recordHistory() {
	track, idx := d.playlist.Current()
	if idx < 0 || track.Path == "" {
		return
	}
	if track.Path != d.historyTrack {
		d.historyTrack = track.Path
		d.historyRecorded = false
	}
	if d.historyRecorded || d.historyStore == nil {
		return
	}
	pos, dur := d.player.PositionAndDuration()
	if dur <= 0 && track.DurationSecs > 0 {
		dur = time.Duration(track.DurationSecs) * time.Second
	}
	if dur > 0 && pos >= dur/2 {
		_ = d.historyStore.Record(track, time.Now())
		d.historyRecorded = true
	}
}

func trackInfos(tracks []playlist.Track) []ipc.TrackInfo {
	items := make([]ipc.TrackInfo, len(tracks))
	for i, track := range tracks {
		items[i] = trackInfo(track, i, 0)
	}
	return items
}

func trackInfo(track playlist.Track, index, queuePosition int) ipc.TrackInfo {
	return ipc.TrackInfo{
		Title: track.Title, Artist: track.Artist, Album: track.Album, Genre: track.Genre,
		Path: track.Path, AlbumArtURL: track.AlbumArtURL, Year: track.Year,
		TrackNumber: track.TrackNumber, DurationSecs: track.DurationSecs, Index: index,
		QueuePosition: queuePosition, Stream: track.Stream, Realtime: track.Realtime,
		Feed: track.Feed, Bookmark: track.Bookmark, Unplayable: track.Unplayable,
		DirSourced: track.DirSourced, ProviderMeta: maps.Clone(track.ProviderMeta),
	}
}

func trackFromInfo(info ipc.TrackInfo) playlist.Track {
	return playlist.Track{
		Title: info.Title, Artist: info.Artist, Album: info.Album, Genre: info.Genre,
		Path: info.Path, AlbumArtURL: info.AlbumArtURL, Year: info.Year,
		TrackNumber: info.TrackNumber, DurationSecs: info.DurationSecs,
		Stream: info.Stream || playlist.IsURL(info.Path), Realtime: info.Realtime,
		Feed: info.Feed, Bookmark: info.Bookmark, Unplayable: info.Unplayable,
		DirSourced: info.DirSourced, ProviderMeta: maps.Clone(info.ProviderMeta),
	}
}

// bandsResponse performs the same FFT analysis used by the interactive TUI so
// V2 clients can render a real spectrum while cliamp runs headless.
func (d *daemon) bandsResponse() ipc.Response {
	if d.vis == nil {
		return ipc.Response{OK: false, Error: "visualizer not available in headless mode"}
	}
	d.vis.Tick(ui.VisTickContext{
		Now:     time.Now(),
		Playing: d.player.IsPlaying(),
		Paused:  d.player.IsPaused(),
		Analyze: func(spec ui.VisAnalysisSpec) []float64 {
			samples := d.vis.SampleBuf()
			n := d.player.SamplesInto(samples)
			return d.vis.Analyze(samples[:n], spec)
		},
	})
	bands := append([]float64(nil), d.vis.SmoothedBands()...)
	return ipc.Response{OK: true, Visualizer: d.vis.ModeName(), Bands: bands}
}

// applyStreamTitle folds a stream's live ICY metadata into info, mirroring the
// TUI's resolveTrackDisplay: split "Artist - Title", fall back to the raw value,
// and keep the entry's own title when the tag carries no song.
func applyStreamTitle(info *ipc.TrackInfo, cur playlist.Track, streamTitle string) {
	if !cur.Stream || streamTitle == "" {
		return
	}
	info.StreamTitle = streamTitle
	if a, t, ok := strings.Cut(streamTitle, " - "); ok {
		if t != "" {
			info.Artist, info.Title = a, t
		}
	} else {
		info.Title = streamTitle
	}
	if info.Title != cur.Title {
		info.Station = cur.Title
	}
}

func (d *daemon) saveResume() {
	d.mu.Lock()
	defer d.mu.Unlock()
	track, idx := d.playlist.Current()
	if idx < 0 || track.Path == "" {
		return
	}
	pos := int(d.player.Position().Seconds())
	if pos <= 0 {
		return
	}
	resume.Save(track.Path, pos, "")
}

func reply(ch chan ipc.Response, resp ipc.Response) {
	if ch != nil {
		ch <- resp
	}
}

func replyError(ch chan ipc.Response, err error) {
	if err != nil {
		reply(ch, ipc.Response{OK: false, Error: err.Error()})
		return
	}
	reply(ch, ipc.Response{OK: true})
}
