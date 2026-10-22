package player

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/gopxl/beep/v2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/internal/ytdlbin"
	"github.com/bjarneo/cliamp/internal/ytdlcookies"
	"github.com/bjarneo/cliamp/internal/ytdlp"
)

// pipeBufSize is the buffer size for audio pipe readers (yt-dlp, ffmpeg).
const pipeBufSize = 64 * 1024

// ytdlPipeTimeout limits how long we wait for yt-dlp to produce initial audio.
const ytdlPipeTimeout = 30 * time.Second

// ytdlCauseGrace bounds how long prefill or the background PCM reader waits,
// after the audio pipe closes, for yt-dlp or ffmpeg to report why. yt-dlp typically
// exits quickly with a stderr message (bot wall, 404, DRM); this only matters
// when the process is slow to flush and exit.
const ytdlCauseGrace = 3 * time.Second

const ytdlPipelineMaxAttempts = 3

// YTDLPAvailable reports whether the configured yt-dlp binary is executable.
func YTDLPAvailable() bool {
	return ytdlbin.Available()
}

func appendYTDLCookieArgs(args []string, pageURL string) []string {
	if browser := ytdlcookies.ForURL(pageURL); browser != "" {
		return append(args, "--cookies-from-browser", browser)
	}
	return args
}

// probeYTDLDuration runs a quick yt-dlp --print duration to obtain
// the track duration when --flat-playlist didn't provide it.
func probeYTDLDuration(pageURL string) time.Duration {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	args := []string{"--skip-download", "--no-playlist", "--socket-timeout", "10", "--print", "duration"}
	args = appendYTDLCookieArgs(args, pageURL)
	// "--" stops yt-dlp parsing pageURL as a flag. Callers gate on
	// playlist.IsURL, but keep the terminator so a future caller cannot turn
	// a crafted URL into --exec and reach arbitrary command execution.
	args = append(args, "--", pageURL)
	cmd := ytdlbin.CommandContext(ctx, args...)
	// WaitDelay ensures cmd.Output() doesn't hang indefinitely if the
	// process is killed but I/O pipe goroutines haven't drained. Without
	// this, a zombie yt-dlp child keeping stdout open can block Output()
	// forever, which in turn blocks the start and leaves the UI stuck at
	// "Buffering...".
	cmd.WaitDelay = 3 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	secs, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs * float64(time.Second))
}

// ffmpegInstallHint returns a platform-specific install command suggestion.
func ffmpegInstallHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "brew install ffmpeg"
	case "linux":
		if _, err := exec.LookPath("apt-get"); err == nil {
			return "sudo apt install ffmpeg"
		}
		if _, err := exec.LookPath("pacman"); err == nil {
			return "sudo pacman -S ffmpeg"
		}
		return "see https://ffmpeg.org/download.html"
	case "windows":
		return "winget install ffmpeg"
	default:
		return "see https://ffmpeg.org/download.html"
	}
}

// ytdlPipeStreamer streams PCM audio from a yt-dlp | ffmpeg pipe chain.
// yt-dlp downloads the best audio and writes raw data to stdout; ffmpeg reads
// that via a pipe and converts it to PCM on its stdout, which we consume.
type ytdlPipeStreamer struct {
	pipeReport // total stays 0: the length of a piped page is unknown
	ytdlCmd    *exec.Cmd
	ffmpegCmd  *exec.Cmd
	pipe       io.ReadCloser // ffmpeg stdout (PCM output)
	reader     *bufio.Reader // buffered reader over pipe
	ytdlErr    <-chan error  // yt-dlp exit error from monitoring goroutine
	ffmpegErr  <-chan error  // ffmpeg exit error from monitoring goroutine
	ytdlDone   <-chan struct{}
	ffmpegDone <-chan struct{}
	pcmBuf     []byte
	f32        bool // true = f32le, false = s16le
	closing    atomic.Bool
	closeOnce  sync.Once
}

// Stream runs on the network prefetch goroutine, never the speaker callback.
// Resolve a PCM EOF before the prefetcher snapshots Err and reports track end;
// otherwise normal-end Stop can kill the children and discard a late failure.
func (y *ytdlPipeStreamer) Stream(samples [][2]float64) (int, bool) {
	n, ok := streamFromReader(y.reader, samples, &y.pcmBuf, y.f32, y.state)
	y.state.pos.Add(int64(n))
	if !ok && y.state.err.load() == nil && !y.closing.Load() {
		y.state.err.publish(y.waitCause(ytdlCauseGrace))
	}
	return n, ok
}

// waitCause reports why the audio pipe closed,
// preferring yt-dlp's reason (bot wall, 404, DRM, region block) over ffmpeg's
// (undecodable input). With d <= 0 it polls without blocking; otherwise it
// waits up to d for a process to report. Returns nil if neither reported an
// error, leaving the caller to surface the bare EOF. Prefill and the background
// PCM reader call this sequentially, never on the speaker callback.
func (y *ytdlPipeStreamer) waitCause(d time.Duration) error {
	if d <= 0 {
		select {
		case e := <-y.ytdlErr:
			if e != nil {
				return e
			}
		default:
		}
		select {
		case e := <-y.ffmpegErr:
			return e
		default:
			return nil
		}
	}
	deadline := time.After(d)
	ytdlDone, ffmpegDone := false, false
	var ffErr error
	for !ytdlDone || !ffmpegDone {
		select {
		case e := <-y.ytdlErr:
			ytdlDone = true
			if e != nil {
				return e
			}
		case e := <-y.ffmpegErr:
			ffmpegDone = true
			ffErr = e
		case <-deadline:
			return ffErr
		}
	}
	return ffErr
}

func (y *ytdlPipeStreamer) Seek(int) error { return nil }

// interrupt releases a blocked PCM read without waiting for yt-dlp or ffmpeg.
// Closing the pipe unblocks the background prefetch reader on a stalled
// download. Close reaps both processes later.
func (y *ytdlPipeStreamer) interrupt() {
	// Mark teardown before closing the pipe or killing either process, since
	// Stop and source replacement interrupt before Close reaps the children.
	y.closing.Store(true)
	_ = y.pipe.Close()
	if y.ytdlCmd.Process != nil {
		_ = y.ytdlCmd.Process.Kill()
	}
	if y.ffmpegCmd.Process != nil {
		_ = y.ffmpegCmd.Process.Kill()
	}
}

func (y *ytdlPipeStreamer) Close() error {
	y.closeOnce.Do(func() {
		y.interrupt()
		// The monitor goroutines own Wait. Killing both children and waiting
		// for their done signals guarantees Close does not leave zombies.
		if y.ytdlDone != nil {
			<-y.ytdlDone
		}
		if y.ffmpegDone != nil {
			<-y.ffmpegDone
		}
	})
	return nil
}

// ytdlExitError keeps the fatal diagnostic separate from warning text so the
// one-row status and retry classifier cannot mistake a warning for the failure.
// The full captured stderr is written to the log by monitorExit.
type ytdlExitError struct {
	cause      error
	diagnostic string
}

func (e *ytdlExitError) Error() string {
	if e.diagnostic != "" {
		return "yt-dlp: " + e.diagnostic
	}
	return fmt.Sprintf("yt-dlp: %v (see cliamp.log)", e.cause)
}

func (e *ytdlExitError) Unwrap() error { return e.cause }

// ytdlFatalDiagnostic selects the final ERROR line, not WARNING lines or their
// continuations. Without a fatal diagnostic, an exit status alone is not enough
// evidence to retry. Earlier errors can describe failed extractor fallbacks.
func ytdlFatalDiagnostic(stderr string) string {
	var diagnostic string
	for line := range strings.SplitSeq(stderr, "\n") {
		if text, ok := strings.CutPrefix(ansi.Strip(line), "ERROR:"); ok {
			diagnostic = strings.Join(strings.Fields(text), " ")
		}
	}
	return diagnostic
}

// monitorExit waits for cmd to exit, logs captured stderr on failure and reports
// a wrapped error (or nil on clean exit). The buffered channel lets the monitor
// finish even with no receiver, including after Close kills the process.
// closing, when set, marks the exit as intentional teardown rather than a
// playback failure; it is nil for callers that never tear a process down.
func monitorExit(cmd *exec.Cmd, stderr *limitedBuffer, name string, closing *atomic.Bool) (<-chan error, <-chan struct{}) {
	ch := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		err := cmd.Wait()
		if err == nil {
			ch <- nil
			return
		}
		trimmed := strings.TrimSpace(stderr.String())
		if closing != nil && closing.Load() {
			// Close kills both processes on stop/seek and abandoned retries.
			// Teardown exits remain available at debug level, not as playback
			// failures every time the user changes tracks.
			applog.Debug("%s: %v: %s", name, err, trimmed)
			ch <- nil
			return
		}
		applog.Error("%s: %v: %s", name, err, trimmed)
		switch {
		case name == "yt-dlp":
			ch <- &ytdlExitError{cause: err, diagnostic: ytdlFatalDiagnostic(trimmed)}
		case trimmed != "":
			ch <- fmt.Errorf("%s: %w: %s", name, err, trimmed)
		default:
			ch <- fmt.Errorf("%s: %w", name, err)
		}
	}()
	return ch, done
}

// decodeYTDLPipe starts a yt-dlp | ffmpeg pipe chain for the given page URL
// and returns a streaming PCM decoder. If startSec > 0, ffmpeg -ss is used
// to skip to the desired position in the input stream.
func decodeYTDLPipe(pageURL string, sr beep.SampleRate, bitDepth, startSec int) (*ytdlPipeStreamer, beep.Format, error) {
	if _, err := ytdlbin.LookPath(); err != nil {
		return nil, beep.Format{}, ytdlbin.NotFoundErrorWithAdvice(err, "install: "+ytdlp.InstallHint())
	}
	if err := requireFFmpeg(); err != nil {
		return nil, beep.Format{}, err
	}

	// os.Pipe connects yt-dlp stdout → ffmpeg stdin.
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, beep.Format{}, fmt.Errorf("os.Pipe: %w", err)
	}

	// Start yt-dlp: download best audio to stdout.
	// Prefer direct HTTPS/HTTP streams over HLS (m3u8). HLS requires segment
	// downloading and muxing which doesn't pipe cleanly to stdout.
	// Live streams (e.g. YouTube live) expose no audio-only formats at all,
	// only muxed video+audio over HLS, so fall back to "best" as a last
	// resort; the ffmpeg stage below outputs PCM audio and drops the video.
	ytdlArgs := []string{
		"-f", "bestaudio[protocol=https]/bestaudio[protocol=http]/bestaudio[protocol!=m3u8_native][protocol!=m3u8]/bestaudio/best",
		"--no-playlist",
		// --quiet drops progress chatter but keeps warnings on stderr. Do not
		// add --no-warnings: yt-dlp's own "version is older than 90 days" and
		// missing-JS-runtime warnings are what explain a persistent HTTP 403,
		// and monitorExit logs them while prioritizing the fatal status line.
		"--quiet",
		"--socket-timeout", "15",
		"-o", "-",
	}
	ytdlArgs = appendYTDLCookieArgs(ytdlArgs, pageURL)
	ytdlArgs = append(ytdlArgs, "--", pageURL)
	ytdlCmd := ytdlbin.Command(ytdlArgs...)
	ytdlCmd.Stdout = pw
	var ytdlStderr limitedBuffer
	ytdlCmd.Stderr = &ytdlStderr
	if err := ytdlCmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		return nil, beep.Format{}, fmt.Errorf("yt-dlp start: %w", err)
	}

	// Start ffmpeg: read from pipe, output PCM to stdout.
	// If startSec > 0, use -ss to seek into the input stream.
	_, _, precision := ffmpegPCMArgs(bitDepth)
	var ffmpegArgs []string
	if startSec > 0 {
		ffmpegArgs = append(ffmpegArgs, "-ss", strconv.Itoa(startSec))
	}
	ffmpegArgs = append(ffmpegArgs, "-i", "pipe:0")
	ffmpegArgs = append(ffmpegArgs, pcmOutputArgs(sr, bitDepth)...)
	ffmpegCmd := exec.Command("ffmpeg", ffmpegArgs...)
	ffmpegCmd.Stdin = pr
	var ffmpegStderr limitedBuffer
	ffmpegCmd.Stderr = &ffmpegStderr
	// An owned pipe carries ffmpeg's PCM, like the yt-dlp → ffmpeg hop above.
	// StdoutPipe would tie the read end to cmd.Wait, which monitorExit runs
	// during playback: Wait closes it as soon as ffmpeg exits, dropping PCM
	// still in the pipe and failing the next read instead of reaching EOF.
	pcmR, pcmW, err := os.Pipe()
	if err != nil {
		pw.Close()
		pr.Close()
		ytdlCmd.Process.Kill()
		ytdlCmd.Wait()
		return nil, beep.Format{}, fmt.Errorf("os.Pipe: %w", err)
	}
	ffmpegCmd.Stdout = pcmW
	if err := ffmpegCmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		pcmR.Close()
		pcmW.Close()
		ytdlCmd.Process.Kill()
		ytdlCmd.Wait()
		return nil, beep.Format{}, fmt.Errorf("ffmpeg start: %w", err)
	}

	// Close parent's copies of pipe ends. yt-dlp owns pw (write end), ffmpeg
	// owns pr (read end) and pcmW (PCM write end). If the parent keeps these
	// open, EOF won't propagate when the owning process exits.
	pw.Close()
	pr.Close()
	pcmW.Close()

	// Monitor each process's exit so we can surface why the pipe closed. A
	// process's stderr is only safe to read after Wait() returns, so the
	// capture happens inside monitorExit.
	streamer := &ytdlPipeStreamer{
		ytdlCmd:    ytdlCmd,
		ffmpegCmd:  ffmpegCmd,
		pipe:       pcmR,
		reader:     bufio.NewReaderSize(pcmR, pipeBufSize),
		pipeReport: pipeReport{state: newPipeStreamState(0)},
		f32:        bitDepth == 32,
	}
	// The monitors read closing to tell teardown from a playback failure, so
	// the streamer that Close sets it on must exist before they start.
	streamer.ytdlErr, streamer.ytdlDone = monitorExit(ytdlCmd, &ytdlStderr, "yt-dlp", &streamer.closing)
	streamer.ffmpegErr, streamer.ffmpegDone = monitorExit(ffmpegCmd, &ffmpegStderr, "ffmpeg", &streamer.closing)

	format := beep.Format{
		SampleRate:  sr,
		NumChannels: 2,
		Precision:   precision,
	}
	return streamer, format, nil
}

// buildYTDLPipeline creates a trackPipeline for a yt-dlp URL.
// If startSec > 0, playback begins at that offset (seek-by-restart).
func (p *Player) buildYTDLPipeline(pageURL string, startSec int) (*trackPipeline, error) {
	p.streamTitle.Store("")

	for attempt := 1; ; attempt++ {
		decoder, format, err := decodeYTDLPipe(pageURL, p.sr, p.bitDepth, startSec)
		if err != nil {
			return nil, err
		}

		if err := prefillYTDLPipe(decoder); err != nil {
			if !isTransientYTDL403(err) {
				return nil, err
			}
			if attempt == ytdlPipelineMaxAttempts {
				// monitorExit logs warnings from every failed attempt; the
				// status shows the last attempt's fatal diagnostic.
				return nil, err
			}
			continue
		}

		// The prefetch reads the pipe away from the speaker callback. A
		// stalled download then plays silence and does not hold the speaker
		// lock, which would freeze every control.
		return p.prefetchNetworkPipeline(&trackPipeline{
			decoder:      decoder,
			stream:       decoder,
			format:       format,
			seekable:     false,
			path:         pageURL,
			ytdlSeek:     true,
			streamOffset: time.Duration(startSec) * time.Second,
		}, true), nil
	}
}

// buildYTDLSource starts the yt-dlp | ffmpeg chain for pageURL at 0. With
// probe and no known duration, it asks yt-dlp for the duration while the chain
// starts.
func (p *Player) buildYTDLSource(pageURL string, knownDuration time.Duration, probe bool) (*trackPipeline, error) {
	probe = probe && knownDuration == 0
	// Probe duration concurrently with pipeline setup so it doesn't delay playback.
	probeCh := make(chan time.Duration, 1)
	if probe {
		go func() { probeCh <- probeYTDLDuration(pageURL) }()
	}
	tp, err := p.buildYTDLPipeline(pageURL, 0)
	if err != nil {
		return nil, err
	}
	if probe {
		// The probe ran concurrently with buildYTDLPipeline. Try to
		// collect the result, but don't block playback for more than 2s.
		// A hung probeYTDLDuration (e.g. yt-dlp zombie keeping pipes
		// open) previously blocked here forever, leaving the UI stuck
		// at "Buffering...".
		select {
		case d := <-probeCh:
			if d > 0 {
				knownDuration = d
			}
		case <-time.After(2 * time.Second):
			// Probe still running — start playback without duration.
			// The seek bar won't show progress but audio plays immediately.
		}
	}
	tp.knownDuration = knownDuration
	return tp, nil
}

func prefillYTDLPipe(decoder *ytdlPipeStreamer) error {
	// Pre-fill: block until yt-dlp + ffmpeg produce initial audio data.
	// This runs in a tea.Cmd goroutine (not the UI thread), so a chain that
	// fails before its first byte reports why before the track starts. A 30s
	// timeout prevents hanging when yt-dlp is slow to produce output.
	stop := func() { decoder.Close() }
	return peekWithTimeout(decoder.reader, 1, ytdlPipeTimeout, stop, func(err error) error {
		// The audio pipe closed before producing a byte. Prefer the real
		// cause from yt-dlp (e.g. "Sign in to confirm you're not a bot",
		// "HTTP Error 404", DRM, region block) or ffmpeg over the opaque
		// EOF — the pipe only tells us the upstream closed, not why.
		cause := decoder.waitCause(ytdlCauseGrace)
		decoder.Close()
		if cause != nil {
			return cause
		}
		return fmt.Errorf("waiting for audio data: %w", err)
	})
}

func isTransientYTDL403(err error) bool {
	var exitErr *ytdlExitError
	return errors.As(err, &exitErr) && strings.Contains(exitErr.diagnostic, "HTTP Error 403: Forbidden")
}
