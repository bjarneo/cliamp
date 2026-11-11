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
		wantDur     time.Duration
	}{
		{
			name:        "direct url with expire",
			out:         "213.5\nhttps://media.example/videoplayback?expire=1800000000&itag=251\n" + headers + "\n",
			wantURL:     "https://media.example/videoplayback?expire=1800000000&itag=251",
			wantUA:      "UA/1",
			wantHeaders: "Accept: */*\r\nAccept-Language: en-us,en;q=0.5\r\n",
			wantExpires: time.Unix(1800000000, 0),
			wantDur:     213500 * time.Millisecond,
		},
		{
			name:    "direct url without expire",
			out:     "NA\nhttp://media.example/a.webm\n{}",
			wantURL: "http://media.example/a.webm",
		},
		{
			name:    "live hls url",
			out:     "NA\nhttps://manifest.example/hls_playlist/expire/1800000000/index.m3u8\n{}",
			wantURL: "https://manifest.example/hls_playlist/expire/1800000000/index.m3u8",
		},
		{name: "separate video and audio", out: "60\nhttps://v.example/v\nhttps://v.example/a\n{}", wantErr: errNoDirectYTDLMedia},
		{name: "not http", out: "60\nfile:///etc/passwd\n{}", wantErr: errNoDirectYTDLMedia},
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
			if m.url != tt.wantURL || m.userAgent != tt.wantUA || m.headers != tt.wantHeaders || !m.expires.Equal(tt.wantExpires) || m.duration != tt.wantDur {
				t.Fatalf("parseYTDLMedia() = %+v", *m)
			}
		})
	}
}

func TestParseYTDLMediaRejectsBadHeaders(t *testing.T) {
	if _, err := parseYTDLMedia("60\nhttps://media.example/a\nnot json"); err == nil || errors.Is(err, errNoDirectYTDLMedia) {
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
// a --print resolve with resolveOut, or fails with $RESOLVE_ERROR when a test
// sets it, and answers a pipe download with PCM bytes. ffmpeg copies stdin for
// the pipe and, for a direct URL, writes PCM unless directFails is set. Each
// run appends its arguments to a log in dir.
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
	if [ -n "$RESOLVE_ERROR" ]; then
		printf '%s\n' "$RESOLVE_ERROR" >&2
		exit 1
	fi
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
	resolveLog, _ := installYTDLSeekFixtures(t, "240\nhttps://media.example/a\n{}\n", true)
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
	resolveLog, ffmpegLog := installYTDLSeekFixtures(t, "240\nhttps://media.example/a\n{}\n", false)
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
	resolveLog, ffmpegLog := installYTDLSeekFixtures(t, "240\nhttps://media.example/a?expire=4000000000\n{\"User-Agent\": \"UA/1\"}\n", false)
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
		{name: "no direct format", resolveOut: "240\nhttps://v.example/v\nhttps://v.example/a\n{}\n", wantNoDirect: true},
		{name: "direct read fails", resolveOut: "240\nhttps://media.example/a\n{}\n", directFails: true, wantNoDirect: true},
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

// A YouTube track starts from its direct URL, takes the duration from the same
// yt-dlp run, and its first seek runs no yt-dlp at all.
func TestBuildYTDLSourceStartsYouTubeFromTheMediaURL(t *testing.T) {
	resolveLog, ffmpegLog := installYTDLSeekFixtures(t, "240\nhttps://media.example/a?expire=4000000000\n{}\n", false)
	p := &Player{sr: beep.SampleRate(44100), bitDepth: 16}

	tp, err := p.buildYTDLSource("https://music.youtube.com/watch?v=start", 0, true)
	if err != nil {
		t.Fatalf("buildYTDLSource() error = %v", err)
	}
	defer tp.close()
	if tp.ytdlMedia == nil || !tp.ytdlSeek || tp.knownDuration != 240*time.Second {
		t.Fatalf("pipeline media = %v, ytdlSeek = %v, duration = %v, want the direct URL, seek-by-restart and 240s", tp.ytdlMedia, tp.ytdlSeek, tp.knownDuration)
	}

	seek, err := p.buildYTDLSeekPipeline(tp, 90)
	if err != nil {
		t.Fatalf("seek error = %v", err)
	}
	defer seek.close()
	if got := fixtureLineCount(t, resolveLog); got != 1 {
		t.Fatalf("yt-dlp resolves = %d, want 1 for the start and a seek", got)
	}
	if log, _ := os.ReadFile(ffmpegLog); strings.Contains(string(log), "pipe:0") {
		t.Fatalf("ffmpeg runs:\n%s\nwant no yt-dlp pipe", log)
	}
}

func TestBuildYTDLSourceFallsBackToThePipe(t *testing.T) {
	tests := []struct {
		name        string
		page        string
		resolveOut  string
		directFails bool
		wantResolve int
	}{
		{name: "direct read fails", page: "https://www.youtube.com/watch?v=a", resolveOut: "240\nhttps://media.example/a\n{}\n", directFails: true, wantResolve: 1},
		{name: "no direct format", page: "https://www.youtube.com/watch?v=a", resolveOut: "240\nhttps://v.example/v\nhttps://v.example/a\n{}\n", wantResolve: 1},
		{name: "other site", page: "https://www.mixcloud.com/creator/show/", resolveOut: "240\nhttps://media.example/a\n{}\n", wantResolve: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolveLog, ffmpegLog := installYTDLSeekFixtures(t, tt.resolveOut, tt.directFails)
			p := &Player{sr: beep.SampleRate(44100), bitDepth: 16}

			tp, err := p.buildYTDLSource(tt.page, time.Minute, false)
			if err != nil {
				t.Fatalf("buildYTDLSource() error = %v", err)
			}
			defer tp.close()
			if tp.ytdlMedia != nil || tp.knownDuration != time.Minute {
				t.Fatalf("pipeline media = %v, duration = %v, want the pipe with the known 1m", tp.ytdlMedia, tp.knownDuration)
			}
			if log, _ := os.ReadFile(ffmpegLog); !strings.Contains(string(log), "-i pipe:0") {
				t.Fatalf("ffmpeg runs:\n%s\nwant the yt-dlp pipe", log)
			}
			if got := fixtureLineCount(t, resolveLog); got != tt.wantResolve {
				t.Fatalf("yt-dlp resolves = %d, want %d", got, tt.wantResolve)
			}
		})
	}
}

// A live stream offers only an HLS URL. ffmpeg opens it, so the start runs
// yt-dlp once: no pipe, and no probe for the duration a live stream lacks.
func TestBuildYTDLSourceOpensALiveStreamFromItsHLSURL(t *testing.T) {
	const hls = "https://manifest.example/hls_playlist/index.m3u8"
	resolveLog, ffmpegLog := installYTDLSeekFixtures(t, "NA\n"+hls+"\n{}\n", false)
	p := &Player{sr: beep.SampleRate(44100), bitDepth: 16}

	tp, err := p.buildYTDLSource("https://www.youtube.com/watch?v=live", 0, true)
	if err != nil {
		t.Fatalf("buildYTDLSource() error = %v", err)
	}
	defer tp.close()
	if tp.ytdlMedia == nil || tp.knownDuration != 0 {
		t.Fatalf("pipeline media = %v, duration = %v, want the HLS URL and no duration", tp.ytdlMedia, tp.knownDuration)
	}
	if got := fixtureLineCount(t, resolveLog); got != 1 {
		t.Fatalf("yt-dlp resolves = %d, want 1", got)
	}
	log, _ := os.ReadFile(ffmpegLog)
	if !strings.Contains(string(log), "-i "+hls) || strings.Contains(string(log), "pipe:0") {
		t.Fatalf("ffmpeg runs:\n%s\nwant the HLS URL and no yt-dlp pipe", log)
	}
}

// ytdlResolveEntries are the two paths that resolve a YouTube page.
var ytdlResolveEntries = []struct {
	name  string
	build func(p *Player) (*trackPipeline, error)
}{
	{name: "start", build: func(p *Player) (*trackPipeline, error) {
		return p.buildYTDLSource("https://www.youtube.com/watch?v=a", time.Minute, false)
	}},
	{name: "seek", build: func(p *Player) (*trackPipeline, error) {
		return p.buildYTDLSeekPipeline(&trackPipeline{path: "https://www.youtube.com/watch?v=a", ytdlSeek: true}, 30)
	}},
}

// A failed resolve already answers for the page: the pipe would run the same
// extraction, so it does not start.
func TestFailedResolveReportsYTDLPsAnswer(t *testing.T) {
	const reason = "ERROR: [youtube] a: This video is unavailable"
	for _, entry := range ytdlResolveEntries {
		t.Run(entry.name, func(t *testing.T) {
			resolveLog, ffmpegLog := installYTDLSeekFixtures(t, "", false)
			t.Setenv("RESOLVE_ERROR", reason)
			p := &Player{sr: beep.SampleRate(44100), bitDepth: 16}

			tp, err := entry.build(p)
			if err == nil {
				tp.close()
				t.Fatal("build succeeded, want yt-dlp's error")
			}
			if !strings.Contains(err.Error(), reason) {
				t.Fatalf("build error = %v, want %q", err, reason)
			}
			if got := fixtureLineCount(t, resolveLog); got != 1 {
				t.Fatalf("yt-dlp resolves = %d, want 1", got)
			}
			if got := fixtureLineCount(t, ffmpegLog); got != 0 {
				t.Fatal("ffmpeg ran after the failed resolve, want no pipe")
			}
		})
	}
}

// A 403 goes on to the pipe, which retries it. The pipeline stays free to
// resolve again on a later seek.
func TestResolve403GoesToThePipe(t *testing.T) {
	for _, entry := range ytdlResolveEntries {
		t.Run(entry.name, func(t *testing.T) {
			resolveLog, ffmpegLog := installYTDLSeekFixtures(t, "", false)
			t.Setenv("RESOLVE_ERROR", "ERROR: [youtube] a: HTTP Error 403: Forbidden")
			p := &Player{sr: beep.SampleRate(44100), bitDepth: 16}

			tp, err := entry.build(p)
			if err != nil {
				t.Fatalf("build error = %v, want the pipe", err)
			}
			defer tp.close()
			if tp.ytdlMedia != nil || tp.ytdlNoDirect {
				t.Fatalf("pipeline media = %v, noDirect = %v, want a pipe that may resolve again", tp.ytdlMedia, tp.ytdlNoDirect)
			}
			if got := fixtureLineCount(t, resolveLog); got != 1 {
				t.Fatalf("yt-dlp resolves = %d, want 1", got)
			}
			if log, _ := os.ReadFile(ffmpegLog); !strings.Contains(string(log), "-i pipe:0") {
				t.Fatalf("ffmpeg runs:\n%s\nwant the yt-dlp pipe", log)
			}
		})
	}
}

// A yt-dlp that cannot run is not an answer about the page. The pipe reports it
// with the install hint.
func TestBuildYTDLSourceReportsAMissingYTDLP(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	p := &Player{sr: beep.SampleRate(44100), bitDepth: 16}

	tp, err := p.buildYTDLSource("https://www.youtube.com/watch?v=a", 0, false)
	if err == nil {
		tp.close()
		t.Fatal("buildYTDLSource() succeeded without yt-dlp")
	}
	if !strings.Contains(err.Error(), "yt-dlp is required") {
		t.Fatalf("buildYTDLSource() error = %v, want the install hint", err)
	}
}

// failedReadDecoder is a decoder whose read has failed.
type failedReadDecoder struct {
	*playbackTestDecoder
	err error
}

func (d *failedReadDecoder) Err() error { return d.err }

// A direct read that fails mid-track makes the reconnect start the page from
// the pipe, so the failure does not repeat. A clean stop or a failed pipe
// leaves the direct reads on.
func TestStopAfterAFailedDirectReadStartsThePageFromThePipe(t *testing.T) {
	const page = "https://music.youtube.com/watch?v=fail"
	tests := []struct {
		name     string
		media    *ytdlMedia
		err      error
		wantPipe bool
	}{
		{name: "direct read failed", media: &ytdlMedia{url: "https://media.example/a"}, err: errors.New("ffmpeg decode: exit status 1"), wantPipe: true},
		{name: "clean stop", media: &ytdlMedia{url: "https://media.example/a"}},
		{name: "pipe failed", err: errors.New("yt-dlp: exit status 1")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolveLog, ffmpegLog := installYTDLSeekFixtures(t, "240\nhttps://media.example/a?expire=4000000000\n{}\n", false)
			p := newTestPlayer()
			p.gapless = &gaplessStreamer{}
			p.suspended = true // Avoid a real speaker context.
			decoder := &failedReadDecoder{playbackTestDecoder: newPlaybackTestDecoder(), err: tt.err}
			p.current = &trackPipeline{decoder: decoder, stream: decoder, path: page, ytdlSeek: true, ytdlMedia: tt.media}
			p.Stop()

			tp, err := p.buildYTDLSource(page, time.Minute, false)
			if err != nil {
				t.Fatalf("buildYTDLSource() error = %v", err)
			}
			defer tp.close()
			gotPipe := tp.ytdlMedia == nil
			if gotPipe != tt.wantPipe || tp.ytdlNoDirect != tt.wantPipe {
				t.Fatalf("pipeline media = %v, noDirect = %v, want pipe %v", tp.ytdlMedia, tp.ytdlNoDirect, tt.wantPipe)
			}
			if log, _ := os.ReadFile(ffmpegLog); strings.Contains(string(log), "-i pipe:0") != tt.wantPipe {
				t.Fatalf("ffmpeg runs:\n%s\nwant the yt-dlp pipe %v", log, tt.wantPipe)
			}
			if tt.wantPipe {
				if got := fixtureLineCount(t, resolveLog); got != 0 {
					t.Fatalf("yt-dlp resolves = %d, want 0 for the pipe", got)
				}
				// Another page still plays from its direct URL.
				other, err := p.buildYTDLSource("https://music.youtube.com/watch?v=other", 0, false)
				if err != nil {
					t.Fatalf("buildYTDLSource(other) error = %v", err)
				}
				defer other.close()
				if other.ytdlMedia == nil {
					t.Fatal("another page plays the pipe, want its direct URL")
				}
			}
		})
	}
}
