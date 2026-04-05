//go:build darwin && cgo

package mediactl

import (
	"reflect"
	"runtime/cgo"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"cliamp/internal/playback"
)

func TestDarwinCallbacksUseHandleRouting(t *testing.T) {
	msgsA := make(chan tea.Msg, 1)
	svcA := &Service{send: func(msg tea.Msg) { msgsA <- msg }}
	handleA := cgo.NewHandle(svcA)
	defer handleA.Delete()

	msgsB := make(chan tea.Msg, 1)
	svcB := &Service{send: func(msg tea.Msg) { msgsB <- msg }}
	handleB := cgo.NewHandle(svcB)
	defer handleB.Delete()

	mediaNext(uintptr(handleB))

	select {
	case got := <-msgsB:
		if _, ok := got.(playback.NextMsg); !ok {
			t.Fatalf("goMediaNext() sent %T, want %T", got, playback.NextMsg{})
		}
	default:
		t.Fatal("goMediaNext() did not send a message to the owning service")
	}

	select {
	case got := <-msgsA:
		t.Fatalf("goMediaNext() unexpectedly sent %T to a different service", got)
	default:
	}
}

func TestDarwinCallbacksEmitExpectedMessages(t *testing.T) {
	cases := []struct {
		name string
		fire func(uintptr)
		want tea.Msg
	}{
		{name: "play_pause", fire: mediaPlayPause, want: playback.PlayPauseMsg{}},
		{name: "play", fire: mediaPlay, want: playback.PlayMsg{}},
		{name: "pause", fire: mediaPause, want: playback.PauseMsg{}},
		{name: "next", fire: mediaNext, want: playback.NextMsg{}},
		{name: "prev", fire: mediaPrev, want: playback.PrevMsg{}},
		{name: "stop", fire: mediaStop, want: playback.StopMsg{}},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			msgs := make(chan tea.Msg, 1)
			svc := &Service{send: func(msg tea.Msg) { msgs <- msg }}
			handle := cgo.NewHandle(svc)
			defer handle.Delete()

			tt.fire(uintptr(handle))

			select {
			case got := <-msgs:
				if reflect.TypeOf(got) != reflect.TypeOf(tt.want) {
					t.Fatalf("%s sent %T, want %T", tt.name, got, tt.want)
				}
			default:
				t.Fatalf("%s did not send a message", tt.name)
			}
		})
	}
}

func TestDarwinSetPositionCallbackConvertsToMicroseconds(t *testing.T) {
	msgs := make(chan tea.Msg, 1)
	svc := &Service{send: func(msg tea.Msg) { msgs <- msg }}
	handle := cgo.NewHandle(svc)
	defer handle.Delete()

	mediaSetPosition(uintptr(handle), 12.25)

	select {
	case got := <-msgs:
		want := playback.SetPositionMsg{Position: 12250 * time.Millisecond}
		if got != want {
			t.Fatalf("goMediaSetPosition() sent %#v, want %#v", got, want)
		}
	default:
		t.Fatal("goMediaSetPosition() did not send a message")
	}
}

func TestDarwinNewRejectsSecondActiveService(t *testing.T) {
	svc, err := New(func(tea.Msg) {})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := New(func(tea.Msg) {}); err == nil {
		t.Fatal("second New() succeeded, want error")
	}

	svc.Close()

	svc2, err := New(func(tea.Msg) {})
	if err != nil {
		t.Fatalf("New() after Close() error = %v", err)
	}
	svc2.Close()
}

func TestDarwinBeginReleaseAllowsNewServiceAfterRunLoopTeardown(t *testing.T) {
	svc, err := New(func(tea.Msg) {})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	svc.mu.Lock()
	svc.runLoopOwned = true
	svc.mu.Unlock()

	handle, _, ok := svc.beginRelease(true)
	if !ok {
		t.Fatal("beginRelease(true) = false, want true")
	}
	if handle != 0 {
		handle.Delete()
	}
	releaseDarwinService(svc)

	svc2, err := New(func(tea.Msg) {})
	if err != nil {
		t.Fatalf("New() after teardown error = %v", err)
	}
	svc2.Close()
}

func TestDarwinUpdateCoalescesPendingState(t *testing.T) {
	svc := &Service{updates: make(chan updateReq, 1)}

	svc.Update(playback.State{
		Status:   playback.StatusPaused,
		Track:    playback.Track{Title: "first"},
		Position: time.Second,
		Seekable: true,
	})
	svc.Update(playback.State{
		Status:   playback.StatusPlaying,
		Track:    playback.Track{Title: "second", Artist: "artist"},
		Position: 2250 * time.Millisecond,
		Seekable: false,
	})

	select {
	case got := <-svc.updates:
		want := updateReq{
			title:         "second",
			artist:        "artist",
			durationSecs:  0,
			elapsedSecs:   2.25,
			playbackState: 1,
			canSeek:       false,
		}
		if got != want {
			t.Fatalf("Update() queued %#v, want %#v", got, want)
		}
	default:
		t.Fatal("Update() did not queue state")
	}

	select {
	case got := <-svc.updates:
		t.Fatalf("Update() left stale pending state %#v", got)
	default:
	}
}
