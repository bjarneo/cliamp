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
	"os"
	"os/exec"
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

// ffmpegExitGrace bounds how long Stream waits for a background monitor
// goroutine to report an FFmpeg exit status after stdout reaches EOF.
const ffmpegExitGrace = 50 * time.Millisecond

const subprocessStderrLimit = 64 * 1024

// pcmFrameSize returns the byte size of one stereo sample frame for the given format.
func pcmFrameSize(f32 bool) int {
	if f32 {
		return pcmFrameSize32
	}
	return pcmFrameSize16
}

func decodePCMFloat32(bits uint32) float64 {
	// Reject IEEE 754 NaN and Inf exponent bits.
	if bits&0x7f800000 == 0x7f800000 {
		return 0
	}
	return float64(math.Float32frombits(bits))
}

// decodePCMFrame decodes one stereo sample frame from buf into a [2]float64.
func decodePCMFrame(buf []byte, f32 bool) [2]float64 {
	if f32 {
		return [2]float64{
			decodePCMFloat32(binary.LittleEndian.Uint32(buf[0:4])),
			decodePCMFloat32(binary.LittleEndian.Uint32(buf[4:8])),
		}
	}
	left := int16(binary.LittleEndian.Uint16(buf[0:2]))
	right := int16(binary.LittleEndian.Uint16(buf[2:4]))
	return [2]float64{float64(left) / 32768, float64(right) / 32768}
}

// streamFromReader is the shared Stream implementation for pipe PCM streamers.
// It reads one block into reusable storage, decodes every complete frame, and
// records the first non-EOF error.
func streamFromReader(reader *bufio.Reader, samples [][2]float64, bufp *[]byte, f32 bool, state *pipeStreamState) (int, bool) {
	if state.err.load() != nil {
		return 0, false
	}
	if len(samples) == 0 {
		return 0, true
	}
	fs := pcmFrameSize(f32)
	need := len(samples) * fs
	buf := *bufp
	if cap(buf) < need {
		buf = make([]byte, need)
	} else {
		buf = buf[:need]
	}
	*bufp = buf

	nBytes, err := io.ReadFull(reader, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		state.err.publish(err)
	}
	n := nBytes / fs
	if n == 0 {
		return 0, false
	}
	dst := samples[:n]
	if f32 {
		raw := buf[:n*pcmFrameSize32]
		for i := range dst {
			off := i * pcmFrameSize32
			v := binary.LittleEndian.Uint64(raw[off : off+pcmFrameSize32])
			dst[i] = [2]float64{
				decodePCMFloat32(uint32(v)),
				decodePCMFloat32(uint32(v >> 32)),
			}
		}
		return n, true
	}
	const invInt16 = 1.0 / 32768.0
	raw := buf[:n*pcmFrameSize16]
	for i := range dst {
		off := i * pcmFrameSize16
		v := binary.LittleEndian.Uint32(raw[off : off+pcmFrameSize16])
		dst[i] = [2]float64{
			float64(int16(v)) * invInt16,
			float64(int16(v>>16)) * invInt16,
		}
	}
	return n, true
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

// atomicClock marks a decoder whose Position and Len methods are safe without
// the speaker lock.
type atomicClock interface {
	atomicClock()
}

// atomicErr marks a decoder whose Err method is safe without the speaker lock.
type atomicErr interface {
	atomicErr()
}

// pipeReport answers Err, Len and Position for a pipe-based streamer from
// the state that its Stream updates. ffmpegPipe and ytdlPipeStreamer embed it.
type pipeReport struct {
	state atomic.Pointer[pipeStreamState]
	total atomic.Int64 // total frames, or 0 when unknown
}

func (*pipeReport) atomicClock() {}
func (*pipeReport) atomicErr()   {}

var (
	_ atomicClock = (*ffmpegPipe)(nil)
	_ atomicErr   = (*ffmpegPipe)(nil)
	_ atomicClock = (*ytdlPipeStreamer)(nil)
	_ atomicErr   = (*ytdlPipeStreamer)(nil)
)

func (r *pipeReport) setState(state *pipeStreamState) {
	r.state.Store(state)
}

func (r *pipeReport) setTotal(total int) {
	r.total.Store(int64(total))
}

func (r *pipeReport) Err() error {
	st := r.state.Load()
	if st == nil {
		return nil
	}
	return st.err.load()
}

func (r *pipeReport) Len() int { return int(r.total.Load()) }

func (r *pipeReport) Position() int {
	st := r.state.Load()
	if st == nil {
		return 0
	}
	return int(st.pos.Load())
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

// ffmpegProcess owns Wait for one FFmpeg command and reaps its exit status in
// a background monitor goroutine.
type ffmpegProcess struct {
	cmd       *exec.Cmd
	stderr    *limitedBuffer
	startOnce sync.Once
	monitored atomic.Bool
	done      chan struct{}
	err       error
}

func newFFmpegProcess(cmd *exec.Cmd) *ffmpegProcess {
	p := &ffmpegProcess{
		cmd:    cmd,
		stderr: &limitedBuffer{},
		done:   make(chan struct{}),
	}
	cmd.Stderr = p.stderr
	return p
}

// startMonitor launches the single goroutine that reaps the FFmpeg process.
func (p *ffmpegProcess) startMonitor() {
	if p == nil {
		return
	}
	p.startOnce.Do(func() {
		p.monitored.Store(true)
		go func() {
			defer close(p.done)
			err := p.cmd.Wait()
			if err != nil {
				stderr := strings.TrimSpace(p.stderr.String())
				if stderr != "" {
					p.err = fmt.Errorf("ffmpeg decode: %w: %s", err, stderr)
				} else {
					p.err = fmt.Errorf("ffmpeg decode: %w", err)
				}
			}
		}()
	})
}

func (p *ffmpegProcess) wait() error {
	if p == nil {
		return nil
	}
	p.startMonitor()
	<-p.done
	return p.err
}

// waitErr polls the background monitor for up to grace without blocking
// indefinitely under the speaker lock.
func (p *ffmpegProcess) waitErr(grace time.Duration) error {
	if p == nil || !p.monitored.Load() {
		return nil
	}
	select {
	case <-p.done:
		return p.err
	default:
	}
	if grace <= 0 {
		return nil
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-p.done:
		return p.err
	case <-timer.C:
		return nil
	}
}

func (p *ffmpegProcess) kill() {
	if p != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

// pipeInput pumps an input stream into a subprocess stdin pipe in the
// background so interrupt does not block on the copy goroutine.
type pipeInput struct {
	src           io.ReadCloser
	dst           io.WriteCloser
	done          chan struct{}
	interrupted   atomic.Bool
	readErr       firstError
	interruptOnce sync.Once
	closeOnce     sync.Once
}

func startPipeInput(src io.ReadCloser, dst io.WriteCloser) *pipeInput {
	in := &pipeInput{src: src, dst: dst, done: make(chan struct{})}
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				if _, werr := dst.Write(buf[:n]); werr != nil {
					break
				}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) && !in.interrupted.Load() {
					in.readErr.publish(err)
				}
				break
			}
		}
		_ = dst.Close()
		close(in.done)
	}()
	return in
}

func (in *pipeInput) err() error {
	if in == nil {
		return nil
	}
	return in.readErr.load()
}

func (in *pipeInput) interrupt() {
	in.interruptOnce.Do(func() {
		in.interrupted.Store(true)
		_ = in.src.Close()
		_ = in.dst.Close()
	})
}

func (in *pipeInput) Close() error {
	in.closeOnce.Do(func() {
		in.interrupt()
		<-in.done
	})
	return nil
}

// decodeFFmpegStream starts ffmpeg as a subprocess and streams PCM data
// incrementally from its stdout pipe without waiting for the entire input.
// It is suitable for live/infinite streams.
func decodeFFmpegStream(path string, sr beep.SampleRate, bitDepth int) (*ffmpegPipeStreamer, beep.Format, error) {
	if err := requireFFmpeg(); err != nil {
		return nil, beep.Format{}, err
	}
	s := &ffmpegPipeStreamer{}
	format, err := initFFmpegPipe(&s.ffmpegPipe, path, nil, sr, bitDepth)
	if err != nil {
		return nil, beep.Format{}, err
	}
	// live stays false because ffmpeg opens the URL itself and handles both
	// live HLS radio and finite fallback decodes.
	return s, format, nil
}

// initFFmpegPipe starts ffmpeg transcoding input to raw PCM on stdout and
// initializes fp. When stdin is non-nil, an OS pipe feeds ffmpeg stdin via a
// background pump so cmd.Wait never blocks on stdin reads.
func initFFmpegPipe(fp *ffmpegPipe, input string, stdin io.ReadCloser, sr beep.SampleRate, bitDepth int) (beep.Format, error) {
	return initFFmpegPipeArgs(fp, []string{"-i", input}, stdin, sr, bitDepth)
}

// initFFmpegPipeArgs is initFFmpegPipe with the full input arguments, for
// callers that set input options such as -ss or request headers before -i.
func initFFmpegPipeArgs(fp *ffmpegPipe, inputArgs []string, stdin io.ReadCloser, sr beep.SampleRate, bitDepth int) (beep.Format, error) {
	_, _, precision := ffmpegPCMArgs(bitDepth)
	args := append(append([]string{}, inputArgs...), pcmOutputArgs(sr, bitDepth)...)
	cmd := exec.Command("ffmpeg", args...)
	proc := newFFmpegProcess(cmd)

	pcmR, pcmW, err := os.Pipe()
	if err != nil {
		return beep.Format{}, fmt.Errorf("ffmpeg stdout pipe: %w", err)
	}
	cmd.Stdout = pcmW

	var inR, inW *os.File
	if stdin != nil {
		inR, inW, err = os.Pipe()
		if err != nil {
			_ = pcmR.Close()
			_ = pcmW.Close()
			return beep.Format{}, fmt.Errorf("ffmpeg stdin pipe: %w", err)
		}
		cmd.Stdin = inR
	}

	if err := cmd.Start(); err != nil {
		_ = pcmR.Close()
		_ = pcmW.Close()
		if inR != nil {
			_ = inR.Close()
			_ = inW.Close()
		}
		return beep.Format{}, fmt.Errorf("ffmpeg start: %w", err)
	}
	_ = pcmW.Close()
	if inR != nil {
		_ = inR.Close()
	}
	proc.startMonitor()

	fp.setState(newPipeStreamState(0))
	fp.proc = proc
	fp.reader = bufio.NewReaderSize(pcmR, pipeBufSize)
	fp.pipe = pcmR
	if stdin != nil {
		fp.input = startPipeInput(stdin, inW)
	}
	fp.f32 = bitDepth == 32
	format := beep.Format{SampleRate: sr, NumChannels: 2, Precision: precision}
	return format, nil
}

// ffmpegPipe holds the common state and methods shared by all pipe-based
// ffmpeg streamers. Each concrete streamer embeds this and adds its own
// Seek (and optionally start) implementation.
type ffmpegPipe struct {
	pipeReport
	proc   *ffmpegProcess
	reader *bufio.Reader
	pipe   io.ReadCloser
	input  io.Closer // optional stdin source or owned stdin pump
	pcmBuf []byte    // reusable block buffer for decoded PCM bytes
	f32    bool      // true = f32le, false = s16le
	live   bool      // true for infinite radio streams: EOF means the upstream died
	// minFrames, when above zero, is the audio a finite input must produce.
	// ffmpeg exits 0 when an HTTP input ends early and its reconnect fails, so
	// an EOF before minFrames means the input was cut short.
	minFrames int64
}

func (f *ffmpegPipe) Stream(samples [][2]float64) (int, bool) {
	state := f.state.Load()
	n, ok := streamFromReader(f.reader, samples, &f.pcmBuf, f.f32, state)
	state.pos.Add(int64(n))
	if !ok || n < len(samples) {
		if f.proc != nil && state.err.load() == nil {
			if err := f.proc.waitErr(ffmpegExitGrace); err != nil {
				state.err.publish(err)
			}
		}
		if f.input != nil && state.err.load() == nil {
			if in, ok := f.input.(interface{ err() error }); ok {
				if err := in.err(); err != nil {
					state.err.publish(err)
				}
			}
		}
		if f.live && state.err.load() == nil {
			// A live stream never ends cleanly, so EOF indicates a dropped connection.
			state.err.publish(io.ErrUnexpectedEOF)
		}
	}
	if !ok && state.pos.Load() < f.minFrames && state.err.load() == nil {
		state.err.publish(io.ErrUnexpectedEOF)
	}
	return n, ok
}

// interrupt stops FFmpeg and closes pipes without waiting for background goroutines.
func (f *ffmpegPipe) interrupt() {
	if f.proc != nil {
		f.proc.kill()
	}
	if f.pipe != nil {
		_ = f.pipe.Close()
	}
	if f.input != nil {
		if in, ok := f.input.(interface{ interrupt() }); ok {
			in.interrupt()
		} else {
			_ = f.input.Close()
		}
	}
}

// stop kills FFmpeg and closes stdout before waiting for process exit and
// joining any stdin pump goroutine.
func (f *ffmpegPipe) stop() error {
	f.interrupt()
	var waitErr error
	if f.proc != nil {
		waitErr = f.proc.wait()
	}
	if f.input != nil {
		_ = f.input.Close()
	}
	return waitErr
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
	return f.waitForAudioBytesContext(context.Background(), n, timeout)
}

// waitForAudioBytesContext works like waitForAudioBytes and stops when ctx
// cancels.
func (f *ffmpegPipe) waitForAudioBytesContext(ctx context.Context, n int, timeout time.Duration) error {
	stop := func() { _ = f.stop() }
	return peekWithTimeoutContext(ctx, f.reader, n, timeout, stop, func(err error) error {
		if f.input != nil {
			if in, ok := f.input.(interface{ interrupt() }); ok {
				in.interrupt()
			}
		}
		waitErr := f.proc.wait()
		_ = f.stop()
		if waitErr != nil {
			return waitErr
		}
		return fmt.Errorf("waiting for audio data: %w", err)
	})
}

// peekWithTimeout waits until reader holds n bytes. When timeout passes
// first, it calls stop, which must unblock the read, and returns a timeout
// error. When the pipe closes first, it returns closed for the read error,
// so the caller can report why its process ended.
func peekWithTimeout(reader *bufio.Reader, n int, timeout time.Duration, stop func(), closed func(error) error) error {
	return peekWithTimeoutContext(context.Background(), reader, n, timeout, stop, closed)
}

// peekWithTimeoutContext works like peekWithTimeout and stops when ctx cancels.
func peekWithTimeoutContext(ctx context.Context, reader *bufio.Reader, n int, timeout time.Duration, stop func(), closed func(error) error) error {
	peekErr := make(chan error, 1)
	go func() {
		_, err := reader.Peek(n)
		peekErr <- err
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case err := <-peekErr:
		if err != nil {
			return closed(err)
		}
		return nil
	case <-ctx.Done():
		stop()
		<-peekErr
		return ctx.Err()
	case <-timer.C:
		stop()
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
	if err := requireFFmpeg(); err != nil {
		return nil, beep.Format{}, err
	}
	s := &ffmpegPipeStreamer{}
	format, err := initFFmpegPipe(&s.ffmpegPipe, "pipe:0", src, sr, bitDepth)
	if err != nil {
		return nil, beep.Format{}, err
	}
	s.live = live
	return s, format, nil
}

// DecodeFFmpegPipeStream decodes src through ffmpeg for the codecs without a
// native decoder (AAC, Opus, M4A, ...). It is the exported counterpart of the
// URL pipeline for external custom streamers (provider.CustomStreamer): src
// yields audio bytes on Read and is closed when the stream stops. Seeking is
// not supported: Seek is a no-op, like the URL path.
func DecodeFFmpegPipeStream(src io.ReadCloser) (beep.StreamSeekCloser, beep.Format, error) {
	return decodeFFmpegPipeStream(src, beep.SampleRate(44100), 16, false)
}

// decodeFFmpegLocal starts ffmpeg as a streaming pipe for local files, giving
// instant playback start instead of buffering the entire file to memory.
// Seeking is supported by killing and restarting ffmpeg with a -ss offset.
// Duration is probed via ffprobe so the seek bar works.
func decodeFFmpegLocal(path string, sr beep.SampleRate, bitDepth int) (*localFFmpegStreamer, beep.Format, error) {
	if err := requireFFmpeg(); err != nil {
		return nil, beep.Format{}, err
	}

	_, _, precision := ffmpegPCMArgs(bitDepth)
	total := probeFrames(path, sr)

	s := &localFFmpegStreamer{ffmpegPipe: ffmpegPipe{f32: bitDepth == 32}, path: path, sr: sr}
	s.setTotal(total)
	fp, err := s.startPipe(0)
	if err != nil {
		return nil, beep.Format{}, err
	}
	s.swapFrom(fp)

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
	// owner, when set, owns the file at path. A finished navBuffer download
	// is the owner of its temporary file.
	owner io.Closer
}

// Close stops ffmpeg, then closes the owner of the input file.
func (s *localFFmpegStreamer) Close() error {
	_ = s.stop()
	if s.owner != nil {
		return s.owner.Close()
	}
	return nil
}

func (s *localFFmpegStreamer) startPipe(seekPos int) (*ffmpegPipe, error) {
	var args []string
	if seekPos > 0 {
		secs := float64(seekPos) / float64(s.sr)
		args = append(args, "-ss", strconv.FormatFloat(secs, 'f', 3, 64))
	}
	args = append(args, "-protocol_whitelist", "file", "-i", "file:"+s.path)
	args = append(args, pcmOutputArgs(s.sr, s.bitDepth())...)

	cmd := exec.Command("ffmpeg", args...)
	proc := newFFmpegProcess(cmd)
	pcmR, pcmW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg pipe: %w", err)
	}
	cmd.Stdout = pcmW
	if err := cmd.Start(); err != nil {
		_ = pcmR.Close()
		_ = pcmW.Close()
		return nil, fmt.Errorf("ffmpeg start: %w", err)
	}
	_ = pcmW.Close()
	proc.startMonitor()

	fp := &ffmpegPipe{
		proc:   proc,
		reader: bufio.NewReaderSize(pcmR, pipeBufSize),
		pipe:   pcmR,
		f32:    s.f32,
	}
	fp.setState(newPipeStreamState(seekPos))
	fp.setTotal(int(s.total.Load()))
	if err := fp.waitForAudioBytes(pcmFrameSize(fp.f32), ffmpegPipeTimeout); err != nil {
		_ = fp.stop()
		return nil, err
	}
	return fp, nil
}

func (s *localFFmpegStreamer) Stream(samples [][2]float64) (int, bool) {
	n, ok := s.ffmpegPipe.Stream(samples)
	if !ok || n < len(samples) {
		if s.total.Load() == 0 {
			s.total.CompareAndSwap(0, int64(s.Position()))
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
	replacement *ffmpegPipe
}

func (p *preparedFFmpegSeek) close() error {
	if p == nil || p.replacement == nil {
		return nil
	}
	return p.replacement.stop()
}

func (f *ffmpegPipe) swapFrom(next *ffmpegPipe) *ffmpegPipe {
	old := &ffmpegPipe{
		proc:   f.proc,
		reader: f.reader,
		pipe:   f.pipe,
		input:  f.input,
		pcmBuf: f.pcmBuf,
		f32:    f.f32,
		live:   f.live,
	}
	old.setState(f.state.Load())
	old.setTotal(int(f.total.Load()))

	f.setState(next.state.Load())
	f.proc = next.proc
	f.reader = next.reader
	f.pipe = next.pipe
	f.input = next.input
	f.pcmBuf = next.pcmBuf
	if n := next.total.Load(); n > 0 && f.total.Load() == 0 {
		f.total.CompareAndSwap(0, n)
	}
	return old
}

func (f *ffmpegPipe) seekMatches(prepared *preparedFFmpegSeek) bool {
	return prepared != nil && f.state.Load() == prepared.expected
}

func (f *ffmpegPipe) commitPreparedSeek(prepared *preparedFFmpegSeek) (*ffmpegPipe, bool) {
	if !f.seekMatches(prepared) || prepared.replacement == nil {
		return nil, false
	}
	old := f.swapFrom(prepared.replacement)
	prepared.replacement = nil
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
	expected := s.state.Load()
	pos = clampFFmpegSeekPosition(pos, int(s.total.Load()), s.sr)
	replacement, err := s.startPipe(pos)
	if err != nil {
		return nil, err
	}
	return &preparedFFmpegSeek{expected: expected, replacement: replacement}, nil
}

// ffmpegAvailable reports whether ffmpeg is on PATH. The buffered pipeline
// decodes through it, so a source can only be routed there when it is
// installed. It looks up PATH on each call, so an ffmpeg installed while
// cliamp runs is found.
func ffmpegAvailable() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

// requireFFmpeg returns an error with a platform install hint when ffmpeg is
// not on PATH. Every ffmpeg decoder calls it first.
func requireFFmpeg() error {
	if !ffmpegAvailable() {
		return fmt.Errorf("ffmpeg is required: %s", ffmpegInstallHint())
	}
	return nil
}

// decodeNavFFmpeg starts ffmpeg from a per-process navBuffer reader, returning a
// navFFmpegStreamer that begins producing PCM immediately as bytes arrive.
// Seeking kills ffmpeg and restarts decoding from byte zero with an FFmpeg time
// offset, so no HTTP reconnect is required.
func decodeNavFFmpeg(nb *navBuffer, sr beep.SampleRate, bitDepth int) (*navFFmpegStreamer, beep.Format, error) {
	if err := requireFFmpeg(); err != nil {
		return nil, beep.Format{}, err
	}
	_, _, precision := ffmpegPCMArgs(bitDepth)
	s := &navFFmpegStreamer{ffmpegPipe: ffmpegPipe{f32: bitDepth == 32}, nb: nb, sr: sr}
	fp, err := s.startPipe(0, false)
	if err != nil {
		return nil, beep.Format{}, err
	}
	s.swapFrom(fp)
	probeCtx, probeCancel := context.WithCancel(context.Background())
	s.probeCancel = probeCancel
	s.probeDone = make(chan struct{})
	// Measure the finished file in the background without delaying playback.
	go s.probeDownloadedLength(probeCtx)
	format := beep.Format{
		SampleRate:  sr,
		NumChannels: 2,
		Precision:   precision,
	}
	return s, format, nil
}

// decodeDownloaded decodes an MP4 file whose moov box comes after its audio.
// ffmpeg cannot read such a moov box from a pipe, so this waits for the whole
// download and decodes the temporary file, which ffmpeg can seek. The caller
// shows the buffering state during the wait. stale ends the wait when it
// reports true, because a newer request replaced this one. A nil stale never
// ends it. The decoder removes the file when it closes.
func decodeDownloaded(nb *navBuffer, sr beep.SampleRate, bitDepth int, stale func() bool) (*localFFmpegStreamer, beep.Format, error) {
	// Check before the wait, so a missing ffmpeg fails before a whole book
	// downloads.
	if err := requireFFmpeg(); err != nil {
		return nil, beep.Format{}, err
	}
	file, err := nb.waitComplete(stale)
	if err != nil {
		return nil, beep.Format{}, err
	}
	s, format, err := decodeFFmpegLocal(file, sr, bitDepth)
	if err != nil {
		return nil, beep.Format{}, err
	}
	s.owner = nb
	return s, format, nil
}

// navFFmpegStreamer streams PCM from a running ffmpeg subprocess whose stdin
// is fed by a cancellable navBuffer reader. Playback starts immediately as
// bytes arrive from the background download.
type navFFmpegStreamer struct {
	ffmpegPipe
	nb          *navBuffer
	sr          beep.SampleRate
	probeCancel context.CancelFunc
	probeDone   chan struct{}

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
// A failed or cancelled probe leaves the metadata hint in place.
func (s *navFFmpegStreamer) probeDownloadedLength(ctx context.Context) {
	defer close(s.probeDone)
	path, ok := s.nb.completedPathContext(ctx)
	if !ok {
		return
	}
	if frames := probeFramesContext(ctx, path, s.sr, ffprobeTimeout); frames > 0 {
		s.probed.Store(int64(frames))
	}
}

func (s *navFFmpegStreamer) startPipe(seekPos int, validate bool) (*ffmpegPipe, error) {
	args := []string{"-i", "pipe:0"}
	if seekPos > 0 {
		secs := float64(seekPos) / float64(s.sr)
		// Output-side seeking decodes from the start of the progressive input,
		// preserving container headers and giving sample-accurate VBR seeks.
		args = append(args, "-ss", strconv.FormatFloat(secs, 'f', 3, 64))
	}
	args = append(args, pcmOutputArgs(s.sr, s.bitDepth())...)
	cmd := exec.Command("ffmpeg", args...)
	proc := newFFmpegProcess(cmd)

	pcmR, pcmW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg nav pipe: %w", err)
	}
	inR, inW, err := os.Pipe()
	if err != nil {
		_ = pcmR.Close()
		_ = pcmW.Close()
		return nil, fmt.Errorf("ffmpeg nav stdin: %w", err)
	}
	cmd.Stdout = pcmW
	cmd.Stdin = inR
	if err := cmd.Start(); err != nil {
		_ = inR.Close()
		_ = inW.Close()
		_ = pcmR.Close()
		_ = pcmW.Close()
		return nil, fmt.Errorf("ffmpeg nav start: %w", err)
	}
	_ = pcmW.Close()
	_ = inR.Close()
	proc.startMonitor()

	fp := &ffmpegPipe{
		proc:   proc,
		pipe:   pcmR,
		reader: bufio.NewReaderSize(pcmR, pipeBufSize),
		input:  startPipeInput(s.nb.newReader(), inW),
		f32:    s.f32,
	}
	fp.setState(newPipeStreamState(seekPos))
	fp.setTotal(int(s.total.Load()))
	if validate {
		if err := fp.waitForAudioBytes(pcmFrameSize(fp.f32), ffmpegPipeTimeout); err != nil {
			_ = fp.stop()
			return nil, err
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
	expected := s.state.Load()
	pos = clampFFmpegSeekPosition(pos, s.Len(), s.sr)
	replacement, err := s.startPipe(pos, true)
	if err != nil {
		return nil, err
	}
	return &preparedFFmpegSeek{expected: expected, replacement: replacement}, nil
}

func (s *navFFmpegStreamer) Close() error {
	if s.probeCancel != nil {
		s.probeCancel()
	}
	if s.probeDone != nil {
		<-s.probeDone
	}
	// Kill ffmpeg and close stdout before waiting on the stdin pump and
	// closing navBuffer.
	_ = s.stop()
	if s.nb != nil {
		return s.nb.Close()
	}
	return nil
}

func clampFFmpegSeekPosition(pos, total int, sr beep.SampleRate) int {
	if pos < 0 {
		return 0
	}
	if total <= 0 {
		return pos
	}
	margin := 1
	if sr > 0 {
		margin = int(sr)
		if margin >= total {
			margin = max(1, int(sr)/10)
		}
	}
	maxPos := max(0, total-margin)
	if pos > maxPos {
		return maxPos
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

// pcmOutputArgs returns the ffmpeg output arguments that write stereo PCM at
// sr to stdout, in the sample format for bitDepth. Every ffmpeg decoder uses
// them after its input arguments.
func pcmOutputArgs(sr beep.SampleRate, bitDepth int) []string {
	pcmFmt, codec, _ := ffmpegPCMArgs(bitDepth)
	return []string{
		"-f", pcmFmt,
		"-acodec", codec,
		"-ar", strconv.Itoa(int(sr)),
		"-ac", "2",
		"-loglevel", "error",
		"pipe:1",
	}
}

// ffprobeTimeout bounds one ffprobe run. ffprobe reads only the container
// header, so a longer run means a stalled mount or a hung process.
const ffprobeTimeout = 5 * time.Second

// probeFrames uses ffprobe to quickly read file duration from metadata and
// converts it to sample frames. This only reads the container header, so it
// returns almost instantly even for very large files. It returns 0 when
// ffprobe fails or runs longer than ffprobeTimeout.
func probeFrames(path string, sr beep.SampleRate) int {
	return probeFramesWithin(path, sr, ffprobeTimeout)
}

func probeFramesWithin(path string, sr beep.SampleRate, timeout time.Duration) int {
	return probeFramesContext(context.Background(), path, sr, timeout)
}

func probeFramesContext(parent context.Context, path string, sr beep.SampleRate, timeout time.Duration) int {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-protocol_whitelist", "file",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		"-i", "file:"+path,
	)
	// A child that keeps stdout open must not hold Output after the kill.
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	secs, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || secs <= 0 || math.IsNaN(secs) || math.IsInf(secs, 0) {
		return 0
	}
	return int(secs * float64(sr))
}
