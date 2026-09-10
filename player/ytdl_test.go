package player

import (
	"errors"
	"slices"
	"testing"
	"time"
)

func TestWaitCause(t *testing.T) {
	ytdlErr := errors.New("yt-dlp: Sign in to confirm you're not a bot")
	ffmpegErr := errors.New("ffmpeg: Invalid data found when processing input")

	tests := []struct {
		name  string
		d     time.Duration
		ytdl  error // value sent on ytdlErr, gated by send
		send  bool  // whether to send ytdl at all
		ff    error
		ffSnd bool
		want  error
	}{
		// Blocking (grace) path.
		{name: "ytdl error preferred over ffmpeg", d: 50 * time.Millisecond, ytdl: ytdlErr, send: true, ff: ffmpegErr, ffSnd: true, want: ytdlErr},
		{name: "ffmpeg error when ytdl exits clean", d: 50 * time.Millisecond, ytdl: nil, send: true, ff: ffmpegErr, ffSnd: true, want: ffmpegErr},
		{name: "both clean exit", d: 50 * time.Millisecond, ytdl: nil, send: true, ff: nil, ffSnd: true, want: nil},
		{name: "ytdl error without ffmpeg report", d: 50 * time.Millisecond, ytdl: ytdlErr, send: true, ff: nil, ffSnd: false, want: ytdlErr},
		{name: "neither reports before deadline", d: 50 * time.Millisecond, send: false, ffSnd: false, want: nil},
		// Non-blocking poll (d <= 0).
		{name: "poll ytdl error", d: 0, ytdl: ytdlErr, send: true, want: ytdlErr},
		{name: "poll ffmpeg fallback after clean ytdl", d: 0, ytdl: nil, send: true, ff: ffmpegErr, ffSnd: true, want: ffmpegErr},
		{name: "poll nothing pending", d: 0, send: false, ffSnd: false, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ytdlCh := make(chan error, 1)
			ffmpegCh := make(chan error, 1)
			if tt.send {
				ytdlCh <- tt.ytdl
			}
			if tt.ffSnd {
				ffmpegCh <- tt.ff
			}
			y := &ytdlPipeStreamer{ytdlErr: ytdlCh, ffmpegErr: ffmpegCh}
			got := y.waitCause(tt.d)
			if !errors.Is(got, tt.want) {
				t.Fatalf("waitCause = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestYTDLPArgsEndOfOptions verifies that the page URL is appended after the
// "--" end-of-options separator, so a URL beginning with "-" (e.g.
// "-o/root/evil") is treated as a URL by yt-dlp, not as an option.
func TestYTDLPArgsEndOfOptions(t *testing.T) {
	old := ytdlCookiesFrom
	t.Cleanup(func() { ytdlCookiesFrom = old })

	tests := []struct {
		name    string
		cookies string // value of ytdlCookiesFrom during the case
		pageURL string
		build   func(string) []string
	}{
		{name: "probe plain URL", pageURL: "https://example.com/a", build: ytdlProbeArgs},
		{name: "probe dash-prefixed URL", cookies: "chrome", pageURL: "-o/root/evil", build: ytdlProbeArgs},
		{name: "stream plain URL", pageURL: "https://example.com/b", build: ytdlStreamArgs},
		{name: "stream option-like URL", cookies: "firefox", pageURL: "--exec whoami", build: ytdlStreamArgs},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ytdlCookiesFrom = tt.cookies
			args := tt.build(tt.pageURL)
			if len(args) < 2 || args[len(args)-2] != "--" {
				t.Fatalf("args = %q, want \"--\" immediately before the page URL", args)
			}
			if args[len(args)-1] != tt.pageURL {
				t.Fatalf("args = %q, want page URL %q as the final argument", args, tt.pageURL)
			}
			if slices.Contains(args[:len(args)-2], tt.pageURL) {
				t.Fatalf("args = %q, page URL appears before the \"--\" separator", args)
			}
			if tt.cookies != "" && !slices.Contains(args, "--cookies-from-browser") {
				t.Fatalf("args = %q, want --cookies-from-browser when configured", args)
			}
		})
	}
}

// TestWaitCauseReturnsBeforeDeadline verifies that a present yt-dlp error is
// returned promptly rather than blocking for the full grace period waiting on
// a silent ffmpeg.
func TestWaitCauseReturnsBeforeDeadline(t *testing.T) {
	ytdlCh := make(chan error, 1)
	ytdlCh <- errors.New("boom")
	y := &ytdlPipeStreamer{ytdlErr: ytdlCh, ffmpegErr: make(chan error, 1)}

	start := time.Now()
	if err := y.waitCause(2 * time.Second); err == nil {
		t.Fatal("expected error, got nil")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("waitCause blocked %v waiting for ffmpeg; should return on yt-dlp error", elapsed)
	}
}
