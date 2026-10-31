package player

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gopxl/beep/v2"
)

func TestParseYTDLMedia(t *testing.T) {
	headers := `{"User-Agent": "UA/1", "Accept": "*/*", "Accept-Language": "en-us,en;q=0.5", "X-Bad": "a\r\nInjected: 1"}`
	tests := []struct {
		name        string
		out         string
		wantErr     error
		wantURL     string
		wantUA      string
		wantHeaders string
		wantExpires time.Time
	}{
		{
			name:        "direct url with expire",
			out:         "https://media.example/videoplayback?expire=1800000000&itag=251\n" + headers + "\n",
			wantURL:     "https://media.example/videoplayback?expire=1800000000&itag=251",
			wantUA:      "UA/1",
			wantHeaders: "Accept: */*\r\nAccept-Language: en-us,en;q=0.5\r\n",
			wantExpires: time.Unix(1800000000, 0),
		},
		{
			name:    "direct url without expire",
			out:     "http://media.example/a.webm\n{}",
			wantURL: "http://media.example/a.webm",
		},
		{name: "separate video and audio", out: "https://v.example/v\nhttps://v.example/a\n{}", wantErr: errNoDirectYTDLMedia},
		{name: "hls", out: "https://v.example/index.m3u8\n{}", wantErr: errNoDirectYTDLMedia},
		{name: "not http", out: "file:///etc/passwd\n{}", wantErr: errNoDirectYTDLMedia},
		{name: "empty", out: "", wantErr: errNoDirectYTDLMedia},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := parseYTDLMedia(tt.out)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("parseYTDLMedia() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseYTDLMedia() error = %v", err)
			}
			if m.url != tt.wantURL || m.userAgent != tt.wantUA || m.headers != tt.wantHeaders || !m.expires.Equal(tt.wantExpires) {
				t.Fatalf("parseYTDLMedia() = %+v", *m)
			}
		})
	}
}

func TestParseYTDLMediaRejectsBadHeaders(t *testing.T) {
	if _, err := parseYTDLMedia("https://media.example/a\nnot json"); err == nil || errors.Is(err, errNoDirectYTDLMedia) {
		t.Fatalf("parseYTDLMedia() error = %v, want a headers error", err)
	}
}

func TestYTDLMediaUsable(t *testing.T) {
	now := time.Unix(1000, 0)
	tests := []struct {
		name  string
		media *ytdlMedia
		want  bool
	}{
		{name: "nil", media: nil, want: false},
		{name: "no expiry", media: &ytdlMedia{}, want: true},
		{name: "expires later", media: &ytdlMedia{expires: now.Add(time.Hour)}, want: true},
		{name: "inside the margin", media: &ytdlMedia{expires: now.Add(ytdlMediaExpiryMargin / 2)}, want: false},
		{name: "expired", media: &ytdlMedia{expires: now.Add(-time.Second)}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.media.usable(now); got != tt.want {
				t.Fatalf("usable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestYTDLMediaInputArgs(t *testing.T) {
	m := &ytdlMedia{url: "https://media.example/a", userAgent: "UA/1", headers: "Accept: */*\r\n"}

	args := m.inputArgs(90)
	ss, in := slices.Index(args, "-ss"), slices.Index(args, "-i")
	if ss < 0 || args[ss+1] != "90" || in != len(args)-2 || ss > in || args[in+1] != m.url {
		t.Fatalf("inputArgs(90) = %q, want -ss 90 before -i %s", args, m.url)
	}
	if i := slices.Index(args, "-user_agent"); i < 0 || args[i+1] != "UA/1" {
		t.Fatalf("inputArgs(90) = %q, want the user agent", args)
	}
	if slices.Contains(m.inputArgs(0), "-ss") {
		t.Fatal("inputArgs(0) seeks, want no -ss")
	}
}

// installYTDLSeekFixtures puts fake yt-dlp and ffmpeg on PATH. yt-dlp answers
// a --print resolve with resolveOut and a pipe download with PCM bytes. ffmpeg
// copies stdin for the pipe and, for a direct URL, writes PCM unless
// directFails is set. Each run appends its arguments to a log in dir.
func installYTDLSeekFixtures(t *testing.T, resolveOut string, directFails bool) (resolveLog, ffmpegLog string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX process fixtures")
	}
	dir := t.TempDir()
	resolveLog = filepath.Join(dir, "resolve.log")
	ffmpegLog = filepath.Join(dir, "ffmpeg.log")
	writeExecutable(t, filepath.Join(dir, "resolve-out"), resolveOut)
	writeExecutable(t, filepath.Join(dir, "yt-dlp"), `#!/bin/sh
case " $* " in
*" --print "*)
	printf 'resolve\n' >> "$RESOLVE_LOG"
	cat "$RESOLVE_OUT"
	exit 0
	;;
esac
printf '\001\002\003\004'
`)
	fail := "0"
	if directFails {
		fail = "1"
	}
	writeExecutable(t, filepath.Join(dir, "ffmpeg"), `#!/bin/sh
printf '%s\n' "$*" >> "$FFMPEG_LOG"
case " $* " in
*" -i pipe:0 "*) exec cat ;;
esac
if [ "$DIRECT_FAILS" = 1 ]; then
	printf 'HTTP error 403 Forbidden\n' >&2
	exit 1
fi
printf '\001\002\003\004'
`)
	t.Setenv("RESOLVE_LOG", resolveLog)
	t.Setenv("RESOLVE_OUT", filepath.Join(dir, "resolve-out"))
	t.Setenv("FFMPEG_LOG", ffmpegLog)
	t.Setenv("DIRECT_FAILS", fail)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return resolveLog, ffmpegLog
}

func TestDirectSeekPage(t *testing.T) {
	tests := []struct {
		page string
		want bool
	}{
		{page: "https://www.youtube.com/watch?v=a", want: true},
		{page: "https://music.youtube.com/watch?v=a", want: true},
		{page: "https://youtu.be/a", want: true},
		{page: "https://www.mixcloud.com/creator/show/", want: false},
		{page: "https://soundcloud.com/artist/track", want: false},
		{page: "ytsearch1:query", want: false},
	}
	for _, tt := range tests {
		if got := directSeekPage(tt.page); got != tt.want {
			t.Errorf("directSeekPage(%q) = %v, want %v", tt.page, got, tt.want)
		}
	}
}

// A kept URL that stopped working, for example after it expired, is resolved
// again rather than abandoned.
func TestBuildYTDLSeekPipelineResolvesAgainWhenAKeptURLFails(t *testing.T) {
	resolveLog, _ := installYTDLSeekFixtures(t, "https://media.example/a\n{}\n", true)
	p := &Player{sr: beep.SampleRate(44100), bitDepth: 16}
	cur := &trackPipeline{
		path:      "https://music.youtube.com/watch?v=seek",
		ytdlSeek:  true,
		ytdlMedia: &ytdlMedia{url: "https://media.example/old"},
	}

	tp, err := p.buildYTDLSeekPipeline(cur, 30)
	if err != nil {
		t.Fatalf("seek error = %v", err)
	}
	defer tp.close()
	if tp.ytdlNoDirect {
		t.Fatal("a failed kept URL stopped direct reads, want a resolve on the next seek")
	}
	if got := fixtureLineCount(t, resolveLog); got != 0 {
		t.Fatalf("yt-dlp resolves = %d, want 0 while the kept URL is still in date", got)
	}
}

func TestBuildYTDLSeekPipelineKeepsThePipeForOtherSites(t *testing.T) {
	resolveLog, ffmpegLog := installYTDLSeekFixtures(t, "https://media.example/a\n{}\n", false)
	p := &Player{sr: beep.SampleRate(44100), bitDepth: 16}
	cur := &trackPipeline{path: "https://www.mixcloud.com/creator/show/", ytdlSeek: true}

	tp, err := p.buildYTDLSeekPipeline(cur, 30)
	if err != nil {
		t.Fatalf("seek error = %v", err)
	}
	defer tp.close()
	if got := fixtureLineCount(t, resolveLog); got != 0 {
		t.Fatalf("yt-dlp resolves = %d, want 0 for a Mixcloud page", got)
	}
	if log, _ := os.ReadFile(ffmpegLog); !strings.Contains(string(log), "-ss 30 -i pipe:0") {
		t.Fatalf("ffmpeg runs:\n%s\nwant the pipe at 30s", log)
	}
}

func TestBuildYTDLSeekPipelineReusesTheResolvedURL(t *testing.T) {
	resolveLog, ffmpegLog := installYTDLSeekFixtures(t, "https://media.example/a?expire=4000000000\n{\"User-Agent\": \"UA/1\"}\n", false)
	p := &Player{sr: beep.SampleRate(44100), bitDepth: 16}
	cur := &trackPipeline{path: "https://www.youtube.com/watch?v=seek", ytdlSeek: true}

	first, err := p.buildYTDLSeekPipeline(cur, 30)
	if err != nil {
		t.Fatalf("first seek error = %v", err)
	}
	defer first.close()
	if first.ytdlMedia == nil || first.streamOffset != 30*time.Second {
		t.Fatalf("first seek pipeline = %+v, want the direct URL at 30s", first)
	}

	second, err := p.buildYTDLSeekPipeline(first, 120)
	if err != nil {
		t.Fatalf("second seek error = %v", err)
	}
	defer second.close()
	if got := fixtureLineCount(t, resolveLog); got != 1 {
		t.Fatalf("yt-dlp resolves = %d, want 1 for two seeks", got)
	}
	log, _ := os.ReadFile(ffmpegLog)
	if !strings.Contains(string(log), "-ss 120 -i https://media.example/a?expire=4000000000") {
		t.Fatalf("ffmpeg runs:\n%s\nwant the second seek to open the URL at 120s", log)
	}
}

func TestBuildYTDLSeekPipelineFallsBackToThePipe(t *testing.T) {
	tests := []struct {
		name         string
		resolveOut   string
		directFails  bool
		wantNoDirect bool
	}{
		{name: "no direct format", resolveOut: "https://v.example/index.m3u8\n{}\n", wantNoDirect: true},
		{name: "direct read fails", resolveOut: "https://media.example/a\n{}\n", directFails: true, wantNoDirect: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolveLog, ffmpegLog := installYTDLSeekFixtures(t, tt.resolveOut, tt.directFails)
			p := &Player{sr: beep.SampleRate(44100), bitDepth: 16}
			cur := &trackPipeline{path: "https://www.youtube.com/watch?v=seek", ytdlSeek: true}

			tp, err := p.buildYTDLSeekPipeline(cur, 30)
			if err != nil {
				t.Fatalf("seek error = %v", err)
			}
			defer tp.close()
			if tp.ytdlMedia != nil || tp.ytdlNoDirect != tt.wantNoDirect {
				t.Fatalf("pipeline media = %v, noDirect = %v, want the yt-dlp pipe with noDirect %v", tp.ytdlMedia, tp.ytdlNoDirect, tt.wantNoDirect)
			}
			if log, _ := os.ReadFile(ffmpegLog); !strings.Contains(string(log), "-ss 30 -i pipe:0") {
				t.Fatalf("ffmpeg runs:\n%s\nwant the pipe at 30s", log)
			}

			// The track keeps the pipe: the next seek does not resolve again.
			next, err := p.buildYTDLSeekPipeline(tp, 60)
			if err != nil {
				t.Fatalf("next seek error = %v", err)
			}
			defer next.close()
			if got := fixtureLineCount(t, resolveLog); got != 1 {
				t.Fatalf("yt-dlp resolves = %d, want 1", got)
			}
		})
	}
}
