package player

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gopxl/beep/v2"
)

func installYTDLRetryFixtures(t *testing.T, mode string) (string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX process fixtures")
	}

	dir := t.TempDir()
	attemptsPath := filepath.Join(dir, "attempts")
	ffmpegDonePath := filepath.Join(dir, "ffmpeg-done")
	ytdlScript := `#!/bin/sh
count=0
if [ -f "$YTDL_ATTEMPTS" ]; then
	count=$(wc -l < "$YTDL_ATTEMPTS")
fi
printf 'attempt\n' >> "$YTDL_ATTEMPTS"
case "$YTDL_MODE" in
	403-once)
		if [ "$count" -eq 0 ]; then
			printf 'ERROR: unable to download video data: HTTP Error 403: Forbidden\n' >&2
			exit 1
		fi
		;;
	403-always)
		printf 'ERROR: unable to download video data: HTTP Error 403: Forbidden\n' >&2
		exit 1
		;;
	unavailable)
		printf 'ERROR: Video unavailable\n' >&2
		exit 1
		;;
esac
printf '\001\002\003\004'
`
	ffmpegScript := `#!/bin/sh
trap 'printf "done\n" >> "$FFMPEG_DONE"' EXIT
cat
`
	for name, contents := range map[string]string{
		"yt-dlp": ytdlScript,
		"ffmpeg": ffmpegScript,
	} {
		writeExecutable(t, filepath.Join(dir, name), contents)
	}
	t.Setenv("YTDL_ATTEMPTS", attemptsPath)
	t.Setenv("YTDL_MODE", mode)
	t.Setenv("FFMPEG_DONE", ffmpegDonePath)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return attemptsPath, ffmpegDonePath
}

func fixtureLineCount(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatalf("read fixture output: %v", err)
	}
	return len(strings.Fields(string(b)))
}

func TestBuildYTDLPipelineRetriesTransient403(t *testing.T) {
	attemptsPath, ffmpegDonePath := installYTDLRetryFixtures(t, "403-once")
	p := &Player{sr: beep.SampleRate(44100), bitDepth: 16}

	pipeline, err := p.buildYTDLPipeline("https://www.youtube.com/watch?v=retry", 0)
	if err != nil {
		t.Fatalf("buildYTDLPipeline() error = %v", err)
	}
	defer pipeline.decoder.Close()
	if got := fixtureLineCount(t, attemptsPath); got != 2 {
		t.Fatalf("yt-dlp attempts = %d, want 2", got)
	}
	if got := fixtureLineCount(t, ffmpegDonePath); got < 1 {
		t.Fatal("abandoned ffmpeg process was not reaped before retry")
	}
}

func TestBuildYTDLPipelineStopsAfterTransient403RetryBudget(t *testing.T) {
	attemptsPath, _ := installYTDLRetryFixtures(t, "403-always")
	p := &Player{sr: beep.SampleRate(44100), bitDepth: 16}

	_, err := p.buildYTDLPipeline("https://www.youtube.com/watch?v=retry", 0)
	if err == nil || !strings.Contains(err.Error(), "HTTP Error 403: Forbidden") {
		t.Fatalf("buildYTDLPipeline() error = %v, want yt-dlp 403 cause", err)
	}
	if got := fixtureLineCount(t, attemptsPath); got != 3 {
		t.Fatalf("yt-dlp attempts = %d, want 3", got)
	}
}

func TestBuildYTDLPipelineDoesNotRetryPermanentYTDLError(t *testing.T) {
	attemptsPath, _ := installYTDLRetryFixtures(t, "unavailable")
	p := &Player{sr: beep.SampleRate(44100), bitDepth: 16}

	_, err := p.buildYTDLPipeline("https://www.youtube.com/watch?v=unavailable", 0)
	if err == nil || !strings.Contains(err.Error(), "Video unavailable") {
		t.Fatalf("buildYTDLPipeline() error = %v, want unavailable-video cause", err)
	}
	if got := fixtureLineCount(t, attemptsPath); got != 1 {
		t.Fatalf("yt-dlp attempts = %d, want 1", got)
	}
}

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

func TestYTDLPipeErrConcurrentWithStream(t *testing.T) {
	readErr := errors.New("yt-dlp PCM read failed")
	y := &ytdlPipeStreamer{
		reader:    bufio.NewReader(&readResult{data: []byte{1}, err: readErr}),
		ytdlErr:   make(chan error),
		ffmpegErr: make(chan error),
		state:     newPipeStreamState(0),
	}
	testPipeErrConcurrentWithStream(t, y, readErr)
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

func TestYTDLPipeCloseReapsBothProcesses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX process fixture")
	}
	ytdlCmd := exec.Command("sleep", "30")
	ffmpegCmd := exec.Command("sleep", "30")
	var ytdlStderr, ffmpegStderr limitedBuffer
	if err := ytdlCmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := ffmpegCmd.Start(); err != nil {
		_ = ytdlCmd.Process.Kill()
		_ = ytdlCmd.Wait()
		t.Fatal(err)
	}
	ytdlErr, ytdlDone := monitorExit(ytdlCmd, &ytdlStderr, "yt-dlp")
	ffmpegErr, ffmpegDone := monitorExit(ffmpegCmd, &ffmpegStderr, "ffmpeg")
	y := &ytdlPipeStreamer{
		ytdlCmd:    ytdlCmd,
		ffmpegCmd:  ffmpegCmd,
		pipe:       io.NopCloser(bytes.NewReader(nil)),
		ytdlErr:    ytdlErr,
		ffmpegErr:  ffmpegErr,
		ytdlDone:   ytdlDone,
		ffmpegDone: ffmpegDone,
	}

	start := time.Now()
	if err := y.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Close() took %v", elapsed)
	}
	select {
	case <-ytdlDone:
	default:
		t.Fatal("yt-dlp process was not reaped")
	}
	select {
	case <-ffmpegDone:
	default:
		t.Fatal("FFmpeg process was not reaped")
	}
}

// When ffmpeg exits before playback has read all of its output, the rest of
// the PCM is still in the pipe and must play out, ending in a clean EOF. The
// last track in a queue ends exactly this way; a read error here would be
// taken for a dropped connection and restart the track.
func TestYTDLPipeDrainsOutputAfterFFmpegExits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX process fixtures")
	}
	const frames = 4096 // s16le stereo: 4 bytes a frame, well under a pipe buffer
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "yt-dlp"), "#!/bin/sh\nhead -c 16384 /dev/zero\n")
	writeExecutable(t, filepath.Join(dir, "ffmpeg"), "#!/bin/sh\ncat\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	y, _, err := decodeYTDLPipe("https://www.youtube.com/watch?v=end", beep.SampleRate(44100), 16, 0)
	if err != nil {
		t.Fatalf("decodeYTDLPipe() error = %v", err)
	}
	defer y.Close()
	select {
	case <-y.ffmpegDone:
	case <-time.After(5 * time.Second):
		t.Fatal("ffmpeg fixture did not exit")
	}

	got := 0
	buf := make([][2]float64, 512)
	for {
		n, ok := y.Stream(buf)
		got += n
		if !ok {
			break
		}
	}
	if got != frames {
		t.Fatalf("streamed %d frames, want %d", got, frames)
	}
	if err := y.Err(); err != nil {
		t.Fatalf("Err() = %v after the output was read, want nil", err)
	}
}
