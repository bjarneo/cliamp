package player

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gopxl/beep/v2"

	"github.com/bjarneo/cliamp/internal/ytdlcookies"
	"github.com/bjarneo/cliamp/internal/ytdlp"
)

// pipeBufSize is the buffer size for audio pipe readers (yt-dlp, ffmpeg).
const pipeBufSize = 64 * 1024

// ytdlPipeTimeout limits how long we wait for yt-dlp to produce initial audio.
const ytdlPipeTimeout = 30 * time.Second

// ytdlCauseGrace bounds how long buildYTDLPipeline waits, after the audio pipe
// closes with no data, for yt-dlp or ffmpeg to report why. yt-dlp typically
// exits quickly with a stderr message (bot wall, 404, DRM); this only matters
// when the process is slow to flush and exit.
const ytdlCauseGrace = 3 * time.Second

// ytdlStreamCauseGrace bounds how long Stream waits after PCM EOF for
// monitorExit to report a mid-stream yt-dlp or ffmpeg error.
const ytdlStreamCauseGrace = 100 * time.Millisecond

const ytdlPipelineMaxAttempts = 3

// ytdlAudioFormat is the yt-dlp format selector for playback. It prefers
// direct HTTPS/HTTP streams over HLS (m3u8): HLS requires segment downloading
// and muxing, which doesn't pipe cleanly to stdout. Live streams (e.g. YouTube
// live) expose no audio-only formats at all, only muxed video+audio over HLS,
// so "best" is the last resort; ffmpeg outputs PCM audio and drops the video.
const ytdlAudioFormat = "bestaudio[protocol=https]/bestaudio[protocol=http]/bestaudio[protocol!=m3u8_native][protocol!=m3u8]/bestaudio/best"

// YTDLPAvailable reports whether yt-dlp is installed and on PATH.
func YTDLPAvailable() bool {
	_, err := exec.LookPath("yt-dlp")
	return err == nil
}

func appendYTDLCookieArgs(args []string, pageURL string) ([]string, func(), error) {
	cookieArgs, cleanup, err := ytdlcookies.Args(pageURL)
	return append(args, cookieArgs...), cleanup, err
}

// probeYTDLDuration runs a quick yt-dlp --print duration to obtain
// the track duration when --flat-playlist did not provide it.
func probeYTDLDuration(parent context.Context, pageURL string) time.Duration {
	if parent.Err() != nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	args := []string{"--skip-download", "--no-playlist", "--socket-timeout", "10", "--print", "duration"}
	args, cleanup, err := appendYTDLCookieArgs(args, pageURL)
	defer cleanup()
	if err != nil || ctx.Err() != nil {
		return 0
	}
	// "--" stops yt-dlp parsing pageURL as a flag. Callers gate on
	// playlist.IsURL, but keep the terminator so a future caller cannot turn
	// a crafted URL into --exec and reach arbitrary command execution.
	args = append(args, "--", pageURL)
	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
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
	pipeReport   // total stays 0: the length of a piped page is unknown
	ytdlCmd      *exec.Cmd
	ffmpegCmd    *exec.Cmd
	pipe         io.ReadCloser // ffmpeg stdout (PCM output)
	reader       *bufio.Reader // buffered reader over pipe
	ytdlErr      <-chan error  // yt-dlp exit error from monitoring goroutine
	ffmpegErr    <-chan error  // ffmpeg exit error from monitoring goroutine
	ytdlDone     <-chan struct{}
	ffmpegDone   <-chan struct{}
	pcmBuf       []byte
	f32          bool // true = f32le, false = s16le
	causeChecked bool
	closeOnce    sync.Once
}

func (y *ytdlPipeStreamer) Stream(samples [][2]float64) (int, bool) {
	state := y.state.Load()
	n, ok := streamFromReader(y.reader, samples, &y.pcmBuf, y.f32, state)
	state.pos.Add(int64(n))
	// Check why the pipe closed when PCM hits EOF or ends on a partial read.
	if (!ok || n < len(samples)) && !y.causeChecked && state.err.load() == nil {
		y.causeChecked = true
		if cause := y.waitCause(ytdlStreamCauseGrace); cause != nil {
			state.err.publish(cause)
		}
	}
	return n, ok
}

// waitCause reports why the audio pipe closed without producing audio,
// preferring yt-dlp's reason (bot wall, 404, DRM, region block) over ffmpeg's
// (undecodable input). With d <= 0 it polls without blocking; otherwise it
// waits up to d for a process to report. Returns nil if neither reported an
// error, leaving the caller to surface the bare EOF.
func (y *ytdlPipeStreamer) waitCause(d time.Duration) error {
	ytdlCh := y.ytdlErr
	ffmpegCh := y.ffmpegErr
	if d <= 0 {
		if ytdlCh != nil {
			select {
			case e := <-ytdlCh:
				if e != nil {
					return e
				}
			default:
			}
		}
		if ffmpegCh != nil {
			select {
			case e := <-ffmpegCh:
				return e
			default:
				return nil
			}
		}
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	var ffErr error
	for ytdlCh != nil || ffmpegCh != nil {
		select {
		case e, ok := <-ytdlCh:
			ytdlCh = nil
			if ok && e != nil {
				return e
			}
		case e, ok := <-ffmpegCh:
			ffmpegCh = nil
			if ok {
				ffErr = e
			}
		case <-timer.C:
			return ffErr
		}
	}
	return ffErr
}

func (y *ytdlPipeStreamer) Seek(int) error { return nil }

// interrupt releases a blocked PCM read without waiting for yt-dlp or ffmpeg.
// A stalled download otherwise holds Stream, and the speaker lock with it,
// until yt-dlp gets data or gives up. Close reaps both processes later.
func (y *ytdlPipeStreamer) interrupt() {
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

// monitorExit waits for cmd to exit and reports the result on a buffered
// channel.
func monitorExit(cmd *exec.Cmd, stderr *limitedBuffer, name string) (<-chan error, <-chan struct{}) {
	return monitorExitWithCleanup(cmd, stderr, name, nil)
}

// monitorExitWithCleanup waits for cmd to exit, runs cleanup before closing
// done, and reports the exit error on a buffered channel.
func monitorExitWithCleanup(cmd *exec.Cmd, stderr *limitedBuffer, name string, cleanup func()) (<-chan error, <-chan struct{}) {
	ch := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		err := cmd.Wait()
		if cleanup != nil {
			cleanup()
		}
		switch trimmed := strings.TrimSpace(stderr.String()); {
		case err == nil:
			ch <- nil
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
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		return nil, beep.Format{}, fmt.Errorf("yt-dlp is required — install: %s", ytdlp.InstallHint())
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
	ytdlArgs := []string{
		"-f", ytdlAudioFormat,
		"--no-playlist",
		"--quiet",
		"--no-warnings",
		"--socket-timeout", "15",
		"-o", "-",
	}
	ytdlArgs, cookieCleanup, err := appendYTDLCookieArgs(ytdlArgs, pageURL)
	// The cookie copy outlives this call only once yt-dlp is running.
	ownCookies := true
	defer func() {
		if ownCookies {
			cookieCleanup()
		}
	}()
	if err != nil {
		pr.Close()
		pw.Close()
		return nil, beep.Format{}, fmt.Errorf("yt-dlp cookies: %w", err)
	}
	ytdlArgs = append(ytdlArgs, "--", pageURL)
	ytdlCmd := exec.Command("yt-dlp", ytdlArgs...)
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

	// Monitor each process exit and run cookieCleanup before closing ytdlDone.
	ytdlErrCh, ytdlDone := monitorExitWithCleanup(ytdlCmd, &ytdlStderr, "yt-dlp", cookieCleanup)
	ownCookies = false
	ffmpegErrCh, ffmpegDone := monitorExit(ffmpegCmd, &ffmpegStderr, "ffmpeg")

	format := beep.Format{
		SampleRate:  sr,
		NumChannels: 2,
		Precision:   precision,
	}

	streamer := &ytdlPipeStreamer{
		ytdlCmd:    ytdlCmd,
		ffmpegCmd:  ffmpegCmd,
		pipe:       pcmR,
		reader:     bufio.NewReaderSize(pcmR, pipeBufSize),
		ytdlErr:    ytdlErrCh,
		ffmpegErr:  ffmpegErrCh,
		ytdlDone:   ytdlDone,
		ffmpegDone: ffmpegDone,
		f32:        bitDepth == 32,
	}
	streamer.setState(newPipeStreamState(0))
	return streamer, format, nil
}

// buildYTDLPipeline creates a trackPipeline for a yt-dlp URL.
// If startSec > 0, playback begins at that offset (seek-by-restart).
func (p *Player) buildYTDLPipeline(pageURL string, startSec int) (*trackPipeline, error) {
	return p.buildYTDLPipelineContext(context.Background(), pageURL, startSec)
}

func (p *Player) buildYTDLPipelineContext(ctx context.Context, pageURL string, startSec int) (*trackPipeline, error) {
	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			return nil, errSourceSuperseded
		}
		decoder, format, err := decodeYTDLPipe(pageURL, p.sr, p.bitDepth, startSec)
		if err != nil {
			return nil, err
		}

		if err := prefillYTDLPipeContext(ctx, decoder); err != nil {
			if ctx.Err() != nil {
				return nil, errSourceSuperseded
			}
			if attempt == ytdlPipelineMaxAttempts || !isTransientYTDL403(err) {
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

// buildYTDLSource starts pageURL at 0. A YouTube page plays from its direct
// media URL (see buildYTDLDirectSource). Every other page plays through the
// yt-dlp | ffmpeg chain: a page of another site, and a YouTube page whose
// direct read failed mid-track, so that the reconnect that follows does not
// hit the same failure. When stale reports true, it cancels the setup.
func (p *Player) buildYTDLSource(pageURL string, knownDuration time.Duration, probe bool, stale func() bool) (*trackPipeline, error) {
	if directSeekPage(pageURL) && !p.ytdlDirectFailedFor(pageURL) {
		return p.buildYTDLDirectSource(pageURL, knownDuration, probe, stale)
	}
	tp, err := p.buildYTDLPipeSource(pageURL, knownDuration, probe, stale)
	if err != nil {
		return nil, err
	}
	// Seeks on this page stay on the pipe too.
	tp.ytdlNoDirect = true
	return tp, nil
}

// staleContext returns a context that is cancelled once stale reports true.
// A nil stale never cancels it. The caller must call the CancelFunc.
func staleContext(stale func() bool) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	if stale == nil {
		return ctx, cancel
	}
	go func() {
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if stale() {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, cancel
}

// buildYTDLPipeSource starts the yt-dlp and ffmpeg chain for pageURL at 0.
// When probe is true and knownDuration is 0, it runs a duration probe in parallel.
// When stale reports true, it cancels the setup and the duration probe.
func (p *Player) buildYTDLPipeSource(pageURL string, knownDuration time.Duration, probe bool, stale func() bool) (*trackPipeline, error) {
	if stale != nil && stale() {
		return nil, errSourceSuperseded
	}
	ctx, cancel := staleContext(stale)
	defer cancel()

	probe = probe && knownDuration == 0
	// Probe duration concurrently with pipeline setup so it does not delay playback.
	probeCh := make(chan time.Duration, 1)
	if probe {
		go func() { probeCh <- probeYTDLDuration(ctx, pageURL) }()
	}
	tp, err := p.buildYTDLPipelineContext(ctx, pageURL, 0)
	if err != nil {
		return nil, err
	}
	if probe {
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case d := <-probeCh:
			if d > 0 {
				knownDuration = d
			}
		case <-ctx.Done():
			tp.close()
			return nil, errSourceSuperseded
		case <-timer.C:
		}
	}
	if stale != nil && stale() {
		tp.close()
		return nil, errSourceSuperseded
	}
	tp.knownDuration = knownDuration
	return tp, nil
}

func prefillYTDLPipe(decoder *ytdlPipeStreamer) error {
	return prefillYTDLPipeContext(context.Background(), decoder)
}

func prefillYTDLPipeContext(ctx context.Context, decoder *ytdlPipeStreamer) error {
	stop := func() { decoder.Close() }
	return peekWithTimeoutContext(ctx, decoder.reader, 1, ytdlPipeTimeout, stop, func(err error) error {
		cause := decoder.waitCause(ytdlCauseGrace)
		decoder.Close()
		if cause != nil {
			return cause
		}
		return fmt.Errorf("waiting for audio data: %w", err)
	})
}

func isTransientYTDL403(err error) bool {
	message := err.Error()
	return strings.Contains(message, "yt-dlp:") && strings.Contains(message, "HTTP Error 403: Forbidden")
}
