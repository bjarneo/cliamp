package player

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gopxl/beep/v2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/internal/ytdlbin"
	"github.com/bjarneo/cliamp/ui"
)

// ytdlpWarning is what a real yt-dlp prints to stderr before failing with a
// 403 it cannot avoid. It must reach the user, so the playback command may not
// pass --no-warnings.
const ytdlpWarning = "WARNING: yt-dlp version stable@2024.01.01 is older than 90 days"

func installYTDLRetryFixtures(t *testing.T, mode string) (string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX process fixtures")
	}

	dir := t.TempDir()
	attemptsPath := filepath.Join(dir, "attempts")
	ffmpegDonePath := filepath.Join(dir, "ffmpeg-done")
	suppressedPath := filepath.Join(dir, "warnings-suppressed")
	ytdlScript := `#!/bin/sh
for arg in "$@"; do
	if [ "$arg" = "--no-warnings" ]; then
		printf 'yes\n' > "$YTDL_SUPPRESSED"
	fi
done
printf '%s\n' "$YTDL_WARNING" >&2
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
	t.Setenv("YTDL_WARNING", ytdlpWarning)
	t.Setenv("YTDL_SUPPRESSED", suppressedPath)
	t.Setenv("FFMPEG_DONE", ffmpegDonePath)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// CLIAMP_YTDLP outranks PATH, so a developer with it set would otherwise
	// run their real yt-dlp instead of this fixture.
	t.Setenv(ytdlbin.EnvVar, "")
	t.Cleanup(func() {
		if _, err := os.Stat(suppressedPath); err == nil {
			t.Error("yt-dlp was invoked with --no-warnings; its 403 diagnostics would be hidden")
		}
	})
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

// TestBuildYTDLPipelineStopsAfterTransient403RetryBudget also covers the
// warning diagnostics in the log without hiding the fatal status message.
func TestBuildYTDLPipelineStopsAfterTransient403RetryBudget(t *testing.T) {
	attemptsPath, _ := installYTDLRetryFixtures(t, "403-always")
	logPath := captureYTDLLog(t)
	p := &Player{sr: beep.SampleRate(44100), bitDepth: 16}

	_, err := p.buildYTDLPipeline("https://www.youtube.com/watch?v=retry", 0)
	if err == nil || !strings.Contains(err.Error(), "HTTP Error 403: Forbidden") {
		t.Fatalf("buildYTDLPipeline() error = %v, want yt-dlp 403 cause", err)
	}
	row := ui.FitRect(fmt.Sprintf("ERR: %s", err), 78, 1)
	if !strings.Contains(row, "HTTP Error 403: Forbidden") || strings.Contains(row, "WARNING:") {
		t.Errorf("status row = %q, want fatal diagnostic without warnings", row)
	}
	logged, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(logged), ytdlpWarning) || !strings.Contains(string(logged), "HTTP Error 403: Forbidden") {
		t.Errorf("log = %q, want warning and fatal diagnostic", logged)
	}
	if got := fixtureLineCount(t, attemptsPath); got != 3 {
		t.Fatalf("yt-dlp attempts = %d, want 3", got)
	}
}

// A missing binary selected by CLIAMP_YTDLP or ytdlp_path must name that
// selection instead of advising an install that the selection would override.
func TestDecodeYTDLPipeNamesSelectedBinary(t *testing.T) {
	t.Setenv(ytdlbin.EnvVar, filepath.Join(t.TempDir(), "absent-yt-dlp"))

	_, _, err := decodeYTDLPipe("https://www.youtube.com/watch?v=x", beep.SampleRate(44100), 16, 0)
	if err == nil {
		t.Fatal("decodeYTDLPipe() error = nil, want missing yt-dlp error")
	}
	if !strings.Contains(err.Error(), "absent-yt-dlp") || !strings.Contains(err.Error(), ytdlbin.EnvVar) {
		t.Fatalf("error = %q, want the selected binary and its source", err)
	}
	if strings.Contains(err.Error(), "install") {
		t.Fatalf("error = %q, want no install advice for an explicit selection", err)
	}
	// The guard replaces the os/exec failure with its own message, so the
	// lookup cause has to stay reachable for callers matching on it.
	var execErr *exec.Error
	if !errors.As(err, &execErr) || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("error %v does not unwrap to the exec lookup failure", err)
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
	logPath := captureYTDLLog(t)
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
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logged), "level=ERROR") {
		t.Errorf("intentional Close logged as playback failure: %s", logged)
	}
}

func captureYTDLLog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cliamp.log")
	closeLog, err := applog.Init(path, applog.LevelError)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := closeLog(); err != nil {
			t.Error(err)
		}
	})
	return path
}

func TestBuildYTDLPipelineDoesNotRetryWarning403(t *testing.T) {
	attemptsPath, _ := installYTDLRetryFixtures(t, "unavailable")
	t.Setenv("YTDL_WARNING", "WARNING: extractor fallback: HTTP Error 403: Forbidden")
	p := &Player{sr: beep.SampleRate(44100), bitDepth: 16}
	_, err := p.buildYTDLPipeline("https://www.youtube.com/watch?v=unavailable", 0)
	if err == nil {
		t.Fatal("expected permanent error")
	}
	row := ui.FitRect(fmt.Sprintf("ERR: %s", err), 78, 1)
	if !strings.Contains(row, "Video unavailable") || strings.Contains(row, "WARNING:") {
		t.Errorf("status row = %q, want fatal diagnostic", row)
	}
	if got := fixtureLineCount(t, attemptsPath); got != 1 {
		t.Errorf("yt-dlp attempts = %d, want 1", got)
	}
}

func TestMonitorExitYTDLDiagnostics(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX process fixture")
	}
	tests := []struct {
		name, stderr, process, want string
		retry                       bool
	}{
		{name: "warning before fatal 403", stderr: ytdlpWarning + "\nERROR: HTTP Error 403: Forbidden\n", want: "HTTP Error 403: Forbidden", retry: true},
		{name: "warning 403 before permanent error", stderr: "WARNING: HTTP Error 403: Forbidden\nERROR: Video unavailable\n", want: "Video unavailable"},
		{name: "warning 403 after permanent error", stderr: "ERROR: Video unavailable\nWARNING: HTTP Error 403: Forbidden\n", want: "Video unavailable"},
		{name: "multiline warning 403", stderr: "WARNING: extractor fallback\nHTTP Error 403: Forbidden\nERROR: Video unavailable", want: "Video unavailable"},
		{name: "indented warning continuation is not fatal", stderr: "ERROR: Video unavailable\nWARNING: extractor fallback\n    ERROR: HTTP Error 403: Forbidden", want: "Video unavailable"},
		{name: "warning only", stderr: "WARNING: HTTP Error 403: Forbidden", want: "exit status 1"},
		{name: "unmarked warning continuation", stderr: "WARNING: extractor fallback\nHTTP Error 403: Forbidden", want: "exit status 1"},
		{name: "no stderr", want: "exit status 1"},
		{name: "last fatal wins", stderr: "ERROR: HTTP Error 403: Forbidden\nERROR: Video unavailable", want: "Video unavailable"},
		{name: "ffmpeg is not yt-dlp", stderr: "ERROR: yt-dlp: HTTP Error 403: Forbidden", process: "ffmpeg", want: "HTTP Error 403: Forbidden"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logPath := captureYTDLLog(t)
			cmd := exec.Command("sh", "-c", `printf '%s' "$DIAGNOSTIC" >&2; exit 1`)
			cmd.Env = append(os.Environ(), "DIAGNOSTIC="+tt.stderr)
			var stderr limitedBuffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			process := tt.process
			if process == "" {
				process = "yt-dlp"
			}
			ch, done := monitorExit(cmd, &stderr, process)
			err := <-ch
			<-done
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			if strings.ContainsAny(err.Error(), "\r\n") || strings.Contains(err.Error(), "WARNING:") {
				t.Errorf("status diagnostic = %q, want one line without warnings", err)
			}
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
				t.Errorf("error lost exit cause: %v", err)
			}
			if got := isTransientYTDL403(fmt.Errorf("playback: %w", err)); got != tt.retry {
				t.Errorf("retry = %v, want %v", got, tt.retry)
			}
			logged, readErr := os.ReadFile(logPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			for _, line := range strings.Split(tt.stderr, "\n") {
				if !strings.Contains(string(logged), line) {
					t.Errorf("log missing %q: %s", line, logged)
				}
			}
		})
	}
}

// PCM EOF can precede cmd.Wait completing; polling once loses the failure and
// treats a broken stream as a successful end of track.
func TestYTDLPipeStreamWaitsForExitDiagnostic(t *testing.T) {
	ytdlCh := make(chan error, 1)
	ffmpegCh := make(chan error, 1)
	cause := errors.New("yt-dlp: Video unavailable")
	y := &ytdlPipeStreamer{
		reader:  bufio.NewReader(strings.NewReader("")),
		ytdlErr: ytdlCh, ffmpegErr: ffmpegCh, state: newPipeStreamState(0),
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		ffmpegCh <- errors.New("ffmpeg: Invalid data found when processing input")
		ytdlCh <- cause
	}()
	n, ok := y.Stream(make([][2]float64, 1))
	if n != 0 || ok {
		t.Fatalf("Stream() = %d, %v, want EOF", n, ok)
	}
	if !errors.Is(y.Err(), cause) {
		t.Errorf("Err() = %v, want yt-dlp cause", y.Err())
	}
}
