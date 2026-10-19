package spotify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/applog"
	librespot "github.com/devgianlu/go-librespot"
	librespotPlayer "github.com/devgianlu/go-librespot/player"
	connectpb "github.com/devgianlu/go-librespot/proto/spotify/connectstate"
	"github.com/devgianlu/go-librespot/spclient"
)

func testDJSession(t *testing.T, pages ...*connectpb.ContextPage) *DJSession {
	t.Helper()
	resolver, err := spclient.NewContextResolver(t.Context(), &librespot.NullLogger{}, nil, &connectpb.Context{Uri: DJContextURI, Pages: pages})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	d := &DJSession{resolver: resolver, ctx: ctx, cancel: cancel, id: "test", session: &Session{}}
	t.Cleanup(d.Close)
	return d
}

func djTestTrack(script string) *connectpb.ContextTrack {
	return &connectpb.ContextTrack{Uri: "spotify:track:2FY7b99s15jUprqC0M5NCT", Metadata: map[string]string{"title": "Song", "artist_name": "Artist", "duration": "123000", "narration.intro.ssml": script}}
}

func TestDJBatchesKeepNarrationAndExpireOldStreams(t *testing.T) {
	d := testDJSession(t, &connectpb.ContextPage{Tracks: []*connectpb.ContextTrack{djTestTrack("first")}}, &connectpb.ContextPage{Tracks: []*connectpb.ContextTrack{djTestTrack("second")}})
	first, err := d.Next(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if first[0].Title != "Song" || first[0].Artist != "Artist" || first[0].DurationSecs != 123 || first[0].Meta("spotify.dj") != "true" {
		t.Fatalf("track=%+v", first[0])
	}
	if d.entries[first[0].Path].Metadata["narration.intro.ssml"] != "first" {
		t.Fatal("lost narration")
	}
	second, err := d.Next(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if first[0].Path == second[0].Path || len(d.entries) != 1 || d.entries[first[0].Path] != nil {
		t.Fatal("batch retained stale streams")
	}
	if _, err := d.Next(t.Context()); !errors.Is(err, io.EOF) {
		t.Fatalf("exhaustion=%v", err)
	}
}

func TestDJRejectsMusicOnlyOrUnsupportedBatches(t *testing.T) {
	for _, tc := range []struct {
		name  string
		track *connectpb.ContextTrack
	}{
		{"music only", djTestTrack("")},
		{"unsupported", &connectpb.ContextTrack{Uri: "spotify:local:unsupported"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := testDJSession(t, &connectpb.ContextPage{Tracks: []*connectpb.ContextTrack{tc.track}})
			if tracks, err := d.Next(t.Context()); err == nil || len(tracks) > 0 {
				t.Fatalf("tracks=%v error=%v", tracks, err)
			}
			if d.page != 0 || len(d.entries) != 0 {
				t.Fatal("failed batch changed the session")
			}
		})
	}
}

func TestDJCloseCancelsBatchAndStreamerSetup(t *testing.T) {
	d := testDJSession(t, &connectpb.ContextPage{Tracks: []*connectpb.ContextTrack{djTestTrack("script")}})
	tracks, err := d.Next(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	if _, err := d.Next(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	p := New(d.session, "client", 320)
	p.dj = d
	if _, _, _, err := p.NewStreamer(tracks[0].Path); err == nil {
		t.Fatal("closed session opened stream")
	}
}

func TestDJNarrationErrorsAreNotMusicFallback(t *testing.T) {
	d := &DJSession{session: &Session{}}
	if audio, err := d.narration(t.Context(), nil, "narration.intro"); audio != nil || err != nil {
		t.Fatalf("absent line=%v %v", audio, err)
	}
	if audio, err := d.narration(t.Context(), djTestTrack("script").Metadata, "narration.intro"); audio != nil || err == nil || !strings.Contains(err.Error(), "session closed") {
		t.Fatalf("failed line=%v %v", audio, err)
	}
	p := New(nil, "client", 320)
	if _, err := p.ResolveURL(t.Context(), DJContextURI); err == nil || !strings.Contains(err.Error(), "DJ (Local)") {
		t.Fatalf("ordinary resolution=%v", err)
	}
	for _, uri := range []string{DJPlaylistID, DJContextURI} {
		if _, err := p.Tracks(uri); err == nil || !strings.Contains(err.Error(), "DJ (Local)") {
			t.Fatalf("Tracks(%q) error = %v", uri, err)
		}
		if _, _, err := p.TracksPage(uri, 0); err == nil || !strings.Contains(err.Error(), "DJ (Local)") {
			t.Fatalf("TracksPage(%q) error = %v", uri, err)
		}
	}
}

func TestDJResolverUsesLexiconAndRejectsBrokenContexts(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		fail       bool
	}{
		{"ready", `{"pages":[{"tracks":[{"uri":"spotify:track:2FY7b99s15jUprqC0M5NCT"}]}]}`, false},
		{"no pages", `{}`, true},
		{"missing page", `{"pages":[null]}`, true},
		{"loading", `{"loading":true,"pages":[{}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/lexicon-session-provider/context-resolve/v2/session" || r.URL.Query().Get("contextUri") != DJContextURI {
					t.Errorf("unexpected resolver path %s", r.URL.Path)
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			sp, err := spclient.NewSpclient(t.Context(), &librespot.NullLogger{}, server.Client(), func(context.Context) string { return strings.TrimPrefix(server.URL, "https://") }, func(context.Context, bool) (string, error) { return "test-token", nil }, "device", "client")
			if err != nil {
				t.Fatal(err)
			}
			resolver, err := resolveDJContext(t.Context(), sp)
			if (err != nil) != tc.fail {
				t.Fatalf("resolver=%v error=%v", resolver, err)
			}
		})
	}
}

func TestDJResolverBoundsFallbackAndKeepsNextPageUsable(t *testing.T) {
	var calls int
	fallbackDeadline := make(chan time.Duration, 1)
	client := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/lexicon-session-provider/context-resolve/v2/session":
			calls++
			if calls == 1 {
				return jsonResponse(r, `{"uri":"`+DJContextURI+`","metadata":{"playlist_number_of_tracks":"2"},"pages":[{"tracks":[{"uri":"spotify:track:2FY7b99s15jUprqC0M5NCT"}]}]}`)
			}
			deadline, ok := r.Context().Deadline()
			if !ok {
				fallbackDeadline <- -1
			} else {
				fallbackDeadline <- time.Until(deadline)
			}
			return jsonResponse(r, `{"uri":"`+DJContextURI+`","pages":[{"tracks":[{"uri":"spotify:track:2FY7b99s15jUprqC0M5NCT"}],"next_page_url":"hm://dj/next"}]}`)
		case "/dj/next":
			return jsonResponse(r, `{"tracks":[{"uri":"spotify:track:4uLU6hMCjMI75M1A2tKUQC"}]}`)
		default:
			return nil, fmt.Errorf("unexpected request path %s", r.URL.Path)
		}
	})
	sp := playbackAPIClient(t, client)
	resolver, err := resolveDJContext(t.Context(), sp)
	if err != nil {
		t.Fatal(err)
	}
	if remaining := <-fallbackDeadline; remaining <= 0 || remaining > 30*time.Second {
		t.Fatalf("fallback deadline remaining = %v, want within 30 seconds", remaining)
	}
	first, err := resolver.Page(t.Context(), 0)
	if err != nil || len(first) != 1 {
		t.Fatalf("first page = %v, %v", first, err)
	}
	second, err := resolver.Page(t.Context(), 1)
	if err != nil || len(second) != 1 || second[0].Uri != "spotify:track:4uLU6hMCjMI75M1A2tKUQC" {
		t.Fatalf("next page = %v, %v", second, err)
	}
}

func TestDJResolverFallbackHonorsCallerCancellation(t *testing.T) {
	var calls int
	fallbackStarted := make(chan struct{})
	client := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/lexicon-session-provider/context-resolve/v2/session" {
			return nil, fmt.Errorf("unexpected request path %s", r.URL.Path)
		}
		calls++
		if calls == 1 {
			return jsonResponse(r, `{"uri":"`+DJContextURI+`","metadata":{"playlist_number_of_tracks":"2"},"pages":[{"tracks":[{"uri":"spotify:track:2FY7b99s15jUprqC0M5NCT"}]}]}`)
		}
		close(fallbackStarted)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	sp := playbackAPIClient(t, client)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := resolveDJContext(ctx, sp)
		result <- err
	}()
	select {
	case <-fallbackStarted:
	case <-time.After(time.Second):
		t.Fatal("fallback request did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("resolveDJContext() error = %v, want caller cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("fallback request outlived caller cancellation")
	}
}

type djClipSource struct {
	valueSource
	read bool
}

func (s *djClipSource) Read(p []float32) (int, error) {
	if s.read {
		return 0, io.EOF
	}
	s.read = true
	n, _ := s.valueSource.Read(p)
	return n, io.EOF
}

func TestDJAudioPlaysIntroMusicAndOutro(t *testing.T) {
	source := librespotPlayer.NewNarratedSource(&djNarrationLogger{},
		&djClipSource{valueSource: valueSource{value: 0.125}},
		&djClipSource{valueSource: valueSource{value: 0.25}},
		&djClipSource{valueSource: valueSource{value: 0.5}})
	s := newSpotifyStreamer(&librespotPlayer.Stream{Source: source}, nil)
	defer s.Close()
	samples := make([][2]float64, 1)
	for _, want := range []float64{0.125, 0.25, 0.5} {
		n, ok := s.Stream(samples)
		if !ok || n != 1 || samples[0] != [2]float64{want, want} {
			t.Fatalf("n=%d ok=%v samples=%v want=%v", n, ok, samples, want)
		}
	}
	if n, ok := s.Stream(samples); n != 0 || ok {
		t.Fatal("narrated stream did not end")
	}
}

func TestDJCloseReleasesNarrationWithoutClosingMusic(t *testing.T) {
	music, intro, outro := &closeErrorSource{}, &closeErrorSource{}, &closeErrorSource{}
	s := newSpotifyStreamer(&librespotPlayer.Stream{Source: music}, nil)
	s.closeNarration = func() { closeDJSource(intro); closeDJSource(outro) }
	s.Close()
	s.Close()
	if music.closeCalls != 0 || intro.closeCalls != 1 || outro.closeCalls != 1 {
		t.Fatalf("close calls music=%d intro=%d outro=%d", music.closeCalls, intro.closeCalls, outro.closeCalls)
	}
}

func TestDJNarrationDecodeFailureIsVisible(t *testing.T) {
	applog.Drain()
	source := librespotPlayer.NewNarratedSource(&djNarrationLogger{}, &droppingSource{}, &djClipSource{valueSource: valueSource{value: 0.25}}, nil)
	if _, err := source.Read(make([]float32, 2)); err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	found := false
	for _, entry := range applog.Drain() {
		if strings.Contains(entry.Text, "narration playback failed") {
			found = true
		}
	}
	if !found {
		t.Fatal("narration failure was silent")
	}
}
