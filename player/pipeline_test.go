package player

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/generators"
	"github.com/gopxl/beep/v2/wav"

	"github.com/bjarneo/cliamp/playlist"
)

// installPipelineRouteFixtures puts fake ffmpeg, ffprobe and ssh binaries
// first on PATH. The fake ffmpeg writes one PCM frame and then waits, so every
// ffmpeg route starts without a real decoder.
func installPipelineRouteFixtures(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "ffmpeg"), `#!/bin/sh
printf '\000\100\000\300'
exec sleep 30
`)
	writeExecutable(t, filepath.Join(dir, "ffprobe"), `#!/bin/sh
printf '1\n'
`)
	writeExecutable(t, filepath.Join(dir, "ssh"), `#!/bin/sh
exec sleep 30
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// testWAV returns a short silent WAV file that the native decoder accepts.
func testWAV(t *testing.T) []byte {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "tone-*.wav")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	format := beep.Format{SampleRate: 44100, NumChannels: 2, Precision: 2}
	if err := wav.Encode(f, generators.Silence(441), format); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// routeServer serves one response shape per path: with or without
// Content-Length, and with or without Icy headers.
func routeServer(t *testing.T, wavData []byte) *httptest.Server {
	t.Helper()
	garbage := bytes.Repeat([]byte("not audio "), 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		finite := func(body []byte) {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = w.Write(body)
		}
		chunked := func(body []byte) {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			_, _ = w.Write(body)
		}
		// live never ends the response, like an Icecast mount.
		live := func(body []byte) {
			chunked(body)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}
		if strings.HasPrefix(r.URL.Path, "/ffmpeg/") {
			chunked(garbage)
			return
		}
		switch r.URL.Path {
		case "/finite.wav":
			finite(wavData)
		case "/chunked.wav":
			chunked(wavData)
		case "/radio.aac":
			w.Header().Set("Icy-Name", "Test Radio")
			live(garbage)
		case "/radio-length.aac":
			w.Header().Set("Icy-Name", "Test Radio")
			finite(garbage)
		case "/garbage.ogg", "/garbage.mp3":
			chunked(garbage)
		case "/radio.ogg":
			w.Header().Set("Icy-Name", "Test Radio")
			live(garbage)
		case "/buffered", "/seg0", "/seg1":
			chunked(garbage)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestBuildPipelineRoutes pins the pipeline shape of each buildPipeline route.
func TestBuildPipelineRoutes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fixtures")
	}
	fixtures := installPipelineRouteFixtures(t)
	wavData := testWAV(t)
	srv := routeServer(t, wavData)

	localFiles := map[string][]byte{
		"track.m4a": []byte("container bytes"),
		"tone.wav":  wavData,
		"bad.wav":   []byte("not a native wav"),
	}
	for name, data := range localFiles {
		if err := os.WriteFile(filepath.Join(fixtures, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	garbageLen := int64(len(bytes.Repeat([]byte("not audio "), 64)))

	tests := []struct {
		name     string
		path     string
		register func(p *Player)
		wantErr  string

		wantDecoder  string
		wantPath     string
		seekable     bool
		live         bool
		prefetch     bool
		counted      bool
		wantLength   int64
		wantDuration time.Duration
	}{
		{
			name: "custom streamer factory",
			path: "fake:track:1",
			register: func(p *Player) {
				p.RegisterStreamerFactory("fake:", func(string) (beep.StreamSeekCloser, beep.Format, time.Duration, error) {
					return newPlaybackTestDecoder(), beep.Format{SampleRate: 44100, NumChannels: 2, Precision: 2}, time.Minute, nil
				})
			},
			wantDecoder:  "*player.playbackTestDecoder",
			seekable:     true,
			wantDuration: time.Minute,
		},
		{
			name: "source resolver segments",
			path: "segs://track/1",
			register: func(p *Player) {
				p.RegisterSourceResolver("segs://", func(string) (ResolvedSource, error) {
					return ResolvedSource{Segments: []string{srv.URL + "/seg0", srv.URL + "/seg1"}}, nil
				})
			},
			wantDecoder: "*player.navFFmpegStreamer",
			wantPath:    "segs://track/1",
			seekable:    true,
			counted:     true,
			wantLength:  -1,
		},
		{
			name: "source resolver url",
			path: "res://radio/1",
			register: func(p *Player) {
				p.RegisterSourceResolver("res://", func(string) (ResolvedSource, error) {
					return ResolvedSource{URL: srv.URL + "/radio.aac"}, nil
				})
			},
			wantDecoder: "*player.ffmpegPipeStreamer",
			wantPath:    srv.URL + "/radio.aac",
			live:        true,
			prefetch:    true,
			counted:     true,
			wantLength:  -1,
		},
		{
			name: "source resolver buffered url",
			path: "buf://track/1",
			register: func(p *Player) {
				p.RegisterSourceResolver("buf://", func(string) (ResolvedSource, error) {
					return ResolvedSource{URL: srv.URL + "/buffered", Buffered: true}, nil
				})
			},
			wantDecoder: "*player.navFFmpegStreamer",
			wantPath:    srv.URL + "/buffered",
			seekable:    true,
			counted:     true,
			wantLength:  -1,
		},
		{
			name: "buffered url matcher",
			path: srv.URL + "/buffered",
			register: func(p *Player) {
				p.RegisterBufferedURLMatcher(func(u string) bool { return strings.HasSuffix(u, "/buffered") })
			},
			wantDecoder: "*player.navFFmpegStreamer",
			wantPath:    srv.URL + "/buffered",
			seekable:    true,
			counted:     true,
			wantLength:  -1,
		},
		{
			name: "buffered url matcher names no provider in errors",
			path: srv.URL + "/missing",
			register: func(p *Player) {
				p.RegisterBufferedURLMatcher(func(u string) bool { return strings.HasSuffix(u, "/missing") })
			},
			wantErr: "buffer source: nav buffer: http status 404",
		},
		{
			name:        "hls playlist",
			path:        srv.URL + "/live.m3u8",
			wantDecoder: "*player.ffmpegPipeStreamer",
			wantPath:    srv.URL + "/live.m3u8",
			prefetch:    true,
		},
		{
			name:        "finite http with content length",
			path:        srv.URL + "/finite.wav",
			wantDecoder: "*player.navFFmpegStreamer",
			wantPath:    srv.URL + "/finite.wav",
			seekable:    true,
			counted:     true,
			wantLength:  int64(len(wavData)),
		},
		{
			name:        "icy response with content length stays live",
			path:        srv.URL + "/radio-length.aac",
			wantDecoder: "*player.ffmpegPipeStreamer",
			wantPath:    srv.URL + "/radio-length.aac",
			live:        true,
			prefetch:    true,
			counted:     true,
			wantLength:  garbageLen,
		},
		{
			name:        "icy radio in an ffmpeg format",
			path:        srv.URL + "/radio.aac",
			wantDecoder: "*player.ffmpegPipeStreamer",
			wantPath:    srv.URL + "/radio.aac",
			live:        true,
			prefetch:    true,
			counted:     true,
			wantLength:  -1,
		},
		{
			name:        "chunked ogg that is not vorbis",
			path:        srv.URL + "/garbage.ogg",
			wantDecoder: "*player.ffmpegPipeStreamer",
			wantPath:    srv.URL + "/garbage.ogg",
			prefetch:    true,
		},
		{
			name:        "icy ogg that is not vorbis",
			path:        srv.URL + "/radio.ogg",
			wantDecoder: "*player.ffmpegPipeStreamer",
			wantPath:    srv.URL + "/radio.ogg",
			live:        true,
			prefetch:    true,
		},
		{
			name:        "chunked native format",
			path:        srv.URL + "/chunked.wav",
			wantDecoder: "*wav.decoder",
			wantPath:    srv.URL + "/chunked.wav",
			prefetch:    true,
			counted:     true,
			wantLength:  -1,
		},
		{
			name:        "chunked native decode failure",
			path:        srv.URL + "/garbage.mp3",
			wantDecoder: "*player.ffmpegPipeStreamer",
			wantPath:    srv.URL + "/garbage.mp3",
			prefetch:    true,
		},
		{
			name:        "local ffmpeg format",
			path:        filepath.Join(fixtures, "track.m4a"),
			wantDecoder: "*player.localFFmpegStreamer",
			wantPath:    filepath.Join(fixtures, "track.m4a"),
			seekable:    true,
		},
		{
			name:        "local native format",
			path:        filepath.Join(fixtures, "tone.wav"),
			wantDecoder: "*wav.decoder",
			wantPath:    filepath.Join(fixtures, "tone.wav"),
			seekable:    true,
			wantLength:  -1,
		},
		{
			name:        "local native decode failure",
			path:        filepath.Join(fixtures, "bad.wav"),
			wantDecoder: "*player.localFFmpegStreamer",
			wantPath:    filepath.Join(fixtures, "bad.wav"),
			seekable:    true,
		},
		{
			name:    "ssh source in an ffmpeg format",
			path:    "ssh://host/music/track.m4a",
			wantErr: "SSH streaming does not support .m4a format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Player{sr: beep.SampleRate(44100), bitDepth: 16, resampleQuality: 1}
			if tt.register != nil {
				tt.register(p)
			}

			tp, err := p.buildPipeline(tt.path, 0)
			if tt.wantErr != "" {
				if tp != nil {
					tp.close()
				}
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("buildPipeline() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildPipeline() error = %v", err)
			}
			defer tp.close()

			got := fmt.Sprintf("decoder=%T path=%q seekable=%v live=%v prefetch=%v counted=%v length=%d duration=%v",
				tp.decoder, tp.path, tp.seekable, tp.live, tp.livePrefetch != nil, tp.bytesRead != nil, tp.contentLength, tp.knownDuration)
			want := fmt.Sprintf("decoder=%s path=%q seekable=%v live=%v prefetch=%v counted=%v length=%d duration=%v",
				tt.wantDecoder, tt.wantPath, tt.seekable, tt.live, tt.prefetch, tt.counted, tt.wantLength, tt.wantDuration)
			if got != want {
				t.Errorf("pipeline:\n got %s\nwant %s", got, want)
			}
			if tp.stream == nil {
				t.Error("pipeline has no stream")
			}
			if tp.format.SampleRate == 0 {
				t.Error("pipeline has no format")
			}
		})
	}
}

// TestBuildSourceRoutes checks that buildSource sends a page URL that the
// yt-dlp matcher claims to the yt-dlp chain, before any source resolver, and
// every other path to buildPipeline at its offset.
func TestBuildSourceRoutes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fixtures")
	}
	fixtures := installPipelineRouteFixtures(t)
	// The fake yt-dlp answers a duration probe with 90 s and otherwise writes
	// page bytes for the fake ffmpeg.
	writeExecutable(t, filepath.Join(fixtures, "yt-dlp"), `#!/bin/sh
for arg do
	if [ "$arg" = "--print" ]; then
		printf '90\n'
		exit 0
	fi
done
printf 'page bytes'
`)
	wavData := testWAV(t)
	tone := filepath.Join(fixtures, "tone.wav")
	if err := os.WriteFile(tone, wavData, 0o600); err != nil {
		t.Fatal(err)
	}
	srv := routeServer(t, wavData)
	page := srv.URL + "/page"
	claimPage := func(p *Player) { p.RegisterYTDLMatcher(func(u string) bool { return u == page }) }

	tests := []struct {
		name     string
		path     string
		known    time.Duration
		offset   time.Duration
		probe    bool
		register func(t *testing.T, p *Player)
		wantErr  string

		wantDecoder  string
		wantYTDL     bool
		wantPrefetch bool
		wantDuration time.Duration
		wantPosition time.Duration
	}{
		{
			name:         "yt-dlp page keeps its known duration",
			path:         page,
			known:        3 * time.Minute,
			probe:        true,
			register:     func(_ *testing.T, p *Player) { claimPage(p) },
			wantDecoder:  "*player.ytdlPipeStreamer",
			wantYTDL:     true,
			wantPrefetch: true,
			wantDuration: 3 * time.Minute,
		},
		{
			name:         "yt-dlp page probes a missing duration",
			path:         page,
			probe:        true,
			register:     func(_ *testing.T, p *Player) { claimPage(p) },
			wantDecoder:  "*player.ytdlPipeStreamer",
			wantYTDL:     true,
			wantPrefetch: true,
			wantDuration: 90 * time.Second,
		},
		{
			name:         "yt-dlp preload does not probe",
			path:         page,
			register:     func(_ *testing.T, p *Player) { claimPage(p) },
			wantDecoder:  "*player.ytdlPipeStreamer",
			wantYTDL:     true,
			wantPrefetch: true,
		},
		{
			name:  "yt-dlp page ignores the offset",
			path:  page,
			known: time.Minute,
			// The chain starts at 0. A resume seeks by restart later.
			offset:       30 * time.Second,
			register:     func(_ *testing.T, p *Player) { claimPage(p) },
			wantDecoder:  "*player.ytdlPipeStreamer",
			wantYTDL:     true,
			wantPrefetch: true,
			wantDuration: time.Minute,
		},
		{
			name: "yt-dlp matcher wins over an http source resolver",
			path: page,
			register: func(t *testing.T, p *Player) {
				p.RegisterSourceResolver("http://", func(uri string) (ResolvedSource, error) {
					t.Errorf("source resolver called for %s", uri)
					return ResolvedSource{URL: uri}, nil
				})
				claimPage(p)
			},
			wantDecoder:  "*player.ytdlPipeStreamer",
			wantYTDL:     true,
			wantPrefetch: true,
		},
		{
			name:    "page that no matcher claims opens over http",
			path:    page,
			wantErr: "play at 0s: open source: http status 404",
		},
		{
			name:         "local file starts at the offset",
			path:         tone,
			known:        time.Minute,
			offset:       5 * time.Millisecond,
			register:     func(_ *testing.T, p *Player) { claimPage(p) },
			wantDecoder:  "*wav.decoder",
			wantDuration: time.Minute,
			wantPosition: beep.SampleRate(44100).D(220),
		},
		{
			name:    "pipeline error names the offset",
			path:    filepath.Join(fixtures, "missing.wav"),
			offset:  2 * time.Second,
			wantErr: "play at 2s: open source:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Player{sr: beep.SampleRate(44100), bitDepth: 16, resampleQuality: 1}
			if tt.register != nil {
				tt.register(t, p)
			}

			tp, err := p.buildSource(tt.path, tt.known, tt.offset, tt.probe)
			if tt.wantErr != "" {
				if tp != nil {
					tp.close()
				}
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("buildSource() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildSource() error = %v", err)
			}
			defer tp.close()

			position, _ := tp.positionAndDuration()
			got := fmt.Sprintf("decoder=%T ytdl=%v prefetch=%v duration=%v position=%v",
				tp.decoder, tp.ytdlSeek, tp.livePrefetch != nil, tp.knownDuration, position)
			want := fmt.Sprintf("decoder=%s ytdl=%v prefetch=%v duration=%v position=%v",
				tt.wantDecoder, tt.wantYTDL, tt.wantPrefetch, tt.wantDuration, tt.wantPosition)
			if got != want {
				t.Errorf("pipeline:\n got %s\nwant %s", got, want)
			}
		})
	}
}

// A metadata duration marks an ICY response with a length as a finite file,
// so its clean EOF ends the track and does not read as a dropped connection.
// The prefetch reads the decoder at once, so run this with -race: the decoder
// must get the flag before that read.
func TestBuildSourceKnownDurationEndsICYFileCleanly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fixtures")
	}
	dir := t.TempDir()
	// ffmpeg writes one PCM frame and exits, so the decoder reaches EOF.
	writeExecutable(t, filepath.Join(dir, "ffmpeg"), "#!/bin/sh\nprintf '\\000\\100\\000\\300'\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	srv := routeServer(t, nil)

	tests := []struct {
		name    string
		known   time.Duration
		wantErr error
	}{
		{name: "no duration is live radio", wantErr: io.ErrUnexpectedEOF},
		{name: "known duration is a file", known: time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Player{sr: beep.SampleRate(44100), bitDepth: 16, resampleQuality: 1}
			tp, err := p.buildSource(srv.URL+"/radio-length.aac", tt.known, 0, false)
			if err != nil {
				t.Fatalf("buildSource() error = %v", err)
			}
			defer tp.close()
			if tp.livePrefetch == nil {
				t.Fatal("ICY response was not prefetched")
			}

			// Wait until the prefetch has read the decoder to its EOF, before
			// any read here can order the two goroutines.
			select {
			case <-tp.livePrefetch.fillDone:
			case <-time.After(2 * time.Second):
				t.Fatal("prefetched source did not end")
			}
			buf := make([][2]float64, 512)
			for i := 0; ; i++ {
				if _, ok := tp.stream.Stream(buf); !ok {
					break
				}
				if i == 2 {
					t.Fatal("prefetched source still streams after its EOF")
				}
			}
			if err := tp.livePrefetch.Err(); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Err() = %v at EOF, want %v", err, tt.wantErr)
			}
		})
	}
}

// TestBuildPipelineSendsFFmpegFormatsPastNativeDecoders checks that every
// extension that needs ffmpeg takes an ffmpeg route before the native
// decoders, for local, HTTP and SSH sources.
func TestBuildPipelineSendsFFmpegFormatsPastNativeDecoders(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fixtures")
	}
	fixtures := installPipelineRouteFixtures(t)
	srv := routeServer(t, nil)

	for _, ext := range playlist.AudioExtensions() {
		if !needsFFmpeg(ext) {
			continue
		}
		local := filepath.Join(fixtures, "track"+ext)
		if err := os.WriteFile(local, []byte("container bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
		tests := []struct {
			name        string
			path        string
			wantDecoder string
			wantErr     string
		}{
			{name: "local", path: local, wantDecoder: "*player.localFFmpegStreamer"},
			{name: "http", path: srv.URL + "/ffmpeg/track" + ext, wantDecoder: "*player.ffmpegPipeStreamer"},
			{name: "ssh", path: "ssh://host/music/track" + ext, wantErr: "SSH streaming does not support " + ext},
		}
		for _, tt := range tests {
			t.Run(ext+"/"+tt.name, func(t *testing.T) {
				p := &Player{sr: beep.SampleRate(44100), bitDepth: 16, resampleQuality: 1}
				tp, err := p.buildPipeline(tt.path, 0)
				if tt.wantErr != "" {
					if tp != nil {
						tp.close()
					}
					if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
						t.Fatalf("buildPipeline() error = %v, want containing %q", err, tt.wantErr)
					}
					return
				}
				if err != nil {
					t.Fatalf("buildPipeline() error = %v", err)
				}
				defer tp.close()
				if got := fmt.Sprintf("%T", tp.decoder); got != tt.wantDecoder {
					t.Fatalf("decoder = %s, want %s", got, tt.wantDecoder)
				}
			})
		}
	}
}
