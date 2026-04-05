//go:build darwin

package mediactl

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework MediaPlayer -framework Foundation -framework AppKit

#include <stdint.h>
#include <stdlib.h>

#import <AppKit/AppKit.h>
#import <MediaPlayer/MediaPlayer.h>

extern void goMediaPlayPause(uintptr_t handle);
extern void goMediaPlay(uintptr_t handle);
extern void goMediaPause(uintptr_t handle);
extern void goMediaNext(uintptr_t handle);
extern void goMediaPrev(uintptr_t handle);
extern void goMediaStop(uintptr_t handle);
extern void goMediaSetPosition(uintptr_t handle, double positionSeconds);

typedef struct MediaCtlBridge {
	uintptr_t handle;
	id togglePlayPauseTarget;
	id playTarget;
	id pauseTarget;
	id nextTarget;
	id prevTarget;
	id stopTarget;
	id changePlaybackPositionTarget;
} MediaCtlBridge;

typedef void *MediaCtlBridgeRef;

static void initApp(void) {
	[NSApplication sharedApplication];
	[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
}

static void clearNowPlaying(void) {
	MPRemoteCommandCenter *cc = [MPRemoteCommandCenter sharedCommandCenter];
	cc.changePlaybackPositionCommand.enabled = NO;
	[MPNowPlayingInfoCenter defaultCenter].nowPlayingInfo = nil;
	[MPNowPlayingInfoCenter defaultCenter].playbackState = MPNowPlayingPlaybackStateStopped;
}

static MediaCtlBridgeRef bridgeCreate(uintptr_t handle) {
	MediaCtlBridge *bridge = (MediaCtlBridge *)calloc(1, sizeof(MediaCtlBridge));
	if (!bridge) {
		return NULL;
	}

	uintptr_t owner = handle;
	bridge->handle = owner;

	MPRemoteCommandCenter *cc = [MPRemoteCommandCenter sharedCommandCenter];

	bridge->togglePlayPauseTarget = [cc.togglePlayPauseCommand addTargetWithHandler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *event) {
		goMediaPlayPause(owner);
		return MPRemoteCommandHandlerStatusSuccess;
	}];
	bridge->playTarget = [cc.playCommand addTargetWithHandler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *event) {
		goMediaPlay(owner);
		return MPRemoteCommandHandlerStatusSuccess;
	}];
	bridge->pauseTarget = [cc.pauseCommand addTargetWithHandler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *event) {
		goMediaPause(owner);
		return MPRemoteCommandHandlerStatusSuccess;
	}];
	bridge->nextTarget = [cc.nextTrackCommand addTargetWithHandler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *event) {
		goMediaNext(owner);
		return MPRemoteCommandHandlerStatusSuccess;
	}];
	bridge->prevTarget = [cc.previousTrackCommand addTargetWithHandler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *event) {
		goMediaPrev(owner);
		return MPRemoteCommandHandlerStatusSuccess;
	}];
	bridge->stopTarget = [cc.stopCommand addTargetWithHandler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *event) {
		goMediaStop(owner);
		return MPRemoteCommandHandlerStatusSuccess;
	}];
	bridge->changePlaybackPositionTarget = [cc.changePlaybackPositionCommand addTargetWithHandler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *event) {
		MPChangePlaybackPositionCommandEvent *posEvent = (MPChangePlaybackPositionCommandEvent *)event;
		goMediaSetPosition(owner, posEvent.positionTime);
		return MPRemoteCommandHandlerStatusSuccess;
	}];

	return bridge;
}

static void bridgeDestroy(MediaCtlBridgeRef ref) {
	MediaCtlBridge *bridge = (MediaCtlBridge *)ref;
	if (!bridge) {
		clearNowPlaying();
		return;
	}

	MPRemoteCommandCenter *cc = [MPRemoteCommandCenter sharedCommandCenter];

	if (bridge->togglePlayPauseTarget) {
		[cc.togglePlayPauseCommand removeTarget:bridge->togglePlayPauseTarget];
	}
	if (bridge->playTarget) {
		[cc.playCommand removeTarget:bridge->playTarget];
	}
	if (bridge->pauseTarget) {
		[cc.pauseCommand removeTarget:bridge->pauseTarget];
	}
	if (bridge->nextTarget) {
		[cc.nextTrackCommand removeTarget:bridge->nextTarget];
	}
	if (bridge->prevTarget) {
		[cc.previousTrackCommand removeTarget:bridge->prevTarget];
	}
	if (bridge->stopTarget) {
		[cc.stopCommand removeTarget:bridge->stopTarget];
	}
	if (bridge->changePlaybackPositionTarget) {
		[cc.changePlaybackPositionCommand removeTarget:bridge->changePlaybackPositionTarget];
	}

	clearNowPlaying();
	free(bridge);
}

// playbackState: 0 = stopped, 1 = playing, 2 = paused
static void updateNowPlaying(const char *title, const char *artist, const char *album,
                              double durationSecs, double elapsedSecs, int playbackState, int canSeek) {
	@autoreleasepool {
		MPRemoteCommandCenter *cc = [MPRemoteCommandCenter sharedCommandCenter];
		cc.changePlaybackPositionCommand.enabled = canSeek ? YES : NO;

		if (playbackState == 0) {
			[MPNowPlayingInfoCenter defaultCenter].nowPlayingInfo = nil;
			[MPNowPlayingInfoCenter defaultCenter].playbackState = MPNowPlayingPlaybackStateStopped;
			return;
		}

		NSMutableDictionary *info = [NSMutableDictionary dictionary];
		if (title)  info[MPMediaItemPropertyTitle] = @(title);
		if (artist) info[MPMediaItemPropertyArtist] = @(artist);
		if (album)  info[MPMediaItemPropertyAlbumTitle] = @(album);
		if (durationSecs > 0) info[MPMediaItemPropertyPlaybackDuration] = @(durationSecs);
		if (elapsedSecs >= 0) info[MPNowPlayingInfoPropertyElapsedPlaybackTime] = @(elapsedSecs);
		info[MPNowPlayingInfoPropertyPlaybackRate] = @(playbackState == 1 ? 1.0 : 0.0);

		[MPNowPlayingInfoCenter defaultCenter].nowPlayingInfo = info;
		[MPNowPlayingInfoCenter defaultCenter].playbackState = (playbackState == 1)
			? MPNowPlayingPlaybackStatePlaying
			: MPNowPlayingPlaybackStatePaused;
	}
}

static void tickRunLoop(void) {
	[[NSRunLoop currentRunLoop] runMode:NSDefaultRunLoopMode
	                         beforeDate:[NSDate dateWithTimeIntervalSinceNow:0.05]];
}
*/
import "C"

import (
	"fmt"
	"runtime/cgo"
	"sync"
	"time"
	"unsafe"

	tea "charm.land/bubbletea/v2"

	"cliamp/internal/playback"
)

func Run(prog *tea.Program, svc *Service) (tea.Model, error) {
	if svc == nil {
		return prog.Run()
	}

	type result struct {
		model tea.Model
		err   error
	}

	resCh := make(chan result, 1)
	done := make(chan struct{})

	go func() {
		m, err := prog.Run()
		resCh <- result{model: m, err: err}
		close(done)
	}()

	svc.runMainLoop(done)

	res := <-resCh
	return res.model, res.err
}

type updateReq struct {
	title, artist, album      string
	durationSecs, elapsedSecs float64
	status                    playback.Status
	canSeek                   bool
}

type Service struct {
	send     func(tea.Msg)
	sendQ    chan tea.Msg
	sendDone chan struct{}
	updates  chan updateReq

	mu           sync.Mutex
	handle       cgo.Handle
	bridge       C.MediaCtlBridgeRef
	runLoopOwned bool
	released     bool
}

var (
	activeDarwinSvc   *Service
	activeDarwinSvcMu sync.Mutex
)

func claimDarwinService(svc *Service) error {
	activeDarwinSvcMu.Lock()
	defer activeDarwinSvcMu.Unlock()

	if activeDarwinSvc != nil {
		return fmt.Errorf("mediactl: darwin service already active")
	}
	activeDarwinSvc = svc
	return nil
}

func releaseDarwinService(svc *Service) {
	activeDarwinSvcMu.Lock()
	if activeDarwinSvc == svc {
		activeDarwinSvc = nil
	}
	activeDarwinSvcMu.Unlock()
}

func serviceFromHandle(handle uintptr) (svc *Service) {
	if handle == 0 {
		return nil
	}

	defer func() {
		if recover() != nil {
			svc = nil
		}
	}()

	svc, _ = cgo.Handle(handle).Value().(*Service)
	return svc
}

func mediaPlayPause(handle uintptr) {
	if svc := serviceFromHandle(handle); svc != nil {
		svc.dispatch(playback.PlayPauseMsg{})
	}
}

func mediaPlay(handle uintptr) {
	if svc := serviceFromHandle(handle); svc != nil {
		svc.dispatch(playback.PlayMsg{})
	}
}

func mediaPause(handle uintptr) {
	if svc := serviceFromHandle(handle); svc != nil {
		svc.dispatch(playback.PauseMsg{})
	}
}

func mediaNext(handle uintptr) {
	if svc := serviceFromHandle(handle); svc != nil {
		svc.dispatch(playback.NextMsg{})
	}
}

func mediaPrev(handle uintptr) {
	if svc := serviceFromHandle(handle); svc != nil {
		svc.dispatch(playback.PrevMsg{})
	}
}

func mediaStop(handle uintptr) {
	if svc := serviceFromHandle(handle); svc != nil {
		svc.dispatch(playback.StopMsg{})
	}
}

func mediaSetPosition(handle uintptr, positionSeconds float64) {
	if svc := serviceFromHandle(handle); svc != nil {
		svc.dispatch(playback.SetPositionMsg{Position: time.Duration(positionSeconds * float64(time.Second))})
	}
}

//export goMediaPlayPause
func goMediaPlayPause(handle C.uintptr_t) {
	mediaPlayPause(uintptr(handle))
}

//export goMediaPlay
func goMediaPlay(handle C.uintptr_t) {
	mediaPlay(uintptr(handle))
}

//export goMediaPause
func goMediaPause(handle C.uintptr_t) {
	mediaPause(uintptr(handle))
}

//export goMediaNext
func goMediaNext(handle C.uintptr_t) {
	mediaNext(uintptr(handle))
}

//export goMediaPrev
func goMediaPrev(handle C.uintptr_t) {
	mediaPrev(uintptr(handle))
}

//export goMediaStop
func goMediaStop(handle C.uintptr_t) {
	mediaStop(uintptr(handle))
}

//export goMediaSetPosition
func goMediaSetPosition(handle C.uintptr_t, positionSeconds C.double) {
	mediaSetPosition(uintptr(handle), float64(positionSeconds))
}

func New(send func(tea.Msg)) (*Service, error) {
	svc := &Service{
		send:     send,
		sendQ:    make(chan tea.Msg, 16),
		sendDone: make(chan struct{}),
		updates:  make(chan updateReq, 1),
	}
	svc.handle = cgo.NewHandle(svc)

	if err := claimDarwinService(svc); err != nil {
		svc.handle.Delete()
		return nil, err
	}

	go svc.forwardMessages()

	return svc, nil
}

func (s *Service) dispatch(msg tea.Msg) {
	if s == nil {
		return
	}
	if s.sendQ == nil {
		s.send(msg)
		return
	}

	select {
	case <-s.sendDone:
	case s.sendQ <- msg:
	}
}

func (s *Service) forwardMessages() {
	for {
		select {
		case <-s.sendDone:
			return
		case msg := <-s.sendQ:
			s.send(msg)
		}
	}
}

func (s *Service) stopDispatch() {
	if s == nil || s.sendDone == nil {
		return
	}
	select {
	case <-s.sendDone:
	default:
		close(s.sendDone)
	}
}

// runMainLoop initialises NSApplication and MPRemoteCommandCenter on the
// current goroutine, which must already be locked to the main OS thread,
// then pumps the run loop until done is closed.
func (s *Service) runMainLoop(done <-chan struct{}) {
	if s == nil {
		<-done
		return
	}

	s.mu.Lock()
	if s.released {
		s.mu.Unlock()
		<-done
		return
	}
	s.runLoopOwned = true
	handle := s.handle
	s.mu.Unlock()

	C.initApp()
	bridge := C.bridgeCreate(C.uintptr_t(handle))

	s.mu.Lock()
	if s.released {
		s.mu.Unlock()
		C.bridgeDestroy(bridge)
		return
	}
	s.bridge = bridge
	s.mu.Unlock()

	for {
		select {
		case req := <-s.updates:
			applyUpdate(req)
		default:
		}

		C.tickRunLoop()

		select {
		case <-done:
			s.releaseMainThreadResources()
			return
		default:
		}
	}
}

func (s *Service) releaseMainThreadResources() {
	handle, bridge, ok := s.beginRelease(true)
	if !ok {
		return
	}
	C.bridgeDestroy(bridge)
	if handle != 0 {
		handle.Delete()
	}
	releaseDarwinService(s)
	s.stopDispatch()
}

func (s *Service) releaseOwnership() {
	handle, _, ok := s.beginRelease(false)
	if !ok {
		return
	}
	if handle != 0 {
		handle.Delete()
	}
	releaseDarwinService(s)
	s.stopDispatch()
}

func (s *Service) beginRelease(allowRunLoop bool) (cgo.Handle, C.MediaCtlBridgeRef, bool) {
	if s == nil {
		return 0, nil, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.released {
		return 0, nil, false
	}
	if s.runLoopOwned && !allowRunLoop {
		return 0, nil, false
	}

	bridge := s.bridge
	s.bridge = nil
	handle := s.handle
	s.handle = 0
	s.released = true
	return handle, bridge, true
}

func applyUpdate(req updateReq) {
	var cTitle, cArtist, cAlbum *C.char
	if req.title != "" {
		cTitle = C.CString(req.title)
		defer C.free(unsafe.Pointer(cTitle))
	}
	if req.artist != "" {
		cArtist = C.CString(req.artist)
		defer C.free(unsafe.Pointer(cArtist))
	}
	if req.album != "" {
		cAlbum = C.CString(req.album)
		defer C.free(unsafe.Pointer(cAlbum))
	}
	canSeek := C.int(0)
	if req.canSeek {
		canSeek = 1
	}
	C.updateNowPlaying(cTitle, cArtist, cAlbum,
		C.double(req.durationSecs), C.double(req.elapsedSecs), nowPlayingState(req.status), canSeek)
}

func nowPlayingState(status playback.Status) C.int {
	switch status {
	case playback.StatusPlaying:
		return 1
	case playback.StatusPaused:
		return 2
	}
	return 0
}

func (s *Service) Update(state playback.State) {
	if s == nil {
		return
	}

	req := updateReq{
		title:        state.Track.Title,
		artist:       state.Track.Artist,
		album:        state.Track.Album,
		durationSecs: state.Track.Duration.Seconds(),
		elapsedSecs:  state.Position.Seconds(),
		status:       state.Status,
		canSeek:      state.Seekable,
	}

	select {
	case s.updates <- req:
	default:
		select {
		case <-s.updates:
		default:
		}
		s.updates <- req
	}
}

func (s *Service) Seeked(position time.Duration) {}

func (s *Service) Close() {
	if s == nil {
		return
	}

	s.mu.Lock()
	if s.released {
		s.mu.Unlock()
		return
	}
	if s.runLoopOwned {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.releaseOwnership()
}
