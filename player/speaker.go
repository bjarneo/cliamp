package player

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/ebitengine/oto/v3"
	"github.com/gopxl/beep/v2"
)

// speaker is the audio output of the player. It works as the speaker package
// of beep does, and it adds Drop. The beep package cannot drop the samples
// that oto has queued, so after a pause the next track played them first.
var speaker = &audioOutput{}

// outputBytesPerFrame is the size of one 16-bit stereo frame.
const outputBytesPerFrame = 4

// outputDevice is the part of the oto context that the output uses.
type outputDevice interface {
	Suspend() error
	Resume() error
}

// outputQueue is the part of the oto player that the output uses. The oto
// player reads ahead from the mixer and queues the samples until the device
// takes them. Seek empties the queue.
type outputQueue interface {
	Seek(offset int64, whence int) (int64, error)
}

// audioOutput mixes the playing streamers and sends them to the device.
// While the speaker lock is held, the output reads no samples.
type audioOutput struct {
	mu     sync.Mutex // the speaker lock
	single beep.Streamer
	multi  []beep.Streamer
	mixBuf [][2]float64
	device outputDevice // nil until Init
	queue  outputQueue  // nil until Init
}

// Init opens the audio device. bufferSize is in frames. The device and the
// oto queue each get half of it, as in the speaker package of beep.
func (o *audioOutput) Init(sampleRate beep.SampleRate, bufferSize int) error {
	if o.device != nil {
		return errors.New("speaker cannot be initialized more than once")
	}
	ctx, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate:   int(sampleRate),
		ChannelCount: 2,
		Format:       oto.FormatSignedInt16LE,
		BufferSize:   sampleRate.D(bufferSize / 2),
	})
	if err != nil {
		return fmt.Errorf("failed to initialize speaker: %w", err)
	}
	<-ready
	queue := ctx.NewPlayer(&mixerReader{out: o})
	queue.SetBufferSize(bufferSize / 2 * outputBytesPerFrame)
	queue.Play()
	o.device, o.queue = ctx, queue
	return nil
}

// Lock stops the output from reading samples until Unlock. Hold it while
// you change a playing streamer.
func (o *audioOutput) Lock() { o.mu.Lock() }

// Unlock lets the output read samples again.
func (o *audioOutput) Unlock() { o.mu.Unlock() }

// Play registers one or more streamers for playback.
func (o *audioOutput) Play(s ...beep.Streamer) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, st := range s {
		if st == nil {
			continue
		}
		if o.single == nil && len(o.multi) == 0 {
			o.single = st
			continue
		}
		if o.single != nil {
			o.multi = append(o.multi, o.single)
			o.single = nil
		}
		o.multi = append(o.multi, st)
	}
}

// Clear removes all active streamers from the output. The samples that the
// output queued before the call still play.
func (o *audioOutput) Clear() {
	o.mu.Lock()
	o.single = nil
	clear(o.multi)
	o.multi = o.multi[:0]
	o.mu.Unlock()
}

// streamLocked reads up to len(dst) frames from the active streamers and
// returns the highest written frame count. The caller holds o.mu.
func (o *audioOutput) streamLocked(dst [][2]float64) int {
	if o.single != nil {
		s := o.single
		n, ok := s.Stream(dst)
		if n < len(dst) || !ok {
			if o.single == s {
				o.single = nil
			}
		}
		return n
	}
	if len(o.multi) == 0 {
		return 0
	}
	clear(dst)
	if len(o.mixBuf) < len(dst) {
		o.mixBuf = make([][2]float64, len(dst))
	}
	tmp := o.mixBuf[:len(dst)]
	maxN := 0
	for si := 0; si < len(o.multi); si++ {
		sn, ok := o.multi[si].Stream(tmp)
		for i := range sn {
			dst[i][0] += tmp[i][0]
			dst[i][1] += tmp[i][1]
		}
		if sn > maxN {
			maxN = sn
		}
		if sn < len(dst) || !ok {
			last := len(o.multi) - 1
			o.multi[si] = o.multi[last]
			o.multi[last] = nil
			o.multi = o.multi[:last]
			si--
		}
	}
	if len(o.multi) == 1 {
		o.single = o.multi[0]
		o.multi[0] = nil
		o.multi = o.multi[:0]
	}
	return maxN
}

// Suspend stops the device. The queued samples stay in the queue, and the
// device plays them first after Resume.
func (o *audioOutput) Suspend() error {
	if o.device == nil {
		return nil
	}
	if err := o.device.Suspend(); err != nil {
		return fmt.Errorf("failed to suspend the speaker: %w", err)
	}
	return nil
}

// Resume starts the device again after Suspend.
func (o *audioOutput) Resume() error {
	if o.device == nil {
		return nil
	}
	if err := o.device.Resume(); err != nil {
		return fmt.Errorf("failed to resume the speaker: %w", err)
	}
	return nil
}

// Drop discards the samples that the output queued and the device did not
// take. oto then fills the queue from the mixer at once, and it can read on
// two goroutines while it does. Call Drop only while the streamers send
// silence, so the order of the reads does not matter and no read waits on a
// decoder. Do not hold the speaker lock: the reads take it.
func (o *audioOutput) Drop() {
	if o.queue == nil {
		return
	}
	// oto empties its queue on Seek. The mixer reader has no position, so
	// the seek itself does nothing and cannot fail.
	_, _ = o.queue.Seek(0, io.SeekCurrent)
}

// mixerReader is the source of the oto player. It reads the mixer and
// encodes the samples as 16-bit little-endian stereo PCM.
type mixerReader struct {
	out *audioOutput
	mu  sync.Mutex // oto reads on two goroutines while Drop fills the queue
	buf [][2]float64
}

const (
	pcm16Max = 1<<15 - 1
	pcm16Min = uint16(0x8001) // two's complement of -32767
)

// clampPCM16 converts one float64 sample to signed 16-bit PCM.
// It maps NaN to 0 and clamps values outside [-1, 1] to full scale.
func clampPCM16(v float64) uint16 {
	if v >= -1 && v <= 1 {
		return uint16(int16(v * pcm16Max))
	}
	if v > 1 {
		return pcm16Max
	}
	if v < -1 {
		return pcm16Min
	}
	return 0
}

func (r *mixerReader) Read(p []byte) (int, error) {
	if len(p)%outputBytesPerFrame != 0 {
		return 0, errors.New("requested number of bytes do not align with the samples")
	}
	if len(p) == 0 {
		return 0, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	frames := len(p) / outputBytesPerFrame
	if len(r.buf) < frames {
		r.buf = make([][2]float64, frames)
	}
	r.out.mu.Lock()
	n := r.out.streamLocked(r.buf[:frames])
	r.out.mu.Unlock()
	if n == 0 {
		clear(p)
		return len(p), nil
	}
	out := p[:n*outputBytesPerFrame]
	for i, frame := range r.buf[:n] {
		l := clampPCM16(frame[0])
		rc := clampPCM16(frame[1])
		binary.LittleEndian.PutUint32(out[i*outputBytesPerFrame:], uint32(l)|uint32(rc)<<16)
	}
	if n < frames {
		clear(p[n*outputBytesPerFrame:])
	}
	return len(p), nil
}

// Seek does nothing, because a mixer has no position. oto calls it when Drop
// empties the queue.
func (r *mixerReader) Seek(int64, int) (int64, error) { return 0, nil }
