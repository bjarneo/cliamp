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

	"github.com/gopxl/beep/v2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/internal/ytdlbin"
	"github.com/bjarneo/cliamp/internal/ytdlcookies"
)

// pipeBufSize is the buffer size for audio pipe readers (yt-dlp, ffmpeg).
const pipeBufSize = 64 * 1024

// ytdlPipeTimeout limits how long we wait for yt-dlp to produce initial audio.
const ytdlPipeTimeout = 30 * time.Second

// ytdlCauseGrace bounds how long prefillYTDLPipe waits, after the audio pipe
// closes with no data, for yt-dlp or ffmpeg to report why. yt-dlp typically
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
	// forever, which in turn blocks PlayYTDL and leaves the UI stuck at
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

// InstallYTDLP attempts to install yt-dlp using the system package manager.
// Returns nil on success. The caller should re-check YTDLPAvailable() after.
func InstallYTDLP() error {
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("brew"); err == nil {
			cmd := exec.Command("brew", "install", "yt-dlp")
			cmd.Stdout = os.Stderr
			cmd.Stderr = os.Stderr
			return cmd.Run()
		}
		// Fall through to pip
	case "linux":
		if _, err := exec.LookPath("apt-get"); err == nil {
			cmd := exec.Command("sudo", "apt-get", "install", "-y", "yt-dlp")
			cmd.Stdout = os.Stderr
			cmd.Stderr = os.Stderr
			return cmd.Run()
		}
		if _, err := exec.LookPath("pacman"); err == nil {
			cmd := exec.Command("sudo", "pacman", "-S", "--noconfirm", "yt-dlp")
			cmd.Stdout = os.Stderr
			cmd.Stderr = os.Stderr
			return cmd.Run()
		}
	}
	// Fallback: pip/pipx
	if path, err := exec.LookPath("pipx"); err == nil {
		cmd := exec.Command(path, "install", "yt-dlp")
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	if path, err := exec.LookPath("pip3"); err == nil {
		cmd := exec.Command(path, "install", "yt-dlp")
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	return fmt.Errorf("no supported package manager found — install manually: https://github.com/yt-dlp/yt-dlp#installation")
}

// YtdlpInstallHint returns a platform-specific install command suggestion.
func YtdlpInstallHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "brew install yt-dlp"
	case "linux":
		if _, err := exec.LookPath("apt-get"); err == nil {
			return "sudo apt install yt-dlp"
		}
		if _, err := exec.LookPath("pacman"); err == nil {
			return "sudo pacman -S yt-dlp"
		}
		return "pip install yt-dlp"
	case "windows":
		return "winget install yt-dlp"
	default:
		return "pip install yt-dlp"
	}
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
	ytdlCmd    *exec.Cmd
	ffmpegCmd  *exec.Cmd
	pipe       io.ReadCloser // ffmpeg stdout (PCM output)
	reader     *bufio.Reader // buffered reader over pipe
	ytdlErr    <-chan error  // yt-dlp exit error from monitoring goroutine
	ffmpegErr  <-chan error  // ffmpeg exit error from monitoring goroutine
	ytdlDone   <-chan struct{}
	ffmpegDone <-chan struct{}
	pcmBuf     []byte
	state      *pipeStreamState
	f32        bool // true = f32le, false = s16le
	closing    atomic.Bool
	closeOnce  sync.Once
}

// Stream runs on the speaker callback goroutine while the speaker lock is
// held, so it never waits for a process to exit: watchExitCause publishes a
// failure that lands around PCM EOF.
func (y *ytdlPipeStreamer) Stream(samples [][2]float64) (int, bool) {
	n, ok := streamFromReader(y.reader, samples, &y.pcmBuf, y.f32, y.state)
	y.state.pos.Add(int64(n))
	return n, ok
}

// watchExitCause publishes why the pipe chain died into the error latch that
// Player.StreamErr polls, so a failure reported around PCM EOF is not mistaken
// for a clean end of track. The exit monitors already run off the audio
// thread; this only forwards their result, preferring yt-dlp's reason (bot
// wall, 404, DRM, region block) over ffmpeg's (undecodable input).
//
// It consumes the exit channels, so it must start only after prefill, the
// other reader, is finished with them. The returned channel closes when the
// watcher has finished; production ignores it, tests await it.
func (y *ytdlPipeStreamer) watchExitCause() <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		var ytdlErr, ffmpegErr error
		// Each monitor reports exactly once on a buffered channel, so two
		// receives drain both and the goroutine always finishes.
		for range 2 {
			select {
			case e := <-y.ytdlErr:
				ytdlErr = e
			case e := <-y.ffmpegErr:
				ffmpegErr = e
			}
		}
		// Close kills both children on stop, seek, and abandoned retries, so
		// whatever they report afterwards is teardown, not playback failure.
		// The flag states that intent directly; an exit code cannot, because
		// a killed process reports a signal on POSIX but status 1 on Windows.
		if y.closing.Load() {
			return
		}
		for _, err := range []error{ytdlErr, ffmpegErr} {
			if err != nil {
				y.state.err.publish(err)
				return
			}
		}
	}()
	return done
}

// waitCause reports why the audio pipe closed without producing audio,
// preferring yt-dlp's reason (bot wall, 404, DRM, region block) over ffmpeg's
// (undecodable input). With d <= 0 it polls without blocking; otherwise it
// waits up to d for a process to report. Returns nil if neither reported an
// error, leaving the caller to surface the bare EOF. Only prefill calls this,
// off the audio thread and before watchExitCause takes over the channels.
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

func (y *ytdlPipeStreamer) Err() error {
	if y.state == nil {
		return nil
	}
	return y.state.err.load()
}
func (y *ytdlPipeStreamer) Len() int { return 0 }
func (y *ytdlPipeStreamer) Position() int {
	if y.state == nil {
		return 0
	}
	return int(y.state.pos.Load())
}
func (y *ytdlPipeStreamer) Seek(int) error { return nil }

func (y *ytdlPipeStreamer) Close() error {
	y.closeOnce.Do(func() {
		// Tell watchExitCause the exits it is about to see are teardown.
		y.closing.Store(true)
		// Kill both processes to stop downloading/decoding.
		if y.ytdlCmd.Process != nil {
			y.ytdlCmd.Process.Kill()
		}
		if y.ffmpegCmd.Process != nil {
			y.ffmpegCmd.Process.Kill()
		}
		y.pipe.Close()
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
		if text, ok := strings.CutPrefix(line, "ERROR:"); ok {
			diagnostic = strings.Join(strings.Fields(text), " ")
		}
	}
	return diagnostic
}

// monitorExit waits for cmd to exit, logs captured stderr on failure and reports
// a wrapped error (or nil on clean exit). The buffered channel lets the monitor
// finish even with no receiver, including after Close kills the process.
// closing, when set, marks the exit as teardown from Close rather than a
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
		} else {
			applog.Error("%s: %v: %s", name, err, trimmed)
		}
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
		return nil, beep.Format{}, ytdlbin.NotFoundErrorWithAdvice(err, "install: "+YtdlpInstallHint())
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, beep.Format{}, fmt.Errorf("ffmpeg is required — install: %s", ffmpegInstallHint())
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
	pcmFmt, codec, precision := ffmpegPCMArgs(bitDepth)
	var ffmpegArgs []string
	if startSec > 0 {
		ffmpegArgs = append(ffmpegArgs, "-ss", strconv.Itoa(startSec))
	}
	ffmpegArgs = append(ffmpegArgs,
		"-i", "pipe:0",
		"-f", pcmFmt,
		"-acodec", codec,
		"-ar", strconv.Itoa(int(sr)),
		"-ac", "2",
		"-loglevel", "error",
		"pipe:1",
	)
	ffmpegCmd := exec.Command("ffmpeg", ffmpegArgs...)
	ffmpegCmd.Stdin = pr
	var ffmpegStderr limitedBuffer
	ffmpegCmd.Stderr = &ffmpegStderr
	// An owned pipe carries ffmpeg's PCM, matching the yt-dlp → ffmpeg hop
	// above. StdoutPipe would tie the read end to cmd.Wait, which the exit
	// monitor runs concurrently with playback: Wait closes the read end as
	// soon as ffmpeg exits, discarding PCM still buffered in the pipe and
	// failing the next read with "file already closed" instead of EOF.
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
		ytdlCmd:   ytdlCmd,
		ffmpegCmd: ffmpegCmd,
		pipe:      pcmR,
		reader:    bufio.NewReaderSize(pcmR, pipeBufSize),
		state:     newPipeStreamState(0),
		f32:       bitDepth == 32,
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

		// Prefill is done with the exit channels, so the streamer can now
		// watch them for a failure that arrives during playback.
		_ = decoder.watchExitCause()

		return &trackPipeline{
			decoder:      decoder,
			stream:       decoder,
			format:       format,
			seekable:     false,
			path:         pageURL,
			ytdlSeek:     true,
			streamOffset: time.Duration(startSec) * time.Second,
		}, nil
	}
}

func prefillYTDLPipe(decoder *ytdlPipeStreamer) error {
	// Pre-fill: block until yt-dlp + ffmpeg produce initial audio data.
	// This runs in a tea.Cmd goroutine (not the UI thread), ensuring the
	// speaker goroutine won't block on an empty pipe and hold its lock
	// (which would freeze the UI). A 30s timeout prevents hanging when
	// yt-dlp is slow to produce output.
	peekErr := make(chan error, 1)
	go func() {
		_, err := decoder.reader.Peek(1)
		peekErr <- err
	}()
	select {
	case err := <-peekErr:
		if err != nil {
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
		}
	case <-time.After(ytdlPipeTimeout):
		decoder.Close()
		<-peekErr // drain goroutine after Close() unblocks the pipe
		return fmt.Errorf("timed out waiting for audio data (%v)", ytdlPipeTimeout)
	}
	return nil
}

func isTransientYTDL403(err error) bool {
	var exitErr *ytdlExitError
	return errors.As(err, &exitErr) && strings.Contains(exitErr.diagnostic, "HTTP Error 403: Forbidden")
}
