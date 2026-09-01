package yandex

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

func writeResult(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"result": v})
}

func testTrack(id string, albumID int) track {
	t := track{ID: flexString(id), Title: "Track " + id, DurationMs: 60000}
	if albumID > 0 {
		t.Albums = []album{{ID: uint64(albumID), Title: "Album " + strconv.Itoa(albumID)}}
	}
	return t
}

// newTestProvider returns a provider backed by an httptest server implementing
// the endpoints the provider calls, plus the collected request log.
func newTestProvider(t *testing.T, waveBatches [][]track) (*Provider, *requestLog) {
	t.Helper()
	log := &requestLog{}

	mux := http.NewServeMux()
	mux.HandleFunc("/account/status", func(w http.ResponseWriter, r *http.Request) {
		log.add("/account/status", nil)
		writeResult(w, map[string]any{"account": map[string]any{"uid": 42}})
	})
	mux.HandleFunc("/rotor/session/new", func(w http.ResponseWriter, r *http.Request) {
		log.add("/rotor/session/new", r)
		seq := make([]map[string]any, 0, 2)
		for i, tr := range waveBatches[0] {
			if i >= 2 {
				break
			}
			seq = append(seq, map[string]any{"type": "track", "track": tr})
		}
		writeResult(w, map[string]any{
			"sequence":       seq,
			"batchId":        "batch-0",
			"radioSessionId": "session-1",
		})
	})
	mux.HandleFunc("/rotor/session/session-1/tracks", func(w http.ResponseWriter, r *http.Request) {
		log.add("/rotor/session/session-1/tracks", r)
		var body struct {
			Feedbacks []json.RawMessage `json:"feedbacks"`
			Queue     []string          `json:"queue"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		log.queues = append(log.queues, body.Queue)
		idx := len(log.queues)
		if idx >= len(waveBatches) {
			writeResult(w, map[string]any{"sequence": []any{}, "batchId": "batch-end"})
			return
		}
		seq := make([]map[string]any, 0, len(waveBatches[idx]))
		for _, tr := range waveBatches[idx] {
			seq = append(seq, map[string]any{"type": "track", "track": tr})
		}
		writeResult(w, map[string]any{
			"sequence": seq,
			"batchId":  fmt.Sprintf("batch-%d", idx),
		})
	})
	mux.HandleFunc("/rotor/session/session-1/feedback", func(w http.ResponseWriter, r *http.Request) {
		var body rotorFeedback
		json.NewDecoder(r.Body).Decode(&body)
		log.add("/rotor/session/session-1/feedback", &body)
		writeResult(w, "ok")
	})
	mux.HandleFunc("/play-audio", func(w http.ResponseWriter, r *http.Request) {
		log.add("/play-audio", r)
		writeResult(w, "ok")
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	p := New("test-token")
	p.api.apiBase = ts.URL
	return p, log
}

type requestLog struct {
	mu     sync.Mutex
	paths  []string
	bodies []any
	queues [][]string
}

func (l *requestLog) add(path string, body any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.paths = append(l.paths, path)
	if body != nil {
		l.bodies = append(l.bodies, body)
	}
}

func (l *requestLog) count(path string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, p := range l.paths {
		if p == path {
			n++
		}
	}
	return n
}

func TestLoadWaveWithContinuations(t *testing.T) {
	p, log := newTestProvider(t, [][]track{
		{{ID: "1", Albums: []album{{ID: 11}}}, {ID: "2", Albums: []album{{ID: 12}}}},
		{{ID: "3", Albums: []album{{ID: 13}}}},
		{{ID: "4", Albums: []album{{ID: 14}}}, {ID: "5", Albums: []album{{ID: 15}}}},
	})

	tracks, err := p.Tracks(wavePlaylistID)
	if err != nil {
		t.Fatalf("Tracks(wave) error = %v", err)
	}
	if len(tracks) != 5 {
		t.Fatalf("got %d wave tracks, want 5", len(tracks))
	}
	for i, tr := range tracks {
		want := TrackURIPrefix + strconv.Itoa(i+1)
		if tr.Path != want {
			t.Errorf("tracks[%d].Path = %q, want %q", i, tr.Path, want)
		}
	}
	// Continuation requests must carry the queue of previously served keys.
	if len(log.queues) != 2 {
		t.Fatalf("got %d continuation requests, want 2", len(log.queues))
	}
	wantFirst := []string{"1:11", "2:12"}
	for i, k := range wantFirst {
		if log.queues[0][i] != k {
			t.Errorf("queue[0][%d] = %q, want %q", i, log.queues[0][i], k)
		}
	}
	if len(log.queues[1]) != 3 || log.queues[1][2] != "3:13" {
		t.Errorf("queue[1] = %v, want cumulative keys", log.queues[1])
	}
	// Second load must reuse the cached session without new API calls.
	if _, err := p.Tracks(wavePlaylistID); err != nil {
		t.Fatalf("second Tracks(wave) error = %v", err)
	}
	if n := log.count("/rotor/session/new"); n != 1 {
		t.Errorf("session/new called %d times, want 1", n)
	}
}

func TestWavePlaybackFeedback(t *testing.T) {
	p, log := newTestProvider(t, [][]track{
		{{ID: "1", DurationMs: 180000, Albums: []album{{ID: 11}}}},
	})
	if _, err := p.Tracks(wavePlaylistID); err != nil {
		t.Fatalf("Tracks(wave) error = %v", err)
	}

	tr := playlist.Track{
		Path:         TrackURIPrefix + "1",
		DurationSecs: 180,
		ProviderMeta: map[string]string{provider.MetaYandexID: "1"},
	}
	if err := p.ReportNowPlaying(tr, 0, true); err != nil {
		t.Fatalf("ReportNowPlaying error = %v", err)
	}
	if err := p.ReportScrobble(tr, 180_000_000_000, 180_000_000_000, true); err != nil {
		t.Fatalf("ReportScrobble error = %v", err)
	}

	n := log.count("/rotor/session/session-1/feedback")
	if n != 2 {
		t.Fatalf("got %d feedback events, want 2", n)
	}
	var types []string
	for _, b := range log.bodies {
		if fb, ok := b.(*rotorFeedback); ok {
			types = append(types, fb.Event.Type)
			if fb.Event.TrackID != "1:11" {
				t.Errorf("feedback TrackID = %q, want %q", fb.Event.TrackID, "1:11")
			}
			if fb.BatchID == "" {
				t.Error("feedback BatchID is empty")
			}
		}
	}
	if len(types) != 2 || types[0] != rotorTrackStarted || types[1] != rotorTrackFinished {
		t.Errorf("feedback types = %v, want [trackStarted trackFinished]", types)
	}
}

func TestResolveStreamURLCached(t *testing.T) {
	// The default guard only allows HTTPS *.yandex.net; point it at the
	// local httptest server for the duration of the test.
	origGuard := fullDownloadInfoGuard
	fullDownloadInfoGuard = func(infoURL string) error { return nil }
	defer func() { fullDownloadInfoGuard = origGuard }()

	var dlCalls int
	var mu sync.Mutex
	var tsURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/tracks/77/download-info":
			mu.Lock()
			dlCalls++
			mu.Unlock()
			writeResult(w, []map[string]any{{
				"codec":           "mp3",
				"bitrateInKbps":   320,
				"downloadInfoUrl": tsURL + "/dl?sign=x&ts=y",
			}})
		case r.URL.Path == "/dl":
			// The download info endpoint returns a bare JSON object, not the
			// API result envelope.
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"host": r.Host, "path": "/get-mp3/x", "ts": "1", "s": "abc"})
		default:
			http.NotFound(w, r)
		}
	}))
	tsURL = ts.URL
	defer ts.Close()

	p := New("test-token")
	p.api.apiBase = ts.URL

	u1, err := p.resolveStreamURL("77", false)
	if err != nil {
		t.Fatalf("resolveStreamURL error = %v", err)
	}
	if u1 == "" {
		t.Fatal("resolveStreamURL returned empty URL")
	}
	// Second non-forced call must hit the cache.
	if _, err := p.resolveStreamURL("77", false); err != nil {
		t.Fatalf("cached resolveStreamURL error = %v", err)
	}
	mu.Lock()
	calls := dlCalls
	mu.Unlock()
	if calls != 1 {
		t.Errorf("download-info called %d times, want 1", calls)
	}
	// Forced call must bypass the cache.
	if _, err := p.resolveStreamURL("77", true); err != nil {
		t.Fatalf("forced resolveStreamURL error = %v", err)
	}
	mu.Lock()
	calls = dlCalls
	mu.Unlock()
	if calls != 2 {
		t.Errorf("download-info called %d times after force, want 2", calls)
	}
}

func TestBuildStreamURLEndToEnd(t *testing.T) {
	// The download-info URL points at the API server; the signed URL must
	// include the secret hash, ts, and path from the JSON download info.
	info := downloadInfo{Codec: "mp3", DownloadInfoURL: "https://api.example/dl?sign=x"}
	full := fullDownloadInfo{Host: "api.example", Path: "/get-mp3/abc", Ts: "42", S: "zz"}
	got := buildStreamURL(info, full)
	want := "https://api.example/get-mp3/" + md5Hex(trackURISecret+"get-mp3/abc"+"zz") + "/42/get-mp3/abc"
	if got != want {
		t.Errorf("buildStreamURL() = %q, want %q", got, want)
	}
}
