package player

import (
	"bufio"
	"bytes"
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
)

// pipeBufSize is the buffer size for audio pipe readers (yt-dlp, ffmpeg).
const pipeBufSize = 64 * 1024

// ytdlPipeTimeout limits how long we wait for yt-dlp to produce initial audio.
const ytdlPipeTimeout = 30 * time.Second

// ytdlCookiesFrom is the browser name for --cookies-from-browser (e.g. "chrome").
// Set via SetYTDLCookiesFrom at startup.
var ytdlCookiesFrom string

// SetYTDLCookiesFrom configures yt-dlp to use cookies from the given browser
// for YouTube Music playback (e.g. "chrome", "firefox", "brave").
func SetYTDLCookiesFrom(browser string) {
	ytdlCookiesFrom = browser
}

// YTDLPAvailable reports whether yt-dlp is installed and on PATH.
func YTDLPAvailable() bool {
	_, err := exec.LookPath("yt-dlp")
	return err == nil
}

// probeYTDLDuration runs a quick yt-dlp --print duration to obtain
// the track duration when --flat-playlist didn't provide it.
func probeYTDLDuration(pageURL string) time.Duration {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	args := []string{"--skip-download", "--no-playlist", "--socket-timeout", "10", "--print", "duration"}
	if ytdlCookiesFrom != "" {
		args = append(args, "--cookies-from-browser", ytdlCookiesFrom)
	}
	args = append(args, pageURL)
	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
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
	ytdlCmd   *exec.Cmd
	ffmpegCmd *exec.Cmd
	pipe      io.ReadCloser // ffmpeg stdout (PCM output)
	reader    *bufio.Reader // buffered reader over pipe
	ytdlErr   chan error    // yt-dlp exit error from monitoring goroutine
	buf       [pcmFrameSize32]byte
	f32       bool // true = f32le, false = s16le
	pos       int  // samples consumed so far
	closeOnce sync.Once
	err       error
}

func (y *ytdlPipeStreamer) Stream(samples [][2]float64) (int, bool) {
	n, ok := streamFromReader(y.reader, samples, y.buf[:], y.f32, &y.err)
	y.pos += n
	// On EOF with no frames read, check if yt-dlp failed (e.g. invalid URL).
	if n == 0 {
		select {
		case ytErr := <-y.ytdlErr:
			if ytErr != nil {
				y.err = ytErr
			}
		default:
		}
	}
	return n, ok
}

func (y *ytdlPipeStreamer) Err() error     { return y.err }
func (y *ytdlPipeStreamer) Len() int       { return 0 }
func (y *ytdlPipeStreamer) Position() int  { return y.pos }
func (y *ytdlPipeStreamer) Seek(int) error { return nil }

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
		// Wait in background to prevent blocking quit/seek.
		go func() {
			y.ffmpegCmd.Wait()
			// Drain error channel so monitor goroutine can exit.
			select {
			case <-y.ytdlErr:
			default:
			}
		}()
	})
	return nil
}

// decodeYTDLPipe starts a yt-dlp | ffmpeg pipe chain for the given page URL
// and returns a streaming PCM decoder. If startSec > 0, ffmpeg -ss is used
// to skip to the desired position in the input stream.
func decodeYTDLPipe(pageURL string, sr beep.SampleRate, bitDepth, startSec int) (*ytdlPipeStreamer, beep.Format, error) {
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		return nil, beep.Format{}, fmt.Errorf("yt-dlp is required — install: %s", YtdlpInstallHint())
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
	ytdlArgs := []string{
		"-f", "bestaudio[protocol=https]/bestaudio[protocol=http]/bestaudio[protocol!=m3u8_native][protocol!=m3u8]/bestaudio",
		"--no-playlist",
		"--no-warnings",
		"--socket-timeout", "15",
		"-o", "-",
	}
	if ytdlCookiesFrom != "" {
		ytdlArgs = append(ytdlArgs, "--cookies-from-browser", ytdlCookiesFrom)
	}
	ytdlArgs = append(ytdlArgs, pageURL)
	ytdlCmd := exec.Command("yt-dlp", ytdlArgs...)
	ytdlCmd.Stdout = pw
	var ytdlStderr bytes.Buffer
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
	var ffmpegStderr bytes.Buffer
	ffmpegCmd.Stderr = &ffmpegStderr
	ffmpegPipe, err := ffmpegCmd.StdoutPipe()
	if err != nil {
		pw.Close()
		pr.Close()
		ytdlCmd.Process.Kill()
		ytdlCmd.Wait()
		return nil, beep.Format{}, fmt.Errorf("ffmpeg stdout pipe: %w", err)
	}
	if err := ffmpegCmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		ytdlCmd.Process.Kill()
		ytdlCmd.Wait()
		return nil, beep.Format{}, fmt.Errorf("ffmpeg start: %w", err)
	}

	// Close parent's copies of pipe ends. yt-dlp owns pw (write end) and
	// ffmpeg owns pr (read end). If the parent keeps these open, EOF won't
	// propagate when the owning process exits.
	pw.Close()
	pr.Close()

	// Monitor yt-dlp exit in a goroutine.
	ytdlErrCh := make(chan error, 1)
	go func() {
		err := ytdlCmd.Wait()
		if err != nil {
			stderr := bytes.TrimSpace(ytdlStderr.Bytes())
			if len(stderr) > 0 {
				ytdlErrCh <- fmt.Errorf("yt-dlp: %s", stderr)
			} else {
				ytdlErrCh <- fmt.Errorf("yt-dlp: %w", err)
			}
		} else {
			ytdlErrCh <- nil
		}
	}()

	format := beep.Format{
		SampleRate:  sr,
		NumChannels: 2,
		Precision:   precision,
	}

	return &ytdlPipeStreamer{
		ytdlCmd:   ytdlCmd,
		ffmpegCmd: ffmpegCmd,
		pipe:      ffmpegPipe,
		reader:    bufio.NewReaderSize(ffmpegPipe, pipeBufSize),
		ytdlErr:   ytdlErrCh,
		f32:       bitDepth == 32,
	}, format, nil
}

// buildYTDLPipeline creates a trackPipeline for a yt-dlp URL.
// If startSec > 0, playback begins at that offset (seek-by-restart).
func (p *Player) buildYTDLPipeline(pageURL string, startSec int) (*trackPipeline, error) {
	p.streamTitle.Store("")

	decoder, format, err := decodeYTDLPipe(pageURL, p.sr, p.bitDepth, startSec)
	if err != nil {
		return nil, err
	}

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
			decoder.Close()
			return nil, fmt.Errorf("waiting for audio data: %w", err)
		}
	case <-time.After(ytdlPipeTimeout):
		decoder.Close()
		<-peekErr // drain goroutine after Close() unblocks the pipe
		return nil, fmt.Errorf("timed out waiting for audio data (%v)", ytdlPipeTimeout)
	}

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
