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

static NSString *cachedArtKey = nil;
static MPMediaItemArtwork *cachedArtwork = nil;

static void clearArtworkCache(void) {
	if (cachedArtKey) {
		[cachedArtKey release];
		cachedArtKey = nil;
	}
	if (cachedArtwork) {
		[cachedArtwork release];
		cachedArtwork = nil;
	}
}

static void initApp(void) {
	@autoreleasepool {
		[NSApplication sharedApplication];
		[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
	}
}

static void clearNowPlaying(void) {
	MPRemoteCommandCenter *cc = [MPRemoteCommandCenter sharedCommandCenter];
	cc.changePlaybackPositionCommand.enabled = NO;
	[MPNowPlayingInfoCenter defaultCenter].nowPlayingInfo = nil;
	[MPNowPlayingInfoCenter defaultCenter].playbackState = MPNowPlayingPlaybackStateStopped;
	clearArtworkCache();
}

static MediaCtlBridgeRef bridgeCreate(uintptr_t handle) {
	@autoreleasepool {
		MediaCtlBridge *bridge = (MediaCtlBridge *)calloc(1, sizeof(MediaCtlBridge));
		if (!bridge) {
			return NULL;
		}

		uintptr_t owner = handle;
		bridge->handle = owner;

		MPRemoteCommandCenter *cc = [MPRemoteCommandCenter sharedCommandCenter];

		bridge->togglePlayPauseTarget = [[cc.togglePlayPauseCommand addTargetWithHandler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *event) {
			goMediaPlayPause(owner);
			return MPRemoteCommandHandlerStatusSuccess;
		}] retain];
		bridge->playTarget = [[cc.playCommand addTargetWithHandler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *event) {
			goMediaPlay(owner);
			return MPRemoteCommandHandlerStatusSuccess;
		}] retain];
		bridge->pauseTarget = [[cc.pauseCommand addTargetWithHandler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *event) {
			goMediaPause(owner);
			return MPRemoteCommandHandlerStatusSuccess;
		}] retain];
		bridge->nextTarget = [[cc.nextTrackCommand addTargetWithHandler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *event) {
			goMediaNext(owner);
			return MPRemoteCommandHandlerStatusSuccess;
		}] retain];
		bridge->prevTarget = [[cc.previousTrackCommand addTargetWithHandler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *event) {
			goMediaPrev(owner);
			return MPRemoteCommandHandlerStatusSuccess;
		}] retain];
		bridge->stopTarget = [[cc.stopCommand addTargetWithHandler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *event) {
			goMediaStop(owner);
			return MPRemoteCommandHandlerStatusSuccess;
		}] retain];
		bridge->changePlaybackPositionTarget = [[cc.changePlaybackPositionCommand addTargetWithHandler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *event) {
			MPChangePlaybackPositionCommandEvent *posEvent = (MPChangePlaybackPositionCommandEvent *)event;
			goMediaSetPosition(owner, posEvent.positionTime);
			return MPRemoteCommandHandlerStatusSuccess;
		}] retain];

		return bridge;
	}
}

static void bridgeDestroy(MediaCtlBridgeRef ref) {
	@autoreleasepool {
		MediaCtlBridge *bridge = (MediaCtlBridge *)ref;
		if (!bridge) {
			clearNowPlaying();
			return;
		}

		MPRemoteCommandCenter *cc = [MPRemoteCommandCenter sharedCommandCenter];

		if (bridge->togglePlayPauseTarget) {
			[cc.togglePlayPauseCommand removeTarget:bridge->togglePlayPauseTarget];
			[bridge->togglePlayPauseTarget release];
		}
		if (bridge->playTarget) {
			[cc.playCommand removeTarget:bridge->playTarget];
			[bridge->playTarget release];
		}
		if (bridge->pauseTarget) {
			[cc.pauseCommand removeTarget:bridge->pauseTarget];
			[bridge->pauseTarget release];
		}
		if (bridge->nextTarget) {
			[cc.nextTrackCommand removeTarget:bridge->nextTarget];
			[bridge->nextTarget release];
		}
		if (bridge->prevTarget) {
			[cc.previousTrackCommand removeTarget:bridge->prevTarget];
			[bridge->prevTarget release];
		}
		if (bridge->stopTarget) {
			[cc.stopCommand removeTarget:bridge->stopTarget];
			[bridge->stopTarget release];
		}
		if (bridge->changePlaybackPositionTarget) {
			[cc.changePlaybackPositionCommand removeTarget:bridge->changePlaybackPositionTarget];
			[bridge->changePlaybackPositionTarget release];
		}

		clearNowPlaying();
		free(bridge);
	}
}

static MPMediaItemArtwork *loadArtwork(const char *artURL, const void *artBytes, int artBytesLen) {
	if (!artURL || artURL[0] == '\0') {
		clearArtworkCache();
		return nil;
	}
	NSString *key = @(artURL);
	if (cachedArtKey && [cachedArtKey isEqualToString:key]) {
		return cachedArtwork;
	}

	clearArtworkCache();
	cachedArtKey = [key copy];

	NSImage *image = nil;
	if (artBytes != NULL && artBytesLen > 0) {
		NSData *data = [NSData dataWithBytes:artBytes length:(NSUInteger)artBytesLen];
		if (data) {
			image = [[[NSImage alloc] initWithData:data] autorelease];
		}
	} else {
		NSURL *url = [NSURL URLWithString:key];
		if (url && [url isFileURL]) {
			image = [[[NSImage alloc] initWithContentsOfURL:url] autorelease];
		}
	}
	if (image) {
		cachedArtwork = [[MPMediaItemArtwork alloc] initWithBoundsSize:image.size
			requestHandler:^NSImage * _Nonnull(CGSize size) {
				return image;
			}];
	}
	return cachedArtwork;
}

// playbackState: 0 = stopped, 1 = playing, 2 = paused
static void updateNowPlaying(const char *title, const char *artist, const char *album,
                              const char *artURL, const void *artBytes, int artBytesLen,
                              double durationSecs, double elapsedSecs, int playbackState, int canSeek) {
	@autoreleasepool {
		MPRemoteCommandCenter *cc = [MPRemoteCommandCenter sharedCommandCenter];
		cc.changePlaybackPositionCommand.enabled = canSeek ? YES : NO;

		if (playbackState == 0) {
			clearNowPlaying();
			return;
		}

		NSMutableDictionary *info = [NSMutableDictionary dictionary];
		if (title)  info[MPMediaItemPropertyTitle] = @(title);
		if (artist) info[MPMediaItemPropertyArtist] = @(artist);
		if (album)  info[MPMediaItemPropertyAlbumTitle] = @(album);
		MPMediaItemArtwork *artwork = loadArtwork(artURL, artBytes, artBytesLen);
		if (artwork) {
			info[MPMediaItemPropertyArtwork] = artwork;
		}
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
	@autoreleasepool {
		[[NSRunLoop currentRunLoop] runMode:NSDefaultRunLoopMode
		                         beforeDate:[NSDate dateWithTimeIntervalSinceNow:0.05]];
	}
}
*/
import "C"

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"runtime/cgo"
	"strings"
	"sync"
	"time"
	"unsafe"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/playback"
)

const (
	maxDispatchQueue  = 64
	maxRemoteArtBytes = 4 << 20
	remoteArtTimeout  = 5 * time.Second
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
	artURL                    string
	durationSecs, elapsedSecs float64
	status                    playback.Status
	canSeek                   bool
}

type Service struct {
	send         func(tea.Msg)
	queueMu      sync.Mutex
	queue        []tea.Msg
	wake         chan struct{}
	sendDone     chan struct{}
	sendDoneOnce sync.Once

	updatesMu sync.Mutex
	updates   chan updateReq
	artReady  chan struct{}

	artMu         sync.Mutex
	artClient     *http.Client
	artURL        string
	artBytes      []byte
	artLoaded     bool
	artAppliedURL string
	artCancel     context.CancelFunc
	lastReq       updateReq

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
		wake:     make(chan struct{}, 1),
		sendDone: make(chan struct{}),
		updates:  make(chan updateReq, 1),
		artReady: make(chan struct{}, 1),
	}
	svc.handle = cgo.NewHandle(svc)

	if err := claimDarwinService(svc); err != nil {
		svc.handle.Delete()
		return nil, err
	}

	go svc.forwardMessages()

	return svc, nil
}

// dispatch queues msg without blocking the macOS main thread. Consecutive seek
// updates coalesce so rapid scrubbing keeps only the latest position.
func (s *Service) dispatch(msg tea.Msg) {
	if s == nil {
		return
	}
	if s.wake == nil {
		if s.send != nil {
			s.send(msg)
		}
		return
	}
	if s.sendDone != nil {
		select {
		case <-s.sendDone:
			return
		default:
		}
	}

	s.queueMu.Lock()
	if _, isSeek := msg.(playback.SetPositionMsg); isSeek && len(s.queue) > 0 {
		if _, lastIsSeek := s.queue[len(s.queue)-1].(playback.SetPositionMsg); lastIsSeek {
			s.queue[len(s.queue)-1] = msg
			s.queueMu.Unlock()
			return
		}
	}
	if len(s.queue) >= maxDispatchQueue {
		copy(s.queue, s.queue[1:])
		s.queue[len(s.queue)-1] = msg
	} else {
		s.queue = append(s.queue, msg)
	}
	s.queueMu.Unlock()

	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) forwardMessages() {
	for {
		select {
		case <-s.sendDone:
			return
		case <-s.wake:
		}
		for {
			s.queueMu.Lock()
			if len(s.queue) == 0 {
				s.queueMu.Unlock()
				break
			}
			msgs := s.queue
			s.queue = nil
			s.queueMu.Unlock()
			for _, msg := range msgs {
				select {
				case <-s.sendDone:
					return
				default:
				}
				if s.send != nil {
					s.send(msg)
				}
			}
		}
	}
}

func (s *Service) stopDispatch() {
	if s == nil {
		return
	}
	s.artMu.Lock()
	s.cancelArtFetchLocked()
	s.artURL = ""
	s.artBytes = nil
	s.artLoaded = false
	s.artAppliedURL = ""
	s.artMu.Unlock()
	if s.sendDone != nil {
		s.sendDoneOnce.Do(func() { close(s.sendDone) })
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
			s.applyUpdate(req)
		default:
			select {
			case <-s.artReady:
				if req, ok := s.pendingArtReplay(); ok {
					s.applyUpdate(req)
				}
			default:
			}
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

func isFileArtURL(raw string) bool {
	return strings.HasPrefix(strings.ToLower(raw), "file://")
}

func isHTTPSArtURL(raw string) bool {
	return strings.HasPrefix(strings.ToLower(raw), "https://")
}

var defaultRemoteArtClient = &http.Client{
	Timeout: remoteArtTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		if !strings.EqualFold(req.URL.Scheme, "https") {
			return fmt.Errorf("non-https artwork redirect")
		}
		return nil
	},
}

func (s *Service) artHTTPClient() *http.Client {
	if s.artClient != nil {
		return s.artClient
	}
	return defaultRemoteArtClient
}

func (s *Service) cancelArtFetchLocked() {
	if s.artCancel != nil {
		s.artCancel()
		s.artCancel = nil
	}
}

// resolveArtwork returns the artwork URL and optional decoded payload for C.
// Only file:// URLs load synchronously in Objective-C. Remote https:// URLs
// download asynchronously on a background goroutine with bounded timeout and size.
func (s *Service) resolveArtwork(req updateReq) (string, []byte) {
	s.artMu.Lock()
	defer s.artMu.Unlock()

	if req.status == playback.StatusStopped || req.artURL == "" {
		s.cancelArtFetchLocked()
		s.artURL = ""
		s.artBytes = nil
		s.artLoaded = false
		s.artAppliedURL = ""
		return "", nil
	}

	if isFileArtURL(req.artURL) {
		s.cancelArtFetchLocked()
		s.artURL = ""
		s.artBytes = nil
		s.artLoaded = false
		s.artAppliedURL = ""
		return req.artURL, nil
	}

	if !isHTTPSArtURL(req.artURL) {
		s.cancelArtFetchLocked()
		s.artURL = ""
		s.artBytes = nil
		s.artLoaded = false
		s.artAppliedURL = ""
		return "", nil
	}

	s.lastReq = req
	if s.artURL == req.artURL {
		if s.artAppliedURL == req.artURL {
			return req.artURL, nil
		}
		if s.artLoaded {
			data := s.artBytes
			s.artBytes = nil
			s.artAppliedURL = req.artURL
			return req.artURL, data
		}
		return "", nil
	}

	s.cancelArtFetchLocked()
	s.artURL = req.artURL
	s.artBytes = nil
	s.artLoaded = false
	s.artAppliedURL = ""
	ctx, cancel := context.WithTimeout(context.Background(), remoteArtTimeout)
	s.artCancel = cancel
	client := s.artHTTPClient()
	go func() {
		defer cancel()
		s.fetchArtAsync(ctx, client, req.artURL)
	}()
	return "", nil
}

func (s *Service) pendingArtReplay() (updateReq, bool) {
	s.artMu.Lock()
	defer s.artMu.Unlock()
	if s.artURL == "" || !s.artLoaded || s.artAppliedURL == s.artURL {
		return updateReq{}, false
	}
	return s.lastReq, true
}

func (s *Service) fetchArtAsync(ctx context.Context, client *http.Client, targetURL string) {
	data := fetchRemoteArt(ctx, client, targetURL)

	s.artMu.Lock()
	if s.artURL != targetURL {
		s.artMu.Unlock()
		return
	}
	s.artBytes = data
	s.artLoaded = true
	s.artMu.Unlock()

	if s.sendDone != nil {
		select {
		case <-s.sendDone:
			return
		default:
		}
	}
	if s.artReady != nil {
		select {
		case s.artReady <- struct{}{}:
		default:
		}
	}
}

func fetchRemoteArt(ctx context.Context, client *http.Client, rawURL string) []byte {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRemoteArtBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxRemoteArtBytes {
		return nil
	}
	return data
}

func (s *Service) applyUpdate(req updateReq) {
	var cTitle, cArtist, cAlbum, cArtURL *C.char
	var cArtBytes unsafe.Pointer
	var cArtBytesLen C.int

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
	artURL, artBytes := s.resolveArtwork(req)
	if artURL != "" {
		cArtURL = C.CString(artURL)
		defer C.free(unsafe.Pointer(cArtURL))
	}
	if len(artBytes) > 0 {
		cArtBytes = C.CBytes(artBytes)
		defer C.free(cArtBytes)
		cArtBytesLen = C.int(len(artBytes))
	}
	canSeek := C.int(0)
	if req.canSeek {
		canSeek = 1
	}
	C.updateNowPlaying(cTitle, cArtist, cAlbum, cArtURL, cArtBytes, cArtBytesLen,
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

func (s *Service) enqueueUpdate(req updateReq) {
	s.updatesMu.Lock()
	defer s.updatesMu.Unlock()
	select {
	case s.updates <- req:
	default:
		select {
		case <-s.updates:
		default:
		}
		select {
		case s.updates <- req:
		default:
		}
	}
}

func (s *Service) Update(state playback.State) {
	if s == nil {
		return
	}

	req := updateReq{
		title:        state.Track.Title,
		artist:       state.Track.Artist,
		album:        state.Track.Album,
		artURL:       state.Track.ArtURL,
		durationSecs: state.Track.Duration.Seconds(),
		elapsedSecs:  state.Position.Seconds(),
		status:       state.Status,
		canSeek:      state.Seekable,
	}

	s.enqueueUpdate(req)
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
