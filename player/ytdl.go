package player

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strconv"

	"github.com/gopxl/beep/v2"
)

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
	err       error
}

func (y *ytdlPipeStreamer) frameSize() int {
	if y.f32 {
		return pcmFrameSize32
	}
	return pcmFrameSize16
}

func (y *ytdlPipeStreamer) Stream(samples [][2]float64) (int, bool) {
	fs := y.frameSize()
	n := 0
	for i := range samples {
		_, err := io.ReadFull(y.reader, y.buf[:fs])
		if err != nil {
			if err != io.EOF && err != io.ErrUnexpectedEOF {
				y.err = err
			}
			// On EOF, check if yt-dlp failed (e.g. invalid URL).
			if n == 0 {
				select {
				case ytErr := <-y.ytdlErr:
					if ytErr != nil {
						y.err = ytErr
					}
				default:
				}
			}
			break
		}
		if y.f32 {
			samples[i][0] = float64(math.Float32frombits(binary.LittleEndian.Uint32(y.buf[0:4])))
			samples[i][1] = float64(math.Float32frombits(binary.LittleEndian.Uint32(y.buf[4:8])))
		} else {
			left := int16(binary.LittleEndian.Uint16(y.buf[0:2]))
			right := int16(binary.LittleEndian.Uint16(y.buf[2:4]))
			samples[i][0] = float64(left) / 32768
			samples[i][1] = float64(right) / 32768
		}
		n++
	}
	return n, n > 0
}

func (y *ytdlPipeStreamer) Err() error     { return y.err }
func (y *ytdlPipeStreamer) Len() int       { return 0 }
func (y *ytdlPipeStreamer) Position() int  { return 0 }
func (y *ytdlPipeStreamer) Seek(int) error { return nil }

func (y *ytdlPipeStreamer) Close() error {
	// Kill both processes to stop downloading/decoding.
	if y.ffmpegCmd.Process != nil {
		y.ffmpegCmd.Process.Kill()
	}
	if y.ytdlCmd.Process != nil {
		y.ytdlCmd.Process.Kill()
	}
	y.pipe.Close()
	// Wait for both to prevent zombie processes.
	y.ffmpegCmd.Wait()
	y.ytdlCmd.Wait()
	// Drain the error channel so the monitor goroutine can exit.
	select {
	case <-y.ytdlErr:
	default:
	}
	return nil
}

// decodeYTDLPipe starts a yt-dlp | ffmpeg pipe chain for the given page URL
// and returns a streaming PCM decoder.
func decodeYTDLPipe(pageURL string, sr beep.SampleRate, bitDepth int) (*ytdlPipeStreamer, beep.Format, error) {
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		return nil, beep.Format{}, fmt.Errorf("yt-dlp is required to play this URL — install it with your package manager")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, beep.Format{}, fmt.Errorf("ffmpeg is required to play this URL — install it with your package manager")
	}

	// os.Pipe connects yt-dlp stdout → ffmpeg stdin.
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, beep.Format{}, fmt.Errorf("os.Pipe: %w", err)
	}

	// Start yt-dlp: download best audio to stdout.
	ytdlCmd := exec.Command("yt-dlp",
		"-f", "bestaudio[protocol=https]/bestaudio[protocol=http]/bestaudio",
		"--no-playlist",
		"-o", "-",
		pageURL,
	)
	ytdlCmd.Stdout = pw
	// Capture stderr for error messages.
	var ytdlStderr bytes.Buffer
	ytdlCmd.Stderr = &ytdlStderr
	if err := ytdlCmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		return nil, beep.Format{}, fmt.Errorf("yt-dlp start: %w", err)
	}

	// Start ffmpeg: read from pipe, output PCM to stdout.
	pcmFmt, codec, precision := ffmpegPCMArgs(bitDepth)
	ffmpegCmd := exec.Command("ffmpeg",
		"-i", "pipe:0",
		"-f", pcmFmt,
		"-acodec", codec,
		"-ar", strconv.Itoa(int(sr)),
		"-ac", "2",
		"-loglevel", "error",
		"pipe:1",
	)
	ffmpegCmd.Stdin = pr
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
		reader:    bufio.NewReaderSize(ffmpegPipe, 64*1024),
		ytdlErr:   ytdlErrCh,
		f32:       bitDepth == 32,
	}, format, nil
}

// buildYTDLPipeline creates a non-seekable trackPipeline for a yt-dlp URL.
func (p *Player) buildYTDLPipeline(pageURL string) (*trackPipeline, error) {
	p.streamTitle.Store("")

	decoder, format, err := decodeYTDLPipe(pageURL, p.sr, p.bitDepth)
	if err != nil {
		return nil, err
	}

	return &trackPipeline{
		decoder:  decoder,
		stream:   decoder,
		format:   format,
		seekable: false,
		path:     pageURL,
	}, nil
}
