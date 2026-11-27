package player

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// makeMP3Frames builds count consecutive synthetic Layer III frames with header h.
func makeMP3Frames(h [4]byte, count int) []byte {
	fsize := mp3FrameSize(h[:])
	if fsize < 4 {
		panic("invalid test MP3 frame header")
	}
	out := make([]byte, 0, fsize*count)
	frame := make([]byte, fsize)
	copy(frame, h[:])
	for range count {
		out = append(out, frame...)
	}
	return out
}

func TestMP3FrameSize(t *testing.T) {
	tests := []struct {
		name string
		hdr  [4]byte
		want int
	}{
		// MPEG-1 Layer III: 128 kbps @ 44100 Hz, padding 0 -> floor(144000*128/44100) = 417
		{name: "mpeg1 44100 128k no pad", hdr: [4]byte{0xff, 0xfb, 0x90, 0x00}, want: 417},
		// MPEG-1 Layer III: 128 kbps @ 44100 Hz, padding 1 -> 418
		{name: "mpeg1 44100 128k pad", hdr: [4]byte{0xff, 0xfb, 0x92, 0x00}, want: 418},
		// MPEG-2 Layer III: 24 kbps @ 22050 Hz, padding 0 -> floor(72000*24/22050) = 78
		{name: "mpeg2 22050 24k no pad", hdr: [4]byte{0xff, 0xf3, 0x30, 0xc0}, want: 78},
		// MPEG-2 Layer III: 24 kbps @ 22050 Hz, padding 1 -> 79 (exact case go-mp3 breaks)
		{name: "mpeg2 22050 24k pad", hdr: [4]byte{0xff, 0xf3, 0x32, 0xc0}, want: 79},
		// MPEG-2.5 Layer III: 16 kbps @ 11025 Hz, padding 0 -> floor(72000*16/11025) = 104
		{name: "mpeg25 11025 16k no pad", hdr: [4]byte{0xff, 0xe3, 0x20, 0xc0}, want: 104},
		// Invalid reserved version
		{name: "reserved version", hdr: [4]byte{0xff, 0xeb, 0x90, 0x00}, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mp3FrameSize(tt.hdr[:]); got != tt.want {
				t.Errorf("mp3FrameSize(%x) = %d, want %d", tt.hdr, got, tt.want)
			}
		})
	}
}

func TestParseMP3ProbeLowRate(t *testing.T) {
	mpeg1Hdr := [4]byte{0xff, 0xfb, 0x90, 0x00}  // MPEG-1 44100 Hz 128 kbps
	mpeg2Hdr := [4]byte{0xff, 0xf3, 0x30, 0xc0}  // MPEG-2 22050 Hz 24 kbps mono
	mpeg25Hdr := [4]byte{0xff, 0xe3, 0x20, 0xc0} // MPEG-2.5 11025 Hz 16 kbps mono

	id3 := append([]byte{'I', 'D', '3', 4, 0, 0, 0, 0, 0x00, 0x20}, make([]byte, 32)...)

	tests := []struct {
		name        string
		data        []byte
		wantLowRate bool
	}{
		{
			name:        "mpeg1 44100 Hz is native",
			data:        makeMP3Frames(mpeg1Hdr, 3),
			wantLowRate: false,
		},
		{
			name:        "mpeg2 22050 Hz is low-rate",
			data:        makeMP3Frames(mpeg2Hdr, 3),
			wantLowRate: true,
		},
		{
			name:        "mpeg2.5 11025 Hz is low-rate",
			data:        makeMP3Frames(mpeg25Hdr, 3),
			wantLowRate: true,
		},
		{
			name:        "id3v2 followed by mpeg2 is low-rate",
			data:        append(append([]byte(nil), id3...), makeMP3Frames(mpeg2Hdr, 3)...),
			wantLowRate: true,
		},
		{
			name:        "false mpeg2 sync in junk before mpeg1 stays native",
			data:        append([]byte{0xff, 0xf3, 0x30, 0xc0, 0x11, 0x22, 0x33}, makeMP3Frames(mpeg1Hdr, 3)...),
			wantLowRate: false,
		},
		{
			name:        "false mpeg1 sync in junk before mpeg2 detects low-rate",
			data:        append([]byte{0xff, 0xfb, 0x90, 0x00, 0x11, 0x22, 0x33}, makeMP3Frames(mpeg2Hdr, 3)...),
			wantLowRate: true,
		},
		{
			name:        "single mpeg2 frame at EOF is low-rate",
			data:        makeMP3Frames(mpeg2Hdr, 1),
			wantLowRate: true,
		},
		{
			name:        "garbage without sync is not low-rate",
			data:        bytes.Repeat([]byte("not mp3 "), 32),
			wantLowRate: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseMP3Probe(bytes.NewReader(tt.data)); got.lowRate != tt.wantLowRate {
				t.Errorf("parseMP3Probe().lowRate = %v, want %v", got.lowRate, tt.wantLowRate)
			}
		})
	}
}

func TestBuildPipelineRoutesLowRateMP3(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script ffmpeg fixtures require Unix")
	}

	mpeg2Data := makeMP3Frames([4]byte{0xff, 0xf3, 0x30, 0xc0}, 8)

	t.Run("local MPEG-2 MP3 routes to localFFmpegPipeline when ffmpeg is available", func(t *testing.T) {
		installPipelineRouteFixtures(t)
		p := newTestPlayer()

		path := filepath.Join(t.TempDir(), "radio22k.mp3")
		if err := os.WriteFile(path, mpeg2Data, 0o644); err != nil {
			t.Fatal(err)
		}

		tp, err := p.buildPipeline(path, 0, nil)
		if err != nil {
			t.Fatalf("buildPipeline(%q) error = %v", path, err)
		}
		defer tp.close()

		if _, ok := tp.decoder.(*localFFmpegStreamer); !ok {
			t.Fatalf("decoder = %T, want *localFFmpegStreamer", tp.decoder)
		}
		if !tp.seekable {
			t.Fatalf("seekable = false, want true for local ffmpeg MP3")
		}
	})

	t.Run("local MPEG-1 MP3 stays on native go-mp3 decoder", func(t *testing.T) {
		installPipelineRouteFixtures(t)
		p := newTestPlayer()

		path := filepath.Join("testdata", "cliamp_whips_terminal_ass.mp3")
		tp, err := p.buildPipeline(path, 0, nil)
		if err != nil {
			t.Fatalf("buildPipeline(%q) error = %v", path, err)
		}
		defer tp.close()

		if _, ok := tp.decoder.(*mp3GaplessDecoder); !ok {
			t.Fatalf("decoder = %T, want *mp3GaplessDecoder", tp.decoder)
		}
	})

	t.Run("local MPEG-2 MP3 falls back to native go-mp3 when ffmpeg is unavailable", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		p := newTestPlayer()

		// Build padded MPEG-2 frames large enough for go-mp3's initial decode read.
		path := filepath.Join(t.TempDir(), "radio22k.mp3")
		if err := os.WriteFile(path, makeMP3Frames([4]byte{0xff, 0xf3, 0x30, 0xc0}, 32), 0o644); err != nil {
			t.Fatal(err)
		}

		tp, err := p.buildPipeline(path, 0, nil)
		if err != nil {
			t.Fatalf("buildPipeline(%q) error = %v", path, err)
		}
		defer tp.close()

		if _, isFFmpeg := tp.decoder.(*localFFmpegStreamer); isFFmpeg {
			t.Fatalf("decoder = %T, want native go-mp3 when ffmpeg is absent", tp.decoder)
		}
		if tp.format.SampleRate != 22050 {
			t.Fatalf("sample rate = %d, want 22050 from native go-mp3", tp.format.SampleRate)
		}
	})

	t.Run("live HTTP MPEG-2 MP3 routes through ffmpeg pipe and preserves ICY metadata", func(t *testing.T) {
		dir := t.TempDir()
		// Fake ffmpeg drains stdin in background so icyReader receives the ICY block,
		// and outputs PCM bytes so waitForInitialAudio succeeds.
		writeExecutable(t, filepath.Join(dir, "ffmpeg"), `#!/bin/sh
(cat >/dev/null) &
printf '\000\100\000\300'
exec sleep 30
`)
		t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		p := newTestPlayer()

		const metaint = 128
		titleBlock := "StreamTitle='U7 Radio - Old Time Show';"
		padLen := ((len(titleBlock) + 15) / 16) * 16
		icyMeta := make([]byte, 1+padLen)
		icyMeta[0] = byte(padLen / 16)
		copy(icyMeta[1:], titleBlock)

		// Ensure at least metaint bytes of MPEG-2 frames before the first ICY block.
		audioChunk := makeMP3Frames([4]byte{0xff, 0xf3, 0x30, 0xc0}, 4)
		if len(audioChunk) < metaint {
			t.Fatalf("audioChunk too short: %d < %d", len(audioChunk), metaint)
		}

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "audio/mpeg")
			w.Header().Set("Icy-Metaint", strconv.Itoa(metaint))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(audioChunk[:metaint])
			_, _ = w.Write(icyMeta)
			_, _ = w.Write(audioChunk[metaint:])
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}))
		defer srv.Close()

		tp, err := p.buildPipeline(srv.URL+"/live.mp3", 0, nil)
		if err != nil {
			t.Fatalf("buildPipeline(live mpeg2) error = %v", err)
		}
		defer tp.close()

		if _, ok := tp.decoder.(*ffmpegPipeStreamer); !ok {
			t.Fatalf("decoder = %T, want *ffmpegPipeStreamer", tp.decoder)
		}
		if tp.meta == nil {
			t.Fatal("tp.meta = nil, want non-nil pipelineMeta")
		}

		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if got := tp.meta.latest.Load(); got == "U7 Radio - Old Time Show" {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("ICY StreamTitle = %q, want %q", tp.meta.latest.Load(), "U7 Radio - Old Time Show")
	})
}
