package player

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gopxl/beep/v2"
)

// pcmFrameSize16 is the byte size of one stereo s16le sample frame (2 channels × 2 bytes).
const pcmFrameSize16 = 4

// pcmFrameSize32 is the byte size of one stereo f32le sample frame (2 channels × 4 bytes).
const pcmFrameSize32 = 8

// ffmpegPipeTimeout limits how long URL streams may take to produce initial PCM.
const ffmpegPipeTimeout = 15 * time.Second

const subprocessStderrLimit = 64 * 1024

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
// PCM streamers. It reads the requested block into reusable storage, decodes
// every complete frame, and records the first non-EOF error.
func streamFromReader(reader *bufio.Reader, samples [][2]float64, bufp *[]byte, f32 bool, state *pipeStreamState) (int, bool) {
	if state.err.load() != nil {
		return 0, false
	}
	if len(samples) == 0 {
		return 0, true
	}
	fs := pcmFrameSize(f32)
	need := len(samples) * fs
	if cap(*bufp) < need {
		*bufp = make([]byte, need)
	} else {
		*bufp = (*bufp)[:need]
	}

	nBytes, err := io.ReadFull(reader, *bufp)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		state.err.publish(err)
	}
	n := nBytes / fs
	for i := range n {
		off := i * fs
		samples[i] = decodePCMFrame((*bufp)[off:off+fs], f32)
	}
	return n, n > 0
}

type errorValue struct {
	err error
}

// firstError publishes one immutable error without locking the audio hot path.
type firstError struct {
	value atomic.Pointer[errorValue]
}

func (e *firstError) publish(err error) {
	if err != nil {
		e.value.CompareAndSwap(nil, &errorValue{err: err})
	}
}

func (e *firstError) load() error {
	if value := e.value.Load(); value != nil {
		return value.err
	}
	return nil
}

type pipeStreamState struct {
	err firstError
	pos atomic.Int64
}

func newPipeStreamState(pos int) *pipeStreamState {
	state := &pipeStreamState{}
	state.pos.Store(int64(pos))
	return state
}

// limitedBuffer captures subprocess stderr without allowing an unbounded
// diagnostic stream to consume memory.
type limitedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	remaining := subprocessStderrLimit - b.buf.Len()
	if remaining > 0 {
		b.buf.Write(p[:min(len(p), remaining)])
	}
	if len(p) > remaining {
		b.truncated = true
	}
	return n, nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.buf.String()
	if b.truncated {
		s += "\n[stderr truncated]"
	}
	return s
}

// ffmpegProcess is the sole owner of Wait for one FFmpeg command. Multiple
// lifecycle paths may wait safely, but os/exec.Cmd.Wait is called exactly once.
type ffmpegProcess struct {
	cmd      *exec.Cmd
	stderr   *limitedBuffer
	waitOnce sync.Once
	err      error
}

func newFFmpegProcess(cmd *exec.Cmd) *ffmpegProcess {
	p := &ffmpegProcess{cmd: cmd, stderr: &limitedBuffer{}}
	cmd.Stderr = p.stderr
	return p
}

func (p *ffmpegProcess) wait() error {
	if p == nil {
		return nil
	}
	p.waitOnce.Do(func() {
		err := p.cmd.Wait()
		if err != nil {
			stderr := strings.TrimSpace(p.stderr.String())
			if stderr != "" {
				p.err = fmt.Errorf("ffmpeg decode: %w: %s", err, stderr)
			} else {
				p.err = fmt.Errorf("ffmpeg decode: %w", err)
			}
		}
	})
	return p.err
}

func (p *ffmpegProcess) kill() {
	if p != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

// decodeFFmpegStream starts ffmpeg as a subprocess and streams PCM data
// incrementally from its stdout pipe without waiting for the entire input.
// It is suitable for live/infinite streams.
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
	proc := newFFmpegProcess(cmd)

	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return ffmpegPipe{}, beep.Format{}, fmt.Errorf("ffmpeg stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		pipe.Close()
		return ffmpegPipe{}, beep.Format{}, fmt.Errorf("ffmpeg start: %w", err)
	}

	fp := ffmpegPipe{
		proc:   proc,
		reader: bufio.NewReaderSize(pipe, pipeBufSize),
		pipe:   pipe,
		input:  stdin,
		state:  newPipeStreamState(0),
		f32:    bitDepth == 32,
	}
	format := beep.Format{SampleRate: sr, NumChannels: 2, Precision: precision}
	return fp, format, nil
}

// ffmpegPipe holds the common state and methods shared by all pipe-based
// ffmpeg streamers. Each concrete streamer embeds this and adds its own
// Seek (and optionally start) implementation.
type ffmpegPipe struct {
	proc   *ffmpegProcess
	reader *bufio.Reader
	pipe   io.ReadCloser
	input  io.Closer // optional stdin source or owned stdin pump; interrupted before Wait
	pcmBuf []byte    // reusable block buffer for decoded PCM bytes
	state  *pipeStreamState
	f32    bool // true = f32le, false = s16le
	live   bool // true for infinite radio streams: EOF means the upstream died
	total  int  // total frames (0 if unknown/unbounded)
}

func (f *ffmpegPipe) Stream(samples [][2]float64) (int, bool) {
	n, ok := streamFromReader(f.reader, samples, &f.pcmBuf, f.f32, f.state)
	f.state.pos.Add(int64(n))
	if !ok && f.proc != nil {
		if f.input != nil {
			_ = f.input.Close()
		}
		if err := f.proc.wait(); err != nil {
			f.state.err.publish(err)
		}
	}
	if !ok && f.live && f.state.err.load() == nil {
		// A live stream never cleanly ends; reaching EOF means the upstream
		// connection dropped (e.g. the stall timeout cancelled it). Surface it
		// so StreamErr()/auto-reconnect fires instead of treating it as
		// end-of-track and stopping playback.
		f.state.err.publish(io.ErrUnexpectedEOF)
	}
	return n, ok
}

func (f *ffmpegPipe) Err() error {
	if f.state == nil {
		return nil
	}
	return f.state.err.load()
}
func (f *ffmpegPipe) Len() int { return f.total }
func (f *ffmpegPipe) Position() int {
	if f.state == nil {
		return 0
	}
	return int(f.state.pos.Load())
}

// interrupt releases any blocked PCM or stdin read without waiting for FFmpeg.
func (f *ffmpegPipe) interrupt() {
	if f.input != nil {
		_ = f.input.Close()
	}
	if f.pipe != nil {
		_ = f.pipe.Close()
	}
	if f.proc != nil {
		f.proc.kill()
	}
}

// stop interrupts input before killing and reaping FFmpeg. This ordering is
// required because os/exec may otherwise wait for its stdin copy to finish.
func (f *ffmpegPipe) stop() error {
	f.interrupt()
	if f.proc == nil {
		return nil
	}
	return f.proc.wait()
}

func (f *ffmpegPipe) Close() error {
	_ = f.stop()
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
	return f.waitForAudioBytes(1, timeout)
}

func (f *ffmpegPipe) waitForAudioBytes(n int, timeout time.Duration) error {
	peekErr := make(chan error, 1)
	go func() {
		_, err := f.reader.Peek(n)
		peekErr <- err
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case err := <-peekErr:
		if err != nil {
			if f.input != nil {
				_ = f.input.Close()
			}
			if waitErr := f.proc.wait(); waitErr != nil {
				return waitErr
			}
			return fmt.Errorf("waiting for audio data: %w", err)
		}
		return nil
	case <-timer.C:
		_ = f.stop()
		<-peekErr // drain after stop unblocks the pipe reader
		return fmt.Errorf("timed out waiting for audio data (%v)", timeout)
	}
}

// ffmpegPipeStreamer reads PCM data incrementally from a running ffmpeg process.
// Used for non-seekable HTTP streams, both finite and live.
type ffmpegPipeStreamer struct {
	ffmpegPipe
}

func (f *ffmpegPipeStreamer) Seek(int) error { return nil }

// decodeFFmpegPipeStream starts ffmpeg reading from src via stdin (pipe:0)
// instead of letting ffmpeg open the URL itself. Keeping the caller's reader
// chain in the data path means the ICY metadata reader stays attached, so live
// radio StreamTitle parsing keeps working for ffmpeg-only codecs (AAC, AAC+,
// Opus, ...). src is closed when the stream stops; seeking is not supported.
func decodeFFmpegPipeStream(src io.ReadCloser, sr beep.SampleRate, bitDepth int, live bool) (*ffmpegPipeStreamer, beep.Format, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, beep.Format{}, fmt.Errorf("ffmpeg is required to play this stream — install it with your package manager")
	}
	fp, format, err := startFFmpegPipe("pipe:0", src, sr, bitDepth)
	if err != nil {
		return nil, beep.Format{}, err
	}
	fp.live = live
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
	fp, err := s.startPipe(0)
	if err != nil {
		return nil, beep.Format{}, err
	}
	s.ffmpegPipe = fp

	format := beep.Format{
		SampleRate:  sr,
		NumChannels: 2,
		Precision:   precision,
	}
	return s, format, nil
}

// localFFmpegStreamer streams PCM from a running ffmpeg subprocess for local
// files. Playback starts as soon as ffmpeg produces output. Seeking kills the
// current process and restarts with -ss (demuxer-level fast seek).
type localFFmpegStreamer struct {
	ffmpegPipe
	path string
	sr   beep.SampleRate
}

func (s *localFFmpegStreamer) startPipe(seekPos int) (ffmpegPipe, error) {
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
	proc := newFFmpegProcess(cmd)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return ffmpegPipe{}, fmt.Errorf("ffmpeg pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		pipe.Close()
		return ffmpegPipe{}, fmt.Errorf("ffmpeg start: %w", err)
	}

	fp := ffmpegPipe{
		proc:   proc,
		reader: bufio.NewReaderSize(pipe, pipeBufSize),
		pipe:   pipe,
		state:  newPipeStreamState(seekPos),
		f32:    s.f32,
		total:  s.total,
	}
	if err := fp.waitForAudioBytes(pcmFrameSize(fp.f32), ffmpegPipeTimeout); err != nil {
		_ = fp.stop()
		return ffmpegPipe{}, err
	}
	return fp, nil
}

func (s *localFFmpegStreamer) Stream(samples [][2]float64) (int, bool) {
	n, ok := s.ffmpegPipe.Stream(samples)
	if !ok {
		if s.total == 0 {
			s.total = s.Position()
		}
	}
	return n, ok
}

func (s *localFFmpegStreamer) Seek(pos int) error {
	prepared, err := s.prepareSeek(pos)
	if err != nil {
		return err
	}
	s.applyPreparedSeek(prepared)
	return nil
}

type preparedFFmpegSeek struct {
	expected    *pipeStreamState
	replacement ffmpegPipe
}

func (p *preparedFFmpegSeek) close() error {
	return p.replacement.stop()
}

func (f *ffmpegPipe) seekMatches(prepared *preparedFFmpegSeek) bool {
	return prepared != nil && f.state == prepared.expected
}

func (f *ffmpegPipe) commitPreparedSeek(prepared *preparedFFmpegSeek) (ffmpegPipe, bool) {
	if !f.seekMatches(prepared) {
		return ffmpegPipe{}, false
	}
	old := *f
	*f = prepared.replacement
	prepared.replacement = ffmpegPipe{}
	return old, true
}

func (f *ffmpegPipe) applyPreparedSeek(prepared *preparedFFmpegSeek) {
	f.interrupt()
	old, ok := f.commitPreparedSeek(prepared)
	if !ok {
		_ = prepared.close()
		return
	}
	_ = old.stop()
}

func (s *localFFmpegStreamer) prepareSeek(pos int) (*preparedFFmpegSeek, error) {
	pos = clampSeekPosition(pos, s.total)
	replacement, err := s.startPipe(pos)
	if err != nil {
		return nil, err
	}
	return &preparedFFmpegSeek{expected: s.state, replacement: replacement}, nil
}

// decodeNavFFmpeg starts ffmpeg from a per-process navBuffer reader, returning a
// navFFmpegStreamer that begins producing PCM immediately as bytes arrive.
// Seeking kills ffmpeg and restarts decoding from byte zero with an FFmpeg time
// offset, so no HTTP reconnect is required.
// ffmpegAvailable reports whether ffmpeg is on PATH, caching the lookup. The
// buffered pipeline decodes through it, so a source can only be routed there
// when it is installed.
var ffmpegAvailable = sync.OnceValue(func() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
})

func decodeNavFFmpeg(nb *navBuffer, sr beep.SampleRate, bitDepth int, totalFrames int) (*navFFmpegStreamer, beep.Format, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, beep.Format{}, fmt.Errorf("ffmpeg is required to decode this format — install it with your package manager")
	}
	_, _, precision := ffmpegPCMArgs(bitDepth)
	s := &navFFmpegStreamer{ffmpegPipe: ffmpegPipe{total: totalFrames, f32: bitDepth == 32}, nb: nb, sr: sr}
	fp, err := s.startPipe(0, false)
	if err != nil {
		return nil, beep.Format{}, err
	}
	s.ffmpegPipe = fp
	// The real length is only knowable once every byte has arrived, so measure
	// it in the background rather than delaying playback for it.
	go s.probeDownloadedLength()
	format := beep.Format{
		SampleRate:  sr,
		NumChannels: 2,
		Precision:   precision,
	}
	return s, format, nil
}

// navFFmpegStreamer streams PCM from a running ffmpeg subprocess whose stdin
// is fed by a cancellable navBuffer reader. Playback starts immediately as
// bytes arrive from the background download.
type navFFmpegStreamer struct {
	ffmpegPipe
	nb *navBuffer
	sr beep.SampleRate

	// probed is the frame count measured from the downloaded bytes, or 0
	// before the probe finishes. It is written by the probe goroutine and read
	// by the UI and audio goroutines, so it must stay atomic.
	probed atomic.Int64
}

// Len returns the measured frame count once the download has been probed,
// falling back to the metadata hint until then.
//
// Podcast feeds routinely understate an episode's length, because
// itunes:duration describes the master and the file served carries inserted
// advertising on top of it. Measuring the bytes is the only way to get a seek
// bar and an end-of-track that match what is playing.
func (s *navFFmpegStreamer) Len() int {
	if n := s.probed.Load(); n > 0 {
		return int(n)
	}
	return s.ffmpegPipe.Len()
}

// probeDownloadedLength measures the finished download and stores the result.
// A failed probe leaves the metadata hint in place.
func (s *navFFmpegStreamer) probeDownloadedLength() {
	path, ok := s.nb.completedPath()
	if !ok {
		return
	}
	if frames := probeFrames(path, s.sr); frames > 0 {
		s.probed.Store(int64(frames))
	}
}

type navFFmpegInput struct {
	reader    *navReader
	stdin     io.WriteCloser
	done      chan struct{}
	closeOnce sync.Once
}

func startNavFFmpegInput(reader *navReader, stdin io.WriteCloser) *navFFmpegInput {
	in := &navFFmpegInput{reader: reader, stdin: stdin, done: make(chan struct{})}
	go func() {
		_, _ = io.Copy(stdin, reader)
		_ = stdin.Close()
		close(in.done)
	}()
	return in
}

func (in *navFFmpegInput) Close() error {
	in.closeOnce.Do(func() {
		_ = in.reader.Close()
		_ = in.stdin.Close()
		<-in.done
	})
	return nil
}

func (s *navFFmpegStreamer) startPipe(seekPos int, validate bool) (ffmpegPipe, error) {
	pcmFmt, codec, _ := ffmpegPCMArgs(s.bitDepth())
	args := []string{"-i", "pipe:0"}
	if seekPos > 0 {
		secs := float64(seekPos) / float64(s.sr)
		// Output-side seeking decodes from the start of the progressive input,
		// preserving container headers and giving sample-accurate VBR seeks.
		args = append(args, "-ss", strconv.FormatFloat(secs, 'f', 3, 64))
	}
	args = append(args,
		"-f", pcmFmt,
		"-acodec", codec,
		"-ar", strconv.Itoa(int(s.sr)),
		"-ac", "2",
		"-loglevel", "error",
		"pipe:1",
	)
	cmd := exec.Command("ffmpeg", args...)
	proc := newFFmpegProcess(cmd)

	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return ffmpegPipe{}, fmt.Errorf("ffmpeg nav pipe: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		pipe.Close()
		return ffmpegPipe{}, fmt.Errorf("ffmpeg nav stdin: %w", err)
	}
	if err := cmd.Start(); err != nil {
		stdin.Close()
		pipe.Close()
		return ffmpegPipe{}, fmt.Errorf("ffmpeg nav start: %w", err)
	}

	fp := ffmpegPipe{
		proc:   proc,
		pipe:   pipe,
		reader: bufio.NewReaderSize(pipe, pipeBufSize),
		input:  startNavFFmpegInput(s.nb.newReader(), stdin),
		state:  newPipeStreamState(seekPos),
		f32:    s.f32,
		total:  s.total,
	}
	if validate {
		if err := fp.waitForAudioBytes(pcmFrameSize(fp.f32), ffmpegPipeTimeout); err != nil {
			_ = fp.stop()
			return ffmpegPipe{}, err
		}
	}
	return fp, nil
}

// Seek repositions playback to the given sample frame. Kills the current
// ffmpeg process and restarts decoding from byte zero with a time offset.
func (s *navFFmpegStreamer) Seek(targetFrame int) error {
	prepared, err := s.prepareSeek(targetFrame)
	if err != nil {
		return err
	}
	// The old reader is interrupted only after the replacement has produced
	// PCM. The shared navBuffer remains open for the replacement.
	s.applyPreparedSeek(prepared)
	return nil
}

func (s *navFFmpegStreamer) prepareSeek(pos int) (*preparedFFmpegSeek, error) {
	pos = clampSeekPosition(pos, s.Len())
	replacement, err := s.startPipe(pos, true)
	if err != nil {
		return nil, err
	}
	return &preparedFFmpegSeek{expected: s.state, replacement: replacement}, nil
}

func (s *navFFmpegStreamer) Close() error {
	if s.input != nil {
		_ = s.input.Close()
	}
	// Final close cancels the progressive read before FFmpeg is reaped.
	err := s.nb.Close()
	_ = s.stop()
	return err
}

func clampSeekPosition(pos, total int) int {
	if pos < 0 {
		return 0
	}
	if total > 0 && pos > total {
		return total
	}
	return pos
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
