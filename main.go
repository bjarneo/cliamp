package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/external/radio"
	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/appmeta"
	"github.com/bjarneo/cliamp/internal/embyapi"
	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/internal/resume"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/mediactl"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/resolve"
	"github.com/bjarneo/cliamp/session"
	"github.com/bjarneo/cliamp/theme"
	"github.com/bjarneo/cliamp/ui/model"
)

// version is set at build time via -ldflags "-X main.version=vX.Y.Z".
var version string

// buildVersion returns version when -ldflags set it. go install and go
// build set none, so it falls back to the module version that go records,
// and then to "dev". The result is never empty, so --version always works.
func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

const (
	defaultUIFPS  = 20
	lowPowerUIFPS = 5
	// Size of the virtual terminal a detached session renders into until a
	// client attaches and reports its own.
	detachedCols = 100
	detachedRows = 30
)

// run starts the player: config and providers, the audio engine, the
// Bubble Tea model, IPC, and media controls. With daemon set it renders into
// a virtual terminal instead of this process's own, so `cliamp attach` can
// lend it one later.
func run(overrides config.Overrides, positional []string, daemon, visualizer60FPS bool) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	overrides.Apply(&cfg)

	closeLog, appliedLevel, logErr := initLogging(cfg.LogLevel)
	defer closeLog()
	if logErr != nil {
		fmt.Fprintf(os.Stderr, "logging: %v (continuing without file log)\n", logErr)
		applog.Status("logging: %v", logErr)
	} else {
		applog.Info("cliamp starting (version=%s level=%s)", appmeta.Version(), appliedLevel)
	}
	if daemon {
		if err := checkNotRunning(); err != nil {
			return err
		}
	}

	providers := buildProviders(cfg, !daemon && isCharDevice(os.Stdin))
	defer providers.Close()

	positional, err = searchArgs(positional)
	if err != nil {
		return err
	}
	if cfg.YouTubeMusic.ExpandPlaylist != nil {
		resolve.ExpandYTPlaylist = *cfg.YouTubeMusic.ExpandPlaylist
	}
	resolved, err := resolve.Args(positional)
	if err != nil {
		return err
	}

	defaultProvider := cfg.Provider
	if defaultProvider == "" {
		defaultProvider = "cliamp"
	}
	defaultRadio := len(positional) == 0 && defaultProvider == "radio"
	// The cliamp radio view waits for the listener to pick a channel. A
	// detached session has nobody looking at the view, and auto-play expects
	// sound without a keypress, so both start with the live channel streams
	// instead.
	liveChannels := defaultRadio ||
		(len(positional) == 0 && defaultProvider == "cliamp" && (daemon || cfg.AutoPlay))
	resumeState := resume.Load()

	pl := playlist.New()
	if cfg.Playlist != "" && providers.local != nil {
		tracks, err := providers.local.Tracks(cfg.Playlist)
		if err != nil {
			return fmt.Errorf("playlist %q: %w", cfg.Playlist, err)
		}
		pl.Add(tracks...)
	} else if liveChannels {
		// The channel list lives in the M3U the radio provider already serves,
		// so resolve that instead of restating it here: the startup playlist
		// then matches what browsing "cliamp radio" shows -- same channels,
		// same order, same titles -- and a new channel needs no code change.
		// It goes through the normal pending path, so the fetch happens in the
		// background rather than delaying launch.
		resolved.Pending = append(resolved.Pending, radio.BuiltinURL)
	}
	pl.Add(resolved.Tracks...)

	resumeServer := providers.resumeServer(defaultProvider)
	restoredContext := false
	restoredIndex := 0
	restoredResumePath := ""
	if resumeServer != nil && cfg.Playlist == "" && len(positional) == 0 && len(resolved.Pending) == 0 && pl.Len() == 0 {
		if tracks, index, activePath, ok := restoreServerContext(resumeState, resumeServer); ok {
			pl.Add(tracks...)
			restoredContext = true
			restoredIndex = index
			restoredResumePath = activePath
		}
	}

	p, closePlayer, err := newPlayer(cfg)
	if err != nil {
		return err
	}
	defer closePlayer()
	providers.registerPlayerHooks(p)
	cfg.ApplyPlayer(p)
	cfg.ApplyPlaylist(pl)

	pluginBroker := ipc.NewBroker()
	defer pluginBroker.Close()

	luaMgr, luaErr := luaplugin.New(cfg.Plugins, pluginBroker, model.ReservedKeys())
	if luaErr != nil {
		fmt.Fprintf(os.Stderr, "lua plugins: %v\n", luaErr)
	}
	if luaMgr != nil {
		defer luaMgr.Close()
	}

	m := model.New(p, pl, providers.entries, defaultProvider, providers.localPlaylists(), providers.favorites, providers.history, theme.LoadAll(), luaMgr, config.SaveFunc{})
	m.SetRadioFavorites(providers.radioFavorites)
	if resumeServer != nil {
		m.SetResumeSaver(serverResumeSaver(resumeServer))
	}
	if restoredContext {
		m.SetInitialTrack(restoredIndex)
	}
	m.SetIPCBroker(pluginBroker)
	if luaMgr != nil {
		luaMgr.SetStateProvider(luaStateProvider(p, m.PluginStateLoader()))
		if names := luaMgr.Visualizers(); len(names) > 0 {
			m.RegisterLuaVisualizers(names, luaMgr)
		}
	}
	m.SetPendingURLs(resolved.Pending)
	if cfg.Playlist != "" && len(resolved.Tracks) == 0 && len(resolved.Pending) == 0 {
		m.SetLoadedPlaylist(cfg.Playlist)
	}
	if len(resolved.Tracks) == 0 && len(resolved.Pending) == 0 && pl.Len() == 0 {
		m.StartInProvider()
	}
	if cfg.AutoPlay && !restoredContext {
		m.SetAutoPlay(true)
	}
	configureModel(&m, cfg, visualizer60FPS)

	if resumeState.Path != "" && resumeState.PositionSec > 0 {
		// Jellyfin and Emby resume the restored context above. Mixcloud is also commonly
		// opened from its provider browser rather than a positional URL; preserve
		// cliamp's existing positional-file behavior for other providers.
		switch {
		case restoredResumePath != "":
			m.SetResume(restoredResumePath, resumeState.PositionSec)
		case playlist.IsMixcloudURL(resumeState.Path) || (!defaultRadio && len(positional) > 0):
			m.SetResume(resumeState.Path, resumeState.PositionSec)
		}
	}

	// Detached mode runs this same player against a virtual terminal instead
	// of the one it was started from: playback, providers, plugins, and IPC
	// are unchanged, and `cliamp attach` lends a terminal to the running
	// program whenever somebody wants to look at it.
	progOpts := programOptions(cfg.LowPower)
	var prog *tea.Program
	var host *session.Host
	if daemon {
		m.SetDetached(true)
		// These callbacks read prog, which is assigned below. Nothing can run
		// them before that: the host only reaches them from an attach, and
		// attaches only arrive once the IPC server has the host registered,
		// which happens further down. Keep that order.
		host, err = session.New(session.Options{
			OnAttach: func(int, int) { prog.Send(model.SetDetachedMsg{Detached: false}) },
			OnDetach: func() { prog.Send(model.SetDetachedMsg{Detached: true}) },
		})
		if err != nil {
			return fmt.Errorf("session: %w", err)
		}
		// The quit key hands the terminal back instead of stopping the music.
		// Ending the session is `cliamp quit`, not a keystroke: its lifetime
		// belongs to whatever started it.
		m.SetSessionDetach(host.Detach)
		defer host.Close()
		progOpts = append(progOpts, sessionProgramOptions(host)...)
	}
	prog = tea.NewProgram(m, progOpts...)
	if host != nil {
		host.SetProgram(prog)
	}
	stopSignals := quitOnSignals(prog.Send)
	defer stopSignals()
	defer providers.observeAuthURLs(prog.Send)()

	svc, svcErr := wireMediaCtl(prog)
	if svcErr != nil {
		applog.Warn("media control (MPRIS/NowPlaying) unavailable: %v", svcErr)
	} else if svc != nil {
		defer svc.Close()
	}

	if luaMgr != nil {
		luaSend, stopLuaSend := newOrderedSender(prog.Send)
		defer stopLuaSend()
		luaMgr.SetControlProvider(luaControlProvider(luaSend))
		luaMgr.SetUIProvider(luaUIProvider(luaSend))
	}

	var attach ipc.AttachHandler
	if host != nil {
		attach = host
	}
	stopIPC, err := startIPC(prog.Send, pluginBroker, luaMgr, attach)
	if err != nil {
		return err
	}
	defer stopIPC()
	if daemon {
		fmt.Fprintf(os.Stderr, "cliamp: running detached (socket: %s)\ncliamp: attach with `cliamp attach`; q detaches, `cliamp quit` stops it\n", ipc.DefaultSocketPath())
		applog.Info("session: running detached")
	}

	finalModel, err := mediactl.Run(prog, svc)
	if host != nil {
		// Tell an attached client why its terminal is going away, before the
		// deferred IPC shutdown closes the connection under it.
		host.Close()
	}
	if err != nil {
		return err
	}
	saveOnExit(finalModel, resumeServer)
	if fm, ok := finalModel.(model.Model); ok {
		fm.WaitReports(reportsExitWait)
	}
	return nil
}

// reportsExitWait bounds the wait at exit for the playback reports that the
// Model queued, such as the scrobble of the track that played at quit.
const reportsExitWait = 3 * time.Second

// checkNotRunning returns an error when another instance serves the socket.
// A detached session calls it before it builds the providers, opens the
// audio device or loads the plugins. The app.quit hooks of the plugins could
// otherwise change the files of the running instance. startIPC still
// catches an instance that starts after the check.
func checkNotRunning() error {
	socket := ipc.DefaultSocketPath()
	running, err := ipc.Listening(socket)
	if err != nil {
		return err
	}
	if running {
		return fmt.Errorf("cliamp is already running (socket %s)", socket)
	}
	return nil
}

// searchArgs turns cliamp search and cliamp search-sc into one argument that
// plays the first match on YouTube or SoundCloud. It returns other arguments
// as they are.
func searchArgs(positional []string) ([]string, error) {
	if len(positional) == 0 || (positional[0] != "search" && positional[0] != "search-sc") {
		return positional, nil
	}
	if len(positional) == 1 {
		return nil, fmt.Errorf("search requires a query string (e.g. cliamp search \"never gonna give you up\")")
	}
	prefix := "ytsearch1:"
	if positional[0] == "search-sc" {
		prefix = "scsearch1:"
	}
	return []string{prefix + strings.Join(positional[1:], " ")}, nil
}

// newPlayer opens the audio output that cfg selects. closePlayer releases
// the player and then the audio device.
func newPlayer(cfg config.Config) (p *player.Player, closePlayer func(), err error) {
	releaseDevice := func() {}
	if cfg.AudioDevice != "" {
		releaseDevice = player.PrepareAudioDevice(cfg.AudioDevice)
	}

	sampleRate := cfg.SampleRate
	if sampleRate == 0 {
		if detected := player.DeviceSampleRate(); detected > 0 {
			sampleRate = detected
		} else {
			sampleRate = 44100
		}
	}

	p, err = player.New(player.Quality{
		SampleRate:      sampleRate,
		BufferMs:        cfg.BufferMs,
		ResampleQuality: cfg.ResampleQuality,
		BitDepth:        cfg.BitDepth,
	})
	if err != nil {
		releaseDevice()
		return nil, nil, fmt.Errorf("player: %w", err)
	}
	return p, func() {
		p.Close()
		releaseDevice()
	}, nil
}

// configureModel applies the settings of cfg to m.
func configureModel(m *model.Model, cfg config.Config, visualizer60FPS bool) {
	m.SetCustomEQBands(cfg.EQ)
	m.SetPadding(cfg.PaddingH, cfg.PaddingV)
	m.SetVisVolumeLinked(cfg.VisVolumeLinked)
	m.SetSeekStepLarge(cfg.SeekStepLargeDuration())
	m.SetLyricsOffset(cfg.LyricsOffsetMs)
	m.SetInitialDirectory(cfg.InitialDirectory)
	m.SetDownloadsDirectory(cfg.Downloads.Directory)
	if cfg.EQPreset != "" && cfg.EQPreset != "Custom" {
		m.SetEQPreset(cfg.EQPreset, nil)
	}
	if cfg.Theme != "" {
		m.SetTheme(cfg.Theme)
	}
	m.SetVisRows(cfg.VisRows)
	m.SetVisualizer60FPS(visualizer60FPS)
	if cfg.Visualizer != "" {
		m.SetVisualizer(cfg.Visualizer)
	}
	if cfg.LowPower {
		m.SetLowPower(true)
	}
	if cfg.Simplified {
		m.SetSimplified(true)
	}
	if cfg.HideHelpBar {
		m.SetHideHelpBar(true)
	}
	if cfg.HideSettingsPane {
		m.SetHideSettingsPane(true)
	}
	if cfg.ShowMetadata {
		m.SetShowMetadata(true)
	}
	if cfg.Expanded {
		m.SetExpanded(true)
	}
}

// startIPC serves the socket and sends its requests to the program through
// send. A non-nil attach takes over the attach requests: that is a detached
// session, controlled only through the socket, so there a failure is an
// error. The TUI reports the failure and runs without the socket.
func startIPC(send func(tea.Msg), broker *ipc.Broker, plugins *luaplugin.Manager, attach ipc.AttachHandler) (stop func(), err error) {
	srv, err := ipc.NewServerWithBroker(ipc.DefaultSocketPath(), broker)
	if err != nil {
		// The errors of the ipc package already start with "ipc: ".
		if attach != nil {
			return nil, err
		}
		fmt.Fprintln(os.Stderr, err)
		return func() {}, nil
	}
	// Program.Send may wait for the update loop, so the requests go through
	// an ordered queue and the socket can acknowledge a job at once.
	queue, stopQueue := newOrderedSender(send)
	srv.SetV2Dispatcher(newV2Dispatcher(queue, srv.JobStore(), plugins))
	srv.SetOperationRegistry(v2Operations(plugins != nil))
	if attach != nil {
		srv.SetAttachHandler(attach)
	}
	go publishV2JobEvents(srv.Done(), srv.JobStore(), broker)
	return func() {
		_ = srv.Close()
		stopQueue()
	}, nil
}

// saveOnExit keeps the theme and the resume position of the final Model.
// When a Jellyfin or Emby server is the default provider, it also keeps the
// list that the track played from.
func saveOnExit(final tea.Model, resumeServer *embyapi.Provider) {
	fm, ok := final.(model.Model)
	if !ok {
		return
	}
	themeName := fm.ThemeName()
	if theme.IsDefaultName(themeName) {
		themeName = ""
	}
	_ = config.SaveString("theme", themeName)

	path, secs, playlistName := fm.ResumeState()
	saveExitResume(path, secs, playlistName, fm.ResumeContext, resumeServer)
}

// saveExitResume saves the track and the position of the exit. When
// resumeServer is set and path is a Jellyfin or Emby stream, it also saves
// the list that resumeContext returns. A track with no position saves
// nothing.
func saveExitResume(path string, secs int, playlistName string, resumeContext func() ([]playlist.Track, int), resumeServer *embyapi.Provider) {
	if path == "" || secs <= 0 {
		return
	}
	if resumeServer != nil && embyapi.IsStreamURL(path) {
		tracks, index := resumeContext()
		resume.SaveState(resume.State{
			Path: path, PositionSec: secs, Playlist: playlistName,
			Context: tracks, ContextIndex: index,
		})
		return
	}
	resume.Save(path, secs, playlistName)
}

// programOptions returns the Bubbletea options of the TUI. run handles the
// signals itself, see quitOnSignals.
func programOptions(lowPower bool) []tea.ProgramOption {
	if lowPower {
		return []tea.ProgramOption{tea.WithFPS(lowPowerUIFPS), tea.WithoutSignalHandler()}
	}
	return []tea.ProgramOption{tea.WithFPS(defaultUIFPS), tea.WithoutSignalHandler()}
}

// sessionProgramOptions render the program into host, the virtual terminal of
// a detached session, instead of this process's own terminal.
func sessionProgramOptions(host *session.Host) []tea.ProgramOption {
	return []tea.ProgramOption{
		tea.WithInput(host.Input()),
		tea.WithOutput(host.Output()),
		// A session outlives every client, so its color depth cannot be
		// negotiated per attach: render truecolor and let each client
		// downsample for its own terminal.
		tea.WithColorProfile(colorprofile.TrueColor),
		tea.WithEnvironment([]string{"TERM=xterm-256color", "COLORTERM=truecolor"}),
		tea.WithWindowSize(detachedCols, detachedRows),
	}
}

// quitOnSignals sends SIGINT, SIGTERM and SIGHUP to quitOnSignal until stop
// is called.
func quitOnSignals(send func(tea.Msg)) (stop func()) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go quitOnSignal(signals, send)
	return func() {
		signal.Stop(signals)
		close(signals)
	}
}

// quitOnSignal asks the Model to quit on the first SIGINT, SIGTERM or
// SIGHUP, so it saves the resume position as the q key does. A closed
// terminal sends SIGHUP. The signal handler of Bubbletea ends the program
// with no Update, and on SIGINT it also returns an error. After the first
// signal the default action is back, so a second signal ends a program that
// does not quit.
func quitOnSignal(signals chan os.Signal, send func(tea.Msg)) {
	if _, ok := <-signals; ok {
		signal.Stop(signals)
		send(playback.QuitMsg{})
	}
}

// v2Operations returns the V2 operations that this runtime serves. The
// plugin operations need the plugin manager.
func v2Operations(plugins bool) *ipc.OperationRegistry {
	operations := ipc.DefaultOperationRegistry()
	if !plugins {
		operations.Unregister("plugin.call", "plugin.commands")
	}
	return operations
}

// v2ReplyTimeout bounds the wait for the Model to answer state.get and
// spectrum.get.
var v2ReplyTimeout = 3 * time.Second

// newV2Dispatcher answers the V2 requests of the TUI and of a detached session.
// send delivers a request to the Model. It must return at once and keep the
// order of the requests, as the queue of newOrderedSender does, so a job is
// acknowledged before the Model reads it and jobs run in the order they came
// in. The plugin jobs run against plugins.
func newV2Dispatcher(send func(tea.Msg), jobs *ipc.JobStore, plugins *luaplugin.Manager) ipc.V2Dispatcher {
	return ipc.V2DispatcherFunc(func(ctx context.Context, request ipc.V2Request) (ipc.V2Result, *ipc.V2Error) {
		switch request.Method {
		case "state.get", "spectrum.get":
			reply := make(chan model.V2RequestResult, 1)
			send(model.V2RequestMsg{Request: request, Reply: reply})
			select {
			case result := <-reply:
				return result.Result, result.Error
			case <-ctx.Done():
				return ipc.V2Result{}, &ipc.V2Error{Code: ipc.V2ErrorCodeCanceled, Message: ipc.V2MessageCanceled}
			case <-time.After(v2ReplyTimeout):
				return ipc.V2Result{}, &ipc.V2Error{Code: ipc.V2ErrorCodeUnavailable, Message: ipc.V2MessageUnavailable}
			}
		}

		job, err := jobs.CreateWithContext(ctx, request.Operation)
		if err != nil {
			return ipc.V2Result{}, &ipc.V2Error{Code: ipc.V2ErrorCodeConflict, Message: ipc.V2MessageConflict}
		}
		if request.Operation == "plugin.call" || request.Operation == "plugin.commands" {
			go runV2PluginJob(jobs, job.ID, request, plugins)
			return ipc.V2Result{Job: &job}, nil
		}
		send(model.V2RequestMsg{Request: request, Jobs: jobs, JobID: job.ID})
		return ipc.V2Result{Job: &job}, nil
	})
}

func runV2PluginJob(jobs *ipc.JobStore, jobID string, request ipc.V2Request, plugins *luaplugin.Manager) {
	ctx, err := jobs.Start(jobID)
	if err != nil || ctx.Err() != nil {
		return
	}
	if plugins == nil {
		_ = jobs.Fail(jobID, ipc.V2Error{Code: ipc.V2ErrorCodeUnavailable, Message: ipc.V2MessageUnavailable})
		return
	}
	if request.Operation == "plugin.commands" {
		data, err := json.Marshal(ipc.Response{OK: true, Items: plugins.CommandList()})
		if err != nil {
			_ = jobs.Fail(jobID, ipc.V2Error{Code: ipc.V2ErrorCodeInternal, Message: ipc.V2MessageInternal})
			return
		}
		_ = jobs.Succeed(jobID, data)
		return
	}

	var params ipc.Request
	if err := json.Unmarshal(request.Params, &params); err != nil || params.Name == "" || params.Sub == "" {
		_ = jobs.Fail(jobID, ipc.V2Error{Code: ipc.V2ErrorCodeInvalidParams, Message: ipc.V2MessageInvalidParams})
		return
	}
	output, err := plugins.EmitCommand(ctx, params.Name, params.Sub, params.Args)
	if err != nil {
		_ = jobs.Fail(jobID, ipc.V2Error{Code: ipc.V2ErrorCodeInternal, Message: ipc.V2MessageInternal, Detail: err.Error()})
		return
	}
	data, err := json.Marshal(ipc.Response{OK: true, Output: output})
	if err != nil {
		_ = jobs.Fail(jobID, ipc.V2Error{Code: ipc.V2ErrorCodeInternal, Message: ipc.V2MessageInternal})
		return
	}
	_ = jobs.Succeed(jobID, data)
}

func publishV2JobEvents(done <-chan struct{}, jobs *ipc.JobStore, broker *ipc.Broker) {
	for {
		select {
		case <-done:
			return
		case event := <-jobs.Events():
			data, err := json.Marshal(event)
			if err == nil {
				_ = broker.Publish("runtime.job", data, false)
			}
		}
	}
}

// initLogging always returns a non-nil close func so the caller can defer
// it unconditionally, plus the applied level as a string for diagnostics.
// Errors come back as the third return value; the close func is a no-op
// and the level string is empty in that case.
func initLogging(levelStr string) (func() error, string, error) {
	noop := func() error { return nil }
	level, err := applog.ParseLevel(levelStr)
	if err != nil {
		return noop, "", err
	}
	dir, err := appdir.Dir()
	if err != nil {
		return noop, "", fmt.Errorf("resolve config dir: %w", err)
	}
	closeFn, err := applog.Init(filepath.Join(dir, "cliamp.log"), level)
	if err != nil {
		return noop, "", err
	}
	return closeFn, level.String(), nil
}

func wireMediaCtl(prog *tea.Program) (*mediactl.Service, error) {
	svc, err := mediactl.New(prog.Send)
	if err != nil || svc == nil {
		return svc, err
	}
	go prog.Send(model.AttachNotifier(svc))
	return svc, nil
}

func main() {
	appmeta.SetVersion(buildVersion())
	app := buildApp()
	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
