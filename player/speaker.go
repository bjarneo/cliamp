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
	mixer  beep.Mixer
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

// Play adds the streamers to the mixer.
func (o *audioOutput) Play(s ...beep.Streamer) {
	o.mu.Lock()
	o.mixer.Add(s...)
	o.mu.Unlock()
}

// Clear removes all streamers from the mixer. The samples that the output
// queued before the call still play.
func (o *audioOutput) Clear() {
	o.mu.Lock()
	o.mixer.Clear()
	o.mu.Unlock()
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

func (r *mixerReader) Read(p []byte) (int, error) {
	if len(p)%outputBytesPerFrame != 0 {
		return 0, errors.New("requested number of bytes do not align with the samples")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	frames := len(p) / outputBytesPerFrame
	if len(r.buf) < frames {
		r.buf = make([][2]float64, frames)
	}
	r.out.mu.Lock()
	// The mixer sends silence when it has no streamers, so it fills every frame.
	n, _ := r.out.mixer.Stream(r.buf[:frames])
	r.out.mu.Unlock()
	for i, frame := range r.buf[:n] {
		for c, v := range frame {
			v16 := int16(max(min(v, 1), -1) * (1<<15 - 1))
			binary.LittleEndian.PutUint16(p[i*outputBytesPerFrame+c*2:], uint16(v16))
		}
	}
	return n * outputBytesPerFrame, nil
}

// Seek does nothing, because a mixer has no position. oto calls it when Drop
// empties the queue.
func (r *mixerReader) Seek(int64, int) (int64, error) { return 0, nil }
