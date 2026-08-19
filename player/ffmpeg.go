package player

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gopxl/beep/v2"
)

// pcmFrameSize16 is the byte size of one stereo s16le sample frame (2 channels × 2 bytes).
const pcmFrameSize16 = 4

// pcmFrameSize32 is the byte size of one stereo f32le sample frame (2 channels × 4 bytes).
const pcmFrameSize32 = 8

// ffmpegPipeTimeout limits how long URL streams may take to produce initial PCM.
const ffmpegPipeTimeout = 15 * time.Second

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
		// cmd.Output captures stderr into ExitError.Stderr; surface it since
		// ffmpeg writes the actual failure reason there (-loglevel error).
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, beep.Format{}, fmt.Errorf("ffmpeg decode: %w: %s", err, bytes.TrimSpace(ee.Stderr))
		}
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
	fp, format, err := startFFmpegPipe(path, nil, sr, bitDepth)
	if err != nil {
		return nil, beep.Format{}, err
	}
	// live is intentionally left false here: ffmpeg opens the URL itself and
	// manages its own reconnection, and this path is dual-use (live HLS radio
	// and finite HLS/VOD or fallback decodes), so an EOF cannot be assumed to
	// mean a dropped stream. Only decodeFFmpegPipeStream (stdin-fed infinite
	// radio) marks live.
	return &ffmpegPipeStreamer{ffmpegPipe: fp}, format, nil
}

// startFFmpegPipe launches ffmpeg transcoding input to raw PCM on stdout and
// returns an ffmpegPipe reading that stdout. input is the ffmpeg -i argument
// (a URL/path, or "pipe:0" when feeding via stdin); stdin, when non-nil, is
// wired to the process. Callers add the concrete Seek behavior by embedding the
// returned ffmpegPipe in a streamer type.
func startFFmpegPipe(input string, stdin io.ReadCloser, sr beep.SampleRate, bitDepth int) (ffmpegPipe, beep.Format, error) {
	pcmFmt, codec, precision := ffmpegPCMArgs(bitDepth)
	cmd := exec.Command("ffmpeg",
		"-i", input,
		"-f", pcmFmt,
		"-acodec", codec,
		"-ar", strconv.Itoa(int(sr)),
		"-ac", "2",
		"-loglevel", "error",
		"pipe:1",
	)
	cmd.Stdin = stdin

	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return ffmpegPipe{}, beep.Format{}, fmt.Errorf("ffmpeg stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return ffmpegPipe{}, beep.Format{}, fmt.Errorf("ffmpeg start: %w", err)
	}

	fp := ffmpegPipe{cmd: cmd, reader: bufio.NewReaderSize(pipe, pipeBufSize), pipe: pipe, src: stdin, f32: bitDepth == 32}
	format := beep.Format{SampleRate: sr, NumChannels: 2, Precision: precision}
	return fp, format, nil
}

// ffmpegPipe holds the common state and methods shared by all pipe-based
// ffmpeg streamers. Each concrete streamer embeds this and adds its own
// Seek (and optionally start) implementation.
type ffmpegPipe struct {
	cmd    *exec.Cmd
	reader *bufio.Reader
	pipe   io.ReadCloser
	src    io.ReadCloser        // optional stdin source (e.g. ICY-wrapped body); closed on stop
	buf    [pcmFrameSize32]byte // large enough for both 16-bit and 32-bit frames
	f32    bool                 // true = f32le, false = s16le
	live   bool                 // true for infinite radio streams: EOF means the upstream died
	err    error
	pos    int // current sample frame position
	total  int // total frames (0 if unknown/unbounded)
}

func (f *ffmpegPipe) Stream(samples [][2]float64) (int, bool) {
	n, ok := streamFromReader(f.reader, samples, f.buf[:], f.f32, &f.err)
	f.pos += n
	if !ok && f.live && f.err == nil {
		// A live stream never cleanly ends; reaching EOF means the upstream
		// connection dropped (e.g. the stall timeout cancelled it). Surface it
		// so StreamErr()/auto-reconnect fires instead of treating it as
		// end-of-track and stopping playback.
		f.err = io.ErrUnexpectedEOF
	}
	return n, ok
}

func (f *ffmpegPipe) Err() error    { return f.err }
func (f *ffmpegPipe) Len() int      { return f.total }
func (f *ffmpegPipe) Position() int { return f.pos }

// stop kills the running ffmpeg process and cleans up. When stdin is fed from
// src, src is closed first: os/exec runs a goroutine copying src -> ffmpeg
// stdin, and cmd.Wait() blocks until it returns. For an infinite radio stream
// that goroutine is parked in src.Read, so src must be closed to unblock it
// before Wait, otherwise stop hangs.
func (f *ffmpegPipe) stop() {
	src := f.src
	pipe := f.pipe
	cmd := f.cmd
	f.src = nil
	f.pipe = nil
	f.cmd = nil

	if src != nil {
		src.Close()
	}
	if pipe != nil {
		pipe.Close()
	}
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
		cmd.Wait()
	}
}

func (f *ffmpegPipe) Close() error {
	f.stop()
	return nil
}

func (f *ffmpegPipe) bitDepth() int {
	if f.f32 {
		return 32
	}
	return 16
}

// waitForInitialAudio waits until ffmpeg has produced at least one PCM byte.
// This runs before a URL stream is handed to the speaker, so an idle or broken
// live stream cannot park the audio goroutine in Read and block future swaps.
func (f *ffmpegPipe) waitForInitialAudio(timeout time.Duration) error {
	peekErr := make(chan error, 1)
	go func() {
		_, err := f.reader.Peek(1)
		peekErr <- err
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case err := <-peekErr:
		if err != nil {
			f.stop()
			return fmt.Errorf("waiting for audio data: %w", err)
		}
		return nil
	case <-timer.C:
		f.stop()
		<-peekErr // drain after stop unblocks the pipe reader
		return fmt.Errorf("timed out waiting for audio data (%v)", timeout)
	}
}

// ffmpegPipeStreamer reads PCM data incrementally from a running ffmpeg process.
// Used for live/infinite streams where seeking is not supported.
type ffmpegPipeStreamer struct {
	ffmpegPipe
}

func (f *ffmpegPipeStreamer) Seek(int) error { return nil }

// decodeFFmpegPipeStream starts ffmpeg reading from src via stdin (pipe:0)
// instead of letting ffmpeg open the URL itself. Keeping the caller's reader
// chain in the data path means the ICY metadata reader stays attached, so live
// radio StreamTitle parsing keeps working for ffmpeg-only codecs (AAC, AAC+,
// Opus, ...). src is closed when the stream stops. Used for live/infinite HTTP
// streams; seeking is not supported.
func decodeFFmpegPipeStream(src io.ReadCloser, sr beep.SampleRate, bitDepth int) (*ffmpegPipeStreamer, beep.Format, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, beep.Format{}, fmt.Errorf("ffmpeg is required to play this stream — install it with your package manager")
	}
	fp, format, err := startFFmpegPipe("pipe:0", src, sr, bitDepth)
	if err != nil {
		return nil, beep.Format{}, err
	}
	fp.live = true // infinite radio stream: a clean EOF means the upstream died
	return &ffmpegPipeStreamer{ffmpegPipe: fp}, format, nil
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

	s := &localFFmpegStreamer{ffmpegPipe: ffmpegPipe{total: total, f32: bitDepth == 32}, path: path, sr: sr}
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
	ffmpegPipe
	path string
	sr   beep.SampleRate
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
	s.reader = bufio.NewReaderSize(pipe, pipeBufSize)
	s.pos = seekPos
	s.err = nil
	return nil
}

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

// decodeNavFFmpeg starts ffmpeg with the navBuffer as stdin, returning a
// navFFmpegStreamer that begins producing PCM immediately as bytes arrive.
// Seeking kills ffmpeg, repositions the navBuffer, and restarts ffmpeg from
// the new position — no HTTP reconnect required.
func decodeNavFFmpeg(nb *navBuffer, sr beep.SampleRate, bitDepth int, totalFrames int) (*navFFmpegStreamer, beep.Format, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, beep.Format{}, fmt.Errorf("ffmpeg is required to decode this format — install it with your package manager")
	}
	_, _, precision := ffmpegPCMArgs(bitDepth)
	s := &navFFmpegStreamer{ffmpegPipe: ffmpegPipe{total: totalFrames, f32: bitDepth == 32}, nb: nb, sr: sr}
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
	ffmpegPipe
	nb *navBuffer
	sr beep.SampleRate
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
	s.reader = bufio.NewReaderSize(pipe, pipeBufSize)
	s.pos = seekPos
	s.err = nil
	return nil
}

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

// ffmpegPCMArgs returns the ffmpeg format flag, codec name, and beep precision
// for the given bit depth. 32-bit uses float PCM (f32le) which preserves
// up to 24-bit audio without any truncation; 16-bit uses integer PCM (s16le).
func ffmpegPCMArgs(bitDepth int) (format, codec string, precision int) {
	if bitDepth == 32 {
		return "f32le", "pcm_f32le", 4
	}
	return "s16le", "pcm_s16le", 2
}

// ffprobeField runs ffprobe against path requesting a single show_entries
// value (e.g. "stream=sample_rate" or "format=duration") and returns its
// trimmed output. ok is false if ffprobe is missing, the file has no audio
// stream, or the field wasn't present. Shared by probeNativeRate and
// probeFrames, which only differ in which field they read and how they parse it.
// ffprobeTimeout bounds a single ffprobe run. path can be an HTTP(S) URL (a
// Navidrome/Subsonic stream, for instance) as well as a local file, and a
// header read must not be allowed to hang indefinitely against an
// unresponsive server. This matters even for a caller that only waits on an
// async result with its own shorter deadline (see probeNativeRateAsync):
// without a bound here, the ffprobe process itself keeps running past that
// wait — and Player.Preload's synchronous probe (unlike decodeRateFor's,
// which is local-file-only) can be called with a URL directly, with nothing
// else bounding it at all.
const ffprobeTimeout = 5 * time.Second

func ffprobeField(path, showEntries string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), ffprobeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-select_streams", "a:0",
		"-show_entries", showEntries,
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	).Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// probeNativeRate uses ffprobe to read a source's own sample rate, so
// bit-perfect mode can ask ffmpeg to decode without resampling. path may be a
// local file or an HTTP(S) URL — ffprobe handles both — and only reads the
// container header, not the whole file, so it returns quickly either way.
// Returns 0 if the source can't be probed (ffprobe missing, no audio stream,
// unreachable URL, unexpected output).
func probeNativeRate(path string) int {
	v, ok := ffprobeField(path, "stream=sample_rate")
	if !ok {
		return 0
	}
	rate, err := strconv.Atoi(v)
	if err != nil || rate <= 0 {
		return 0
	}
	return rate
}

// probeNativeRateAsync starts probeNativeRate in the background and returns a
// channel that receives its result. Used where the probe must run alongside
// opening a connection rather than block it — see the buffered-URL pipeline
// in pipeline.go, where blocking on ffprobe would delay stream start exactly
// where navBuffer exists to avoid that.
func probeNativeRateAsync(path string) <-chan int {
	ch := make(chan int, 1)
	go func() { ch <- probeNativeRate(path) }()
	return ch
}

// probeNativeBits is probeNativeRate's exact counterpart for bit depth:
// ffprobe only populates bits_per_raw_sample for a lossless/uncompressed
// codec (FLAC, ALAC, WAV) — a lossy codec (AAC, MP3, Opus, a transcoded
// Navidrome/Subsonic stream, ...) has no such property and this correctly
// returns 0 (unverified) for one, same as an unreachable URL or missing
// ffprobe. path may be a local file or an HTTP(S) URL.
func probeNativeBits(path string) int {
	v, ok := ffprobeField(path, "stream=bits_per_raw_sample")
	if !ok {
		return 0
	}
	bits, err := strconv.Atoi(v)
	if err != nil || bits <= 0 {
		return 0
	}
	return bits
}

// probeNativeBitsAsync is probeNativeRateAsync's counterpart for bit depth —
// see that function's doc comment.
func probeNativeBitsAsync(path string) <-chan int {
	ch := make(chan int, 1)
	go func() { ch <- probeNativeBits(path) }()
	return ch
}

// probeFrames uses ffprobe to quickly read file duration from metadata and
// converts it to sample frames. This only reads the container header, so it
// returns almost instantly even for very large files.
func probeFrames(path string, sr beep.SampleRate) int {
	v, ok := ffprobeField(path, "format=duration")
	if !ok {
		return 0
	}
	secs, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0
	}
	return int(secs * float64(sr))
}
