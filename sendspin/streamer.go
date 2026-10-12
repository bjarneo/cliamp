package sendspin

import (
	"encoding/binary"
	"fmt"
	"strings"
	"time"

	"github.com/gopxl/beep/v2"
)

// URI is the playlist path that plays the renderer's stream.
const URI = "sendspin:live"

// NewStreamer returns the beep source for the Sendspin stream. The newest
// streamer owns the output; Streaming reports true until it closes.
// It matches player.StreamerFactory.
func (r *Renderer) NewStreamer(uri string) (beep.StreamSeekCloser, beep.Format, time.Duration, error) {
	if !strings.HasPrefix(uri, "sendspin:") {
		return nil, beep.Format{}, 0, fmt.Errorf("sendspin: unsupported uri %q", uri)
	}
	gen := r.gen.Add(1)
	r.owner.Store(gen)
	format := beep.Format{SampleRate: SampleRate, NumChannels: 2, Precision: 2}
	return &streamer{r: r, gen: gen, epoch: r.epoch.Load()}, format, 0, nil
}

// streamer turns queued PCM into beep samples. Before the first chunk it
// waits for a short prebuffer; on an underrun it fades the last sample out
// instead of clicking.
type streamer struct {
	r       *Renderer
	gen     int64
	epoch   int64
	pending []byte
	last    [2]float64
	primed  bool
	held    int
}

// drain moves queued PCM into pending. Audio of a stream that has since been
// replaced, stopped or cleared is dropped, including what was queued while
// the output was paused.
func (s *streamer) drain() {
	if cur := s.r.epoch.Load(); cur != s.epoch {
		s.epoch = cur
		s.pending = s.pending[:0]
		s.primed = false
	}
	for {
		select {
		case chunk := <-s.r.pcm:
			if int64(binary.LittleEndian.Uint64(chunk)) == s.epoch {
				s.pending = append(s.pending, chunk[8:]...)
			}
		default:
			return
		}
	}
}

func (s *streamer) Stream(samples [][2]float64) (int, bool) {
	s.drain()
	const prebuffer = SampleRate * 80 / 1000 // frames held back before the first sample
	const holdLimit = SampleRate * 2 / 1000  // underrun frames faded out before silence
	if !s.primed && len(s.pending)/frameBytes < prebuffer {
		clear(samples)
		return len(samples), true
	}
	s.primed = true
	n := 0
	for n < len(samples) {
		if len(s.pending) >= frameBytes {
			l := int16(binary.LittleEndian.Uint16(s.pending[0:]))
			r := int16(binary.LittleEndian.Uint16(s.pending[2:]))
			s.pending = s.pending[frameBytes:]
			s.last = [2]float64{float64(l) / 32768, float64(r) / 32768}
			samples[n] = s.last
			s.held = 0
			s.r.pos.Add(1)
		} else if s.held < holdLimit {
			fade := 1 - float64(s.held)/float64(holdLimit)
			samples[n] = [2]float64{s.last[0] * fade, s.last[1] * fade}
			s.held++
		} else {
			samples[n] = [2]float64{}
		}
		n++
	}
	return n, true
}

func (s *streamer) Err() error { return nil }

// Len is 0: the stream is live and has no known length.
func (s *streamer) Len() int { return 0 }

// Position is the track position in frames. See Renderer.Position.
func (s *streamer) Position() int { return int(s.r.Position() * SampleRate / time.Second) }

// Seek is unsupported; Music Assistant seeks on the server.
func (s *streamer) Seek(int) error { return nil }

func (s *streamer) Close() error {
	s.r.owner.CompareAndSwap(s.gen, 0)
	return nil
}
