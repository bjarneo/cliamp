//go:build darwin && cgo

package mediactl

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"runtime/cgo"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/playback"
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
		Track:    playback.Track{Title: "second", Artist: "artist", ArtURL: "file:///tmp/cover.jpg"},
		Position: 2250 * time.Millisecond,
		Seekable: false,
	})

	select {
	case got := <-svc.updates:
		want := updateReq{
			title:        "second",
			artist:       "artist",
			artURL:       "file:///tmp/cover.jpg",
			durationSecs: 0,
			elapsedSecs:  2.25,
			status:       playback.StatusPlaying,
			canSeek:      false,
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

func TestDarwinDispatchNonBlockingAndCoalescesSeeks(t *testing.T) {
	firstStarted := make(chan struct{})
	release := make(chan struct{})
	var got []tea.Msg
	gotCh := make(chan tea.Msg, 8)
	var count atomic.Int32

	svc := &Service{
		send: func(msg tea.Msg) {
			if count.Add(1) == 1 {
				close(firstStarted)
				<-release
			}
			gotCh <- msg
		},
		wake:     make(chan struct{}, 1),
		sendDone: make(chan struct{}),
	}
	go svc.forwardMessages()
	defer svc.stopDispatch()

	svc.dispatch(playback.PlayMsg{})
	<-firstStarted

	// Rapid seeks while send is blocked must not block and must coalesce to the last seek.
	for i := 1; i <= 5; i++ {
		svc.dispatch(playback.SetPositionMsg{Position: time.Duration(i) * time.Second})
	}
	svc.dispatch(playback.NextMsg{})
	close(release)

	for range 3 {
		select {
		case msg := <-gotCh:
			got = append(got, msg)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for forwarded messages, got %#v", got)
		}
	}
	want := []tea.Msg{
		playback.PlayMsg{},
		playback.SetPositionMsg{Position: 5 * time.Second},
		playback.NextMsg{},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("message %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestDarwinResolveArtworkSchemesAndAsyncCache(t *testing.T) {
	var hits atomic.Int32
	payload := []byte("fake-image-bytes")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/huge" {
			_, _ = w.Write(bytes.Repeat([]byte("a"), maxRemoteArtBytes+10))
			return
		}
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	tests := []struct {
		name    string
		req     updateReq
		wantURL string
	}{
		{
			name:    "file url passes through synchronously",
			req:     updateReq{status: playback.StatusPlaying, artURL: "file:///tmp/cover.jpg"},
			wantURL: "file:///tmp/cover.jpg",
		},
		{
			name:    "http url is skipped",
			req:     updateReq{status: playback.StatusPlaying, artURL: "http://example.com/cover.jpg"},
			wantURL: "",
		},
		{
			name:    "stopped status clears artwork",
			req:     updateReq{status: playback.StatusStopped, artURL: "file:///tmp/cover.jpg"},
			wantURL: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &Service{updates: make(chan updateReq, 1), sendDone: make(chan struct{})}
			gotURL, gotBytes := svc.resolveArtwork(tt.req)
			if gotURL != tt.wantURL || len(gotBytes) != 0 {
				t.Fatalf("resolveArtwork() = (%q, %d bytes), want (%q, 0)", gotURL, len(gotBytes), tt.wantURL)
			}
		})
	}

	t.Run("https fetches once asynchronously and reuses cache on ticks", func(t *testing.T) {
		svc := &Service{
			updates:   make(chan updateReq, 1),
			artReady:  make(chan struct{}, 1),
			sendDone:  make(chan struct{}),
			artClient: srv.Client(),
		}
		defer svc.stopDispatch()

		req := updateReq{status: playback.StatusPlaying, artURL: srv.URL + "/cover.jpg", elapsedSecs: 1}
		if gotURL, gotBytes := svc.resolveArtwork(req); gotURL != "" || len(gotBytes) != 0 {
			t.Fatalf("initial resolveArtwork() = (%q, %d bytes), want empty while fetching", gotURL, len(gotBytes))
		}
		select {
		case <-svc.artReady:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for async artwork completion")
		}

		req.elapsedSecs = 2
		gotURL, gotBytes := svc.resolveArtwork(req)
		if gotURL != req.artURL || !bytes.Equal(gotBytes, payload) {
			t.Fatalf("post-fetch resolveArtwork() = (%q, %q), want (%q, %q)", gotURL, gotBytes, req.artURL, payload)
		}

		// Subsequent 1-second position ticks hit cache with zero bytes and no new HTTP fetch.
		req.elapsedSecs = 3
		gotURL, gotBytes = svc.resolveArtwork(req)
		if gotURL != req.artURL || len(gotBytes) != 0 {
			t.Fatalf("cached tick resolveArtwork() = (%q, %d bytes), want (%q, 0)", gotURL, len(gotBytes), req.artURL)
		}
		if got := hits.Load(); got != 1 {
			t.Fatalf("HTTP fetch count = %d, want 1", got)
		}
	})

	t.Run("oversized remote artwork is rejected", func(t *testing.T) {
		svc := &Service{
			updates:   make(chan updateReq, 1),
			artReady:  make(chan struct{}, 1),
			sendDone:  make(chan struct{}),
			artClient: srv.Client(),
		}
		defer svc.stopDispatch()

		req := updateReq{status: playback.StatusPlaying, artURL: srv.URL + "/huge"}
		svc.resolveArtwork(req)
		select {
		case <-svc.artReady:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for oversized artwork rejection")
		}
		gotURL, gotBytes := svc.resolveArtwork(req)
		if gotURL != req.artURL || len(gotBytes) != 0 {
			t.Fatalf("oversized resolveArtwork() = (%q, %d bytes), want (%q, 0)", gotURL, len(gotBytes), req.artURL)
		}
	})
}
