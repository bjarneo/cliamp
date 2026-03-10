package player

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gopxl/beep/v2"
)

// pcmFrameSize16 is the byte size of one stereo s16le sample frame (2 channels × 2 bytes).
const pcmFrameSize16 = 4

// pcmFrameSize32 is the byte size of one stereo f32le sample frame (2 channels × 4 bytes).
const pcmFrameSize32 = 8

// pcmFrameSize returns the byte size of one stereo sample frame for the given format.
func pcmFrameSize(f32 bool) int {
	if f32 {
		return pcmFrameSize32
	}
	return pcmFrameSize16
}

// decodePCMFrame decodes one stereo sample frame from buf into a [2]float64.
func decodePCMFrame(buf []byte, f32 bool) [2]float64 {
	if f32 {
		return [2]float64{
			float64(math.Float32frombits(binary.LittleEndian.Uint32(buf[0:4]))),
			float64(math.Float32frombits(binary.LittleEndian.Uint32(buf[4:8]))),
		}
	}
	left := int16(binary.LittleEndian.Uint16(buf[0:2]))
	right := int16(binary.LittleEndian.Uint16(buf[2:4]))
	return [2]float64{float64(left) / 32768, float64(right) / 32768}
}

// streamFromReader is the shared Stream() implementation for all pipe-based
// PCM streamers. It reads frames from a buffered reader, decodes them, and
// records the first non-EOF error.
func streamFromReader(reader *bufio.Reader, samples [][2]float64, buf []byte, f32 bool, errp *error) (int, bool) {
	fs := pcmFrameSize(f32)
	n := 0
	for i := range samples {
		_, err := io.ReadFull(reader, buf[:fs])
		if err != nil {
			if err != io.EOF && err != io.ErrUnexpectedEOF {
				*errp = err
			}
			break
		}
		samples[i] = decodePCMFrame(buf[:fs], f32)
		n++
	}
	return n, n > 0
}

// decodeFFmpeg uses ffmpeg to decode any audio file into raw PCM,
// returning a seekable beep.StreamSeekCloser.
// bitDepth selects the output format: 16 (s16le) or 32 (f32le, lossless).
func decodeFFmpeg(path string, sr beep.SampleRate, bitDepth int) (beep.StreamSeekCloser, beep.Format, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		ext := filepath.Ext(path)
		return nil, beep.Format{}, fmt.Errorf("ffmpeg is required to play %s files — install it with your package manager", ext)
	}

	pcmFmt, codec, precision := ffmpegPCMArgs(bitDepth)
	cmd := exec.Command("ffmpeg",
		"-i", path,
		"-f", pcmFmt,
		"-acodec", codec,
		"-ar", strconv.Itoa(int(sr)),
		"-ac", "2",
		"-loglevel", "error",
		"pipe:1",
	)

	out, err := cmd.Output()
	if err != nil {
		return nil, beep.Format{}, fmt.Errorf("ffmpeg decode: %w", err)
	}

	format := beep.Format{
		SampleRate:  sr,
		NumChannels: 2,
		Precision:   precision,
	}

	return &pcmStreamer{data: out, f32: bitDepth == 32}, format, nil
}

// pcmStreamer wraps raw stereo PCM data (s16le or f32le) as a beep.StreamSeekCloser.
type pcmStreamer struct {
	data []byte
	pos  int  // current sample frame index
	f32  bool // true = f32le (32-bit float), false = s16le (16-bit int)
}

func (p *pcmStreamer) Stream(samples [][2]float64) (int, bool) {
	fs := pcmFrameSize(p.f32)
	totalFrames := len(p.data) / fs

	if p.pos >= totalFrames {
		return 0, false
	}

	n := 0
	for i := range samples {
		if p.pos >= totalFrames {
			break
		}
		off := p.pos * fs
		samples[i] = decodePCMFrame(p.data[off:off+fs], p.f32)
		p.pos++
		n++
	}
	return n, true
}

func (p *pcmStreamer) Err() error { return nil }

func (p *pcmStreamer) Len() int {
	return len(p.data) / pcmFrameSize(p.f32)
}

func (p *pcmStreamer) Position() int {
	return p.pos
}

func (p *pcmStreamer) Seek(pos int) error {
	if pos < 0 || pos > p.Len() {
		return fmt.Errorf("seek position %d out of range [0, %d]", pos, p.Len())
	}
	p.pos = pos
	return nil
}

func (p *pcmStreamer) Close() error {
	p.data = nil
	return nil
}

// decodeFFmpegStream starts ffmpeg as a subprocess and streams PCM data
// incrementally from its stdout pipe. Unlike decodeFFmpeg, this does not
// wait for the entire input to be read — suitable for live/infinite streams.
func decodeFFmpegStream(path string, sr beep.SampleRate, bitDepth int) (*ffmpegPipeStreamer, beep.Format, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		ext := filepath.Ext(path)
		return nil, beep.Format{}, fmt.Errorf("ffmpeg is required to play %s files — install it with your package manager", ext)
	}

	pcmFmt, codec, precision := ffmpegPCMArgs(bitDepth)
	cmd := exec.Command("ffmpeg",
		"-i", path,
		"-f", pcmFmt,
		"-acodec", codec,
		"-ar", strconv.Itoa(int(sr)),
		"-ac", "2",
		"-loglevel", "error",
		"pipe:1",
	)

	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, beep.Format{}, fmt.Errorf("ffmpeg stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, beep.Format{}, fmt.Errorf("ffmpeg start: %w", err)
	}

	format := beep.Format{
		SampleRate:  sr,
		NumChannels: 2,
		Precision:   precision,
	}

	return &ffmpegPipeStreamer{cmd: cmd, reader: bufio.NewReaderSize(pipe, 64*1024), pipe: pipe, f32: bitDepth == 32}, format, nil
}

// ffmpegPipeStreamer reads PCM data incrementally from a running ffmpeg process.
type ffmpegPipeStreamer struct {
	cmd    *exec.Cmd
	reader *bufio.Reader
	pipe   io.ReadCloser
	buf    [pcmFrameSize32]byte // large enough for both 16-bit and 32-bit frames
	f32    bool                 // true = f32le, false = s16le
	err    error
	pos    int // current sample frame position
}

func (f *ffmpegPipeStreamer) Stream(samples [][2]float64) (int, bool) {
	n, ok := streamFromReader(f.reader, samples, f.buf[:], f.f32, &f.err)
	f.pos += n
	return n, ok
}

func (f *ffmpegPipeStreamer) Err() error { return f.err }

func (f *ffmpegPipeStreamer) Len() int { return 0 }

func (f *ffmpegPipeStreamer) Position() int { return f.pos }

func (f *ffmpegPipeStreamer) Seek(int) error { return nil }

func (f *ffmpegPipeStreamer) Close() error {
	f.pipe.Close()
	if f.cmd.Process != nil {
		f.cmd.Process.Kill()
	}
	f.cmd.Wait() // ignore error — process was intentionally killed
	return nil
}

// decodeFFmpegLocal starts ffmpeg as a streaming pipe for local files, giving
// instant playback start instead of buffering the entire file to memory.
// Seeking is supported by killing and restarting ffmpeg with a -ss offset.
// Duration is probed via ffprobe so the seek bar works.
func decodeFFmpegLocal(path string, sr beep.SampleRate, bitDepth int) (*localFFmpegStreamer, beep.Format, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		ext := filepath.Ext(path)
		return nil, beep.Format{}, fmt.Errorf("ffmpeg is required to play %s files — install it with your package manager", ext)
	}

	_, _, precision := ffmpegPCMArgs(bitDepth)
	total := probeFrames(path, sr)

	s := &localFFmpegStreamer{path: path, sr: sr, total: total, f32: bitDepth == 32}
	if err := s.start(0); err != nil {
		return nil, beep.Format{}, err
	}

	format := beep.Format{
		SampleRate:  sr,
		NumChannels: 2,
		Precision:   precision,
	}
	return s, format, nil
}

// localFFmpegStreamer streams PCM from a running ffmpeg subprocess for local
// files. Unlike pcmStreamer it does not buffer the entire file — playback
// starts as soon as ffmpeg begins producing output. Seeking kills the current
// process and restarts with -ss (demuxer-level fast seek).
type localFFmpegStreamer struct {
	path   string
	sr     beep.SampleRate
	cmd    *exec.Cmd
	reader *bufio.Reader
	pipe   io.ReadCloser
	buf    [pcmFrameSize32]byte // large enough for both 16-bit and 32-bit frames
	f32    bool                 // true = f32le, false = s16le
	err    error
	pos    int // current sample frame
	total  int // total frames (0 if unknown)
}

// start launches ffmpeg, optionally seeking to seekPos sample frames.
func (s *localFFmpegStreamer) start(seekPos int) error {
	var args []string
	if seekPos > 0 {
		secs := float64(seekPos) / float64(s.sr)
		args = append(args, "-ss", strconv.FormatFloat(secs, 'f', 3, 64))
	}
	pcmFmt, codec, _ := ffmpegPCMArgs(s.bitDepth())
	args = append(args,
		"-i", s.path,
		"-f", pcmFmt,
		"-acodec", codec,
		"-ar", strconv.Itoa(int(s.sr)),
		"-ac", "2",
		"-loglevel", "error",
		"pipe:1",
	)

	cmd := exec.Command("ffmpeg", args...)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("ffmpeg pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ffmpeg start: %w", err)
	}

	s.cmd = cmd
	s.pipe = pipe
	s.reader = bufio.NewReaderSize(pipe, 64*1024)
	s.pos = seekPos
	s.err = nil
	return nil
}

// stop kills the running ffmpeg process and cleans up.
func (s *localFFmpegStreamer) stop() {
	if s.pipe != nil {
		s.pipe.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
		s.cmd.Wait()
	}
}

func (s *localFFmpegStreamer) bitDepth() int {
	if s.f32 {
		return 32
	}
	return 16
}

func (s *localFFmpegStreamer) Stream(samples [][2]float64) (int, bool) {
	n, ok := streamFromReader(s.reader, samples, s.buf[:], s.f32, &s.err)
	s.pos += n
	return n, ok
}

func (s *localFFmpegStreamer) Err() error    { return s.err }
func (s *localFFmpegStreamer) Len() int      { return s.total }
func (s *localFFmpegStreamer) Position() int { return s.pos }

func (s *localFFmpegStreamer) Seek(pos int) error {
	if pos < 0 {
		pos = 0
	}
	if s.total > 0 && pos > s.total {
		pos = s.total
	}
	s.stop()
	return s.start(pos)
}

func (s *localFFmpegStreamer) Close() error {
	s.stop()
	return nil
}

// decodeNavFFmpeg starts ffmpeg with the navBuffer as stdin, returning a
// navFFmpegStreamer that begins producing PCM immediately as bytes arrive.
// Seeking kills ffmpeg, repositions the navBuffer, and restarts ffmpeg from
// the new position — no HTTP reconnect required.
func decodeNavFFmpeg(nb *navBuffer, sr beep.SampleRate, bitDepth int, totalFrames int) (*navFFmpegStreamer, beep.Format, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, beep.Format{}, fmt.Errorf("ffmpeg is required to decode this format — install it with your package manager")
	}
	_, _, precision := ffmpegPCMArgs(bitDepth)
	s := &navFFmpegStreamer{nb: nb, sr: sr, total: totalFrames, f32: bitDepth == 32}
	if err := s.start(0); err != nil {
		return nil, beep.Format{}, err
	}
	format := beep.Format{
		SampleRate:  sr,
		NumChannels: 2,
		Precision:   precision,
	}
	return s, format, nil
}

// navFFmpegStreamer streams PCM from a running ffmpeg subprocess whose stdin
// is a *navBuffer. Playback starts immediately — ffmpeg reads from the buffer
// as bytes arrive from the background download. Seeking kills the current
// ffmpeg process, repositions the navBuffer to the target byte offset, and
// restarts ffmpeg so it reads from that position onwards.
type navFFmpegStreamer struct {
	nb     *navBuffer
	sr     beep.SampleRate
	cmd    *exec.Cmd
	reader *bufio.Reader
	pipe   io.ReadCloser
	buf    [pcmFrameSize32]byte
	f32    bool
	err    error
	pos    int // current sample frame
	total  int // total frames (from track metadata; 0 if unknown)
}

// start launches ffmpeg reading from nb at nb's current position.
func (s *navFFmpegStreamer) start(seekPos int) error {
	pcmFmt, codec, _ := ffmpegPCMArgs(s.bitDepth())
	cmd := exec.Command("ffmpeg",
		"-i", "pipe:0",
		"-f", pcmFmt,
		"-acodec", codec,
		"-ar", strconv.Itoa(int(s.sr)),
		"-ac", "2",
		"-loglevel", "error",
		"pipe:1",
	)
	cmd.Stdin = s.nb

	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("ffmpeg nav pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ffmpeg nav start: %w", err)
	}

	s.cmd = cmd
	s.pipe = pipe
	s.reader = bufio.NewReaderSize(pipe, 64*1024)
	s.pos = seekPos
	s.err = nil
	return nil
}

// stop kills the running ffmpeg process and cleans up.
func (s *navFFmpegStreamer) stop() {
	if s.pipe != nil {
		s.pipe.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
		s.cmd.Wait()
	}
}

func (s *navFFmpegStreamer) bitDepth() int {
	if s.f32 {
		return 32
	}
	return 16
}

func (s *navFFmpegStreamer) Stream(samples [][2]float64) (int, bool) {
	n, ok := streamFromReader(s.reader, samples, s.buf[:], s.f32, &s.err)
	s.pos += n
	return n, ok
}

func (s *navFFmpegStreamer) Err() error    { return s.err }
func (s *navFFmpegStreamer) Len() int      { return s.total }
func (s *navFFmpegStreamer) Position() int { return s.pos }

// Seek repositions playback to the given sample frame. Kills the current
// ffmpeg process, seeks the navBuffer to the proportional byte offset, and
// restarts ffmpeg reading from that position.
func (s *navFFmpegStreamer) Seek(targetFrame int) error {
	if targetFrame < 0 {
		targetFrame = 0
	}
	if s.total > 0 && targetFrame > s.total {
		targetFrame = s.total
	}

	// Compute the byte offset in the navBuffer proportional to the seek position.
	byteOffset := int64(0)
	if s.total > 0 && s.nb.total > 0 {
		ratio := float64(targetFrame) / float64(s.total)
		byteOffset = int64(ratio * float64(s.nb.total))
	}

	s.stop()

	// Reposition the navBuffer to the target byte offset.
	// navBuffer.Seek blocks if the target hasn't downloaded yet.
	if _, err := s.nb.Seek(byteOffset, io.SeekStart); err != nil {
		return fmt.Errorf("nav ffmpeg seek: %w", err)
	}

	return s.start(targetFrame)
}

func (s *navFFmpegStreamer) Close() error {
	s.stop()
	return nil
}

// ffmpegPCMArgs returns the ffmpeg format flag, codec name, and beep precision
// for the given bit depth. 32-bit uses float PCM (f32le) which preserves
// up to 24-bit audio without any truncation; 16-bit uses integer PCM (s16le).
func ffmpegPCMArgs(bitDepth int) (format, codec string, precision int) {
	if bitDepth == 32 {
		return "f32le", "pcm_f32le", 4
	}
	return "s16le", "pcm_s16le", 2
}

// probeFrames uses ffprobe to quickly read file duration from metadata and
// converts it to sample frames. This only reads the container header, so it
// returns almost instantly even for very large files.
func probeFrames(path string, sr beep.SampleRate) int {
	out, err := exec.Command("ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	).Output()
	if err != nil {
		return 0
	}
	secs, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0
	}
	return int(secs * float64(sr))
}
