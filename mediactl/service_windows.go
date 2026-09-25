//go:build windows

package mediactl

import (
	"runtime"
	"sync"
	"time"
	"unsafe"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/sys/windows"

	"github.com/bjarneo/cliamp/internal/playback"
)

const (
	vkMediaNextTrack = 0xB0
	vkMediaPrevTrack = 0xB1
	vkMediaStop      = 0xB2
	vkMediaPlayPause = 0xB3

	modNoRepeat = 0x4000
	modWin      = 0x0008

	wmHotkey = 0x0312

	hotkeyIDPlayPause = 1
	hotkeyIDNext      = 2
	hotkeyIDPrev      = 3
	hotkeyIDStop      = 4

	// Win-held variants of the same four keys. Some keyboard remappers (e.g.
	// kanata layers bound to a held Win/Super key) re-emit the real Windows
	// key as held output while sending the media VK, so the OS reports Win
	// as down at the moment the media key fires. RegisterHotKey only matches
	// the exact modifier set it was given, so registering these VKs with
	// modWin as well means cliamp responds whether or not Win happens to be
	// held, without needing a second no-modifier registration to guess right.
	hotkeyIDPlayPauseWin = 5
	hotkeyIDNextWin      = 6
	hotkeyIDPrevWin      = 7
	hotkeyIDStopWin      = 8
)

var hotkeyVKs = map[int]uint32{
	hotkeyIDPlayPause: vkMediaPlayPause,
	hotkeyIDNext:      vkMediaNextTrack,
	hotkeyIDPrev:      vkMediaPrevTrack,
	hotkeyIDStop:      vkMediaStop,

	hotkeyIDPlayPauseWin: vkMediaPlayPause,
	hotkeyIDNextWin:      vkMediaNextTrack,
	hotkeyIDPrevWin:      vkMediaPrevTrack,
	hotkeyIDStopWin:      vkMediaStop,
}

// hotkeyModifiers returns the fsModifiers to register each hotkey ID with.
// The Win-held IDs need modWin added; everything else just gets modNoRepeat.
func hotkeyModifiers(id int) uint32 {
	switch id {
	case hotkeyIDPlayPauseWin, hotkeyIDNextWin, hotkeyIDPrevWin, hotkeyIDStopWin:
		return modNoRepeat | modWin
	default:
		return modNoRepeat
	}
}

var (
	user32                 = windows.NewLazySystemDLL("user32.dll")
	procRegisterHotKey     = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey   = user32.NewProc("UnregisterHotKey")
	procGetMessageW        = user32.NewProc("GetMessageW")
	procTranslateMessage   = user32.NewProc("TranslateMessage")
	procDispatchMessageW   = user32.NewProc("DispatchMessageW")
	procPostThreadMessageW = user32.NewProc("PostThreadMessageW")
)

// win32Msg mirrors the Win32 MSG struct. Field order and types must match
// https://learn.microsoft.com/windows/win32/api/winuser/ns-winuser-msg
// exactly; Go's default struct alignment on amd64 matches the C layout here.
type win32Msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	ptX     int32
	ptY     int32
}

type Service struct {
	send      func(tea.Msg)
	threadID  uint32
	stopped   chan struct{}
	closeOnce sync.Once
}

// New registers the media-key hotkeys and starts pumping their Win32 message
// loop on a dedicated background goroutine, then returns once that loop is
// ready. This mirrors service_linux.go's New(), which synchronously connects
// to D-Bus and exports MPRIS — callers (including the headless daemon, which
// never calls Run) only need New for OS media-key integration to be live.
func New(send func(tea.Msg)) (*Service, error) {
	svc := &Service{send: send, stopped: make(chan struct{})}

	ready := make(chan uint32, 1)
	go svc.runMessageLoop(ready)
	svc.threadID = <-ready

	return svc, nil
}

func Run(prog *tea.Program, svc *Service) (tea.Model, error) {
	return prog.Run()
}

// runMessageLoop must run on a dedicated OS thread: Win32 message queues are
// thread-affine, so the thread that registers the hotkeys must be the same
// thread that later retrieves WM_HOTKEY via GetMessageW. The loop needs no
// visible window: RegisterHotKey(NULL, ...) ties hotkeys to the calling
// thread's message queue rather than to an HWND.
func (s *Service) runMessageLoop(ready chan<- uint32) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(s.stopped)

	for id, vk := range hotkeyVKs {
		registerHotKey(id, vk)
	}
	defer func() {
		for id := range hotkeyVKs {
			unregisterHotKey(id)
		}
	}()

	ready <- windows.GetCurrentThreadId()

	var m win32Msg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) <= 0 {
			return
		}

		if m.message == wmHotkey {
			if msg, ok := hotkeyMsg(int32(m.wParam)); ok {
				s.send(msg)
			}
			continue
		}

		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// hotkeyMsg translates a registered hotkey id (WM_HOTKEY's wParam) into the
// playback command it represents.
func hotkeyMsg(id int32) (tea.Msg, bool) {
	switch id {
	case hotkeyIDPlayPause, hotkeyIDPlayPauseWin:
		return playback.PlayPauseMsg{}, true
	case hotkeyIDNext, hotkeyIDNextWin:
		return playback.NextMsg{}, true
	case hotkeyIDPrev, hotkeyIDPrevWin:
		return playback.PrevMsg{}, true
	case hotkeyIDStop, hotkeyIDStopWin:
		return playback.StopMsg{}, true
	default:
		return nil, false
	}
}

// registerHotKey best-effort registers a media key: failure (e.g. another
// process already holds it) just means that key won't control cliamp.
func registerHotKey(id int, vk uint32) {
	procRegisterHotKey.Call(0, uintptr(id), uintptr(hotkeyModifiers(id)), uintptr(vk))
}

func unregisterHotKey(id int) {
	procUnregisterHotKey.Call(0, uintptr(id))
}

func postThreadMessageW(threadID uint32) {
	const wmQuit = 0x0012
	procPostThreadMessageW.Call(uintptr(threadID), wmQuit, 0, 0)
}

func (s *Service) Update(state playback.State) {}

func (s *Service) Seeked(position time.Duration) {}

// Close unregisters the hotkeys and stops the message-loop goroutine,
// posting WM_QUIT to unblock its GetMessageW call and waiting for it to
// exit.
func (s *Service) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		postThreadMessageW(s.threadID)
		<-s.stopped
	})
}
