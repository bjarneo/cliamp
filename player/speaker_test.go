package player

import (
	"encoding/binary"
	"math"
	"testing"
)

// testOutput stands in for oto under the speaker. As the oto player does, it
// reads ahead from the mixer and keeps what it read while the device is
// suspended. Seek empties the queue, as it empties the oto player.
type testOutput struct {
	t         *testing.T
	src       *mixerReader
	queued    []int16 // the left channel of each queued frame
	suspended bool
}

func (o *testOutput) Suspend() error { o.suspended = true; return nil }
func (o *testOutput) Resume() error  { o.suspended = false; return nil }

func (o *testOutput) Seek(int64, int) (int64, error) {
	o.queued = o.queued[:0]
	return 0, nil
}

// readAhead reads frames from the mixer into the queue.
func (o *testOutput) readAhead(frames int) {
	o.t.Helper()
	buf := make([]byte, frames*outputBytesPerFrame)
	if n, err := o.src.Read(buf); err != nil || n != len(buf) {
		o.t.Fatalf("Read = %d, %v, want %d, nil", n, err, len(buf))
	}
	for i := 0; i < len(buf); i += outputBytesPerFrame {
		o.queued = append(o.queued, int16(binary.LittleEndian.Uint16(buf[i:])))
	}
}

// play returns the next frames that the device plays: the queue first, then
// new reads from the mixer.
func (o *testOutput) play(frames int) []int16 {
	o.t.Helper()
	if o.suspended {
		o.t.Fatal("the device plays while it is suspended")
	}
	if missing := frames - len(o.queued); missing > 0 {
		o.readAhead(missing)
	}
	played := o.queued[:frames]
	o.queued = o.queued[frames:]
	return played
}

// useTestOutput puts a suspended testOutput under the speaker until the test
// ends. New suspends the speaker in the same way.
func useTestOutput(t *testing.T) *testOutput {
	t.Helper()
	out := &testOutput{t: t, src: &mixerReader{out: speaker}, suspended: true}
	speaker.device, speaker.queue = out, out
	t.Cleanup(func() {
		speaker.Clear()
		speaker.device, speaker.queue = nil, nil
	})
	return out
}

// levelDecoder streams frames at one level. A negative left streams without
// end.
type levelDecoder struct {
	level float64
	left  int
}

func (d *levelDecoder) Stream(samples [][2]float64) (int, bool) {
	n := len(samples)
	if d.left >= 0 {
		n = min(n, d.left)
		d.left -= n
	}
	for i := range samples[:n] {
		samples[i] = [2]float64{d.level, d.level}
	}
	return n, n > 0
}

func (*levelDecoder) Err() error     { return nil }
func (*levelDecoder) Len() int       { return 0 }
func (*levelDecoder) Position() int  { return 0 }
func (*levelDecoder) Seek(int) error { return nil }
func (*levelDecoder) Close() error   { return nil }

// levelTrack returns a pipeline that plays frames at level.
func levelTrack(level float64, frames int) *trackPipeline {
	d := &levelDecoder{level: level, left: frames}
	return &trackPipeline{decoder: d, stream: d}
}

// After a pause or a stop, the device must not play the samples of the old
// track before a new track. A plain resume plays them, because they continue
// the track. The last samples of a track that played to its end also play.
func TestQueuedSamplesAfterPauseOrStop(t *testing.T) {
	const queued = 64 // frames that the device holds when the action starts
	start := func(t *testing.T, p *Player, tp *trackPipeline) {
		t.Helper()
		if err := p.playPipelineForGeneration(tp, 0); err != nil {
			t.Fatalf("playPipelineForGeneration: %v", err)
		}
	}
	tests := []struct {
		name      string
		oldFrames int // frames of the old track. -1 streams without end
		act       func(t *testing.T, p *Player, next *trackPipeline)
		wantOld   bool // the queued samples of the old track play first
	}{
		{
			name:      "resume after pause",
			oldFrames: -1,
			act: func(t *testing.T, p *Player, _ *trackPipeline) {
				p.TogglePause()
				p.TogglePause()
			},
			wantOld: true,
		},
		{
			name:      "new track after pause",
			oldFrames: -1,
			act: func(t *testing.T, p *Player, next *trackPipeline) {
				p.TogglePause()
				start(t, p, next)
			},
		},
		{
			name:      "new track after stop",
			oldFrames: -1,
			act: func(t *testing.T, p *Player, next *trackPipeline) {
				p.Stop()
				start(t, p, next)
			},
		},
		{
			name:      "new track after pause and stop",
			oldFrames: -1,
			act: func(t *testing.T, p *Player, next *trackPipeline) {
				p.TogglePause()
				p.Stop()
				start(t, p, next)
			},
		},
		{
			name:      "new track after the old track played to its end",
			oldFrames: queued / 2,
			act: func(t *testing.T, p *Player, next *trackPipeline) {
				if !p.Drained() {
					t.Fatal("the old track did not drain")
				}
				p.Stop()
				start(t, p, next)
			},
			wantOld: true,
		},
		{
			name:      "new track while playing",
			oldFrames: -1,
			act: func(t *testing.T, p *Player, next *trackPipeline) {
				start(t, p, next)
			},
			wantOld: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := useTestOutput(t)
			p := &Player{sr: 1000, gapless: &gaplessStreamer{}, tapBufferFrames: 4096, suspended: true}
			p.speed.Store(math.Float64bits(1.0))
			start(t, p, levelTrack(0.5, tt.oldFrames))
			out.readAhead(queued) // oto keeps its queue full while it plays

			tt.act(t, p, levelTrack(-0.5, -1))

			played := out.play(2 * queued)
			if gotOld := played[0] > 0; gotOld != tt.wantOld {
				t.Fatalf("the first frame is from the old track: %v, want %v. Frames: %v", gotOld, tt.wantOld, played)
			}
			if tt.wantOld {
				return
			}
			for i, v := range played {
				if v > 0 {
					t.Fatalf("frame %d is from the old track. Frames: %v", i, played)
				}
			}
			if played[len(played)-1] >= 0 {
				t.Fatalf("the new track did not play. Frames: %v", played)
			}
		})
	}
}

// The mixer reader encodes the samples as the speaker package of beep does:
// 16-bit little-endian stereo PCM, clamped to [-1, 1].
func TestMixerReaderEncodesPCM(t *testing.T) {
	tests := []struct {
		level float64
		want  int16
	}{
		{0, 0},
		{0.5, 16383},
		{-0.5, -16383},
		{1, 32767},
		{2, 32767},
		{-2, -32767},
	}
	for _, tt := range tests {
		out := &audioOutput{}
		out.Play(&levelDecoder{level: tt.level, left: -1})
		buf := make([]byte, 2*outputBytesPerFrame)
		n, err := (&mixerReader{out: out}).Read(buf)
		if err != nil || n != len(buf) {
			t.Fatalf("level %v: Read = %d, %v, want %d, nil", tt.level, n, err, len(buf))
		}
		for i := 0; i < len(buf); i += 2 {
			if got := int16(binary.LittleEndian.Uint16(buf[i:])); got != tt.want {
				t.Errorf("level %v: sample %d = %d, want %d", tt.level, i/2, got, tt.want)
			}
		}
	}
	if _, err := (&mixerReader{out: &audioOutput{}}).Read(make([]byte, 3)); err == nil {
		t.Error("Read of a partial frame: got no error")
	}
}
