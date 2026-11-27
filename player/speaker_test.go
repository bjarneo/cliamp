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
		{-1, -32767},
		{2, 32767},
		{-2, -32767},
		{math.NaN(), 0},
		{math.Inf(1), 32767},
		{math.Inf(-1), -32767},
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

type callCountingStreamer struct {
	calls int
	val   [2]float64
	left  int
}

func (s *callCountingStreamer) Stream(samples [][2]float64) (int, bool) {
	s.calls++
	n := len(samples)
	if s.left >= 0 {
		n = min(n, s.left)
		s.left -= n
	}
	for i := range samples[:n] {
		samples[i] = s.val
	}
	return n, n > 0
}

func (*callCountingStreamer) Err() error { return nil }

func TestMixerReaderSingleSlotDirectAndMultiFallback(t *testing.T) {
	t.Run("single streamer bypasses 512-frame chunking", func(t *testing.T) {
		out := &audioOutput{}
		src := &callCountingStreamer{val: [2]float64{0.5, -0.25}, left: -1}
		out.Play(src)
		r := &mixerReader{out: out}
		buf := make([]byte, 1024*outputBytesPerFrame)
		n, err := r.Read(buf)
		if err != nil || n != len(buf) {
			t.Fatalf("Read = (%d, %v), want (%d, nil)", n, err, len(buf))
		}
		if src.calls != 1 {
			t.Fatalf("single streamer Stream calls = %d, want 1", src.calls)
		}
		gotL := int16(binary.LittleEndian.Uint16(buf[0:2]))
		gotR := int16(binary.LittleEndian.Uint16(buf[2:4]))
		if gotL != 16383 || gotR != -8191 {
			t.Fatalf("first stereo frame = (%d, %d), want (16383, -8191)", gotL, gotR)
		}
	})

	t.Run("partial drain pads trailing silence and removes streamer", func(t *testing.T) {
		out := &audioOutput{}
		src := &callCountingStreamer{val: [2]float64{0.5, 0.5}, left: 2}
		out.Play(src)
		r := &mixerReader{out: out}
		buf := make([]byte, 4*outputBytesPerFrame)
		for i := range buf {
			buf[i] = 0x7f
		}
		n, err := r.Read(buf)
		if err != nil || n != len(buf) {
			t.Fatalf("Read = (%d, %v), want (%d, nil)", n, err, len(buf))
		}
		for frame := range 4 {
			got := int16(binary.LittleEndian.Uint16(buf[frame*4:]))
			want := int16(16383)
			if frame >= 2 {
				want = 0
			}
			if got != want {
				t.Fatalf("frame %d = %d, want %d", frame, got, want)
			}
		}
		if out.single != nil {
			t.Fatal("drained single streamer stayed registered")
		}
	})

	t.Run("multiple streamers mix and collapse back to single slot", func(t *testing.T) {
		out := &audioOutput{}
		short := &callCountingStreamer{val: [2]float64{0.25, 0.25}, left: 2}
		steady := &callCountingStreamer{val: [2]float64{0.25, -0.25}, left: -1}
		out.Play(short, steady)
		r := &mixerReader{out: out}

		buf := make([]byte, 4*outputBytesPerFrame)
		if _, err := r.Read(buf); err != nil {
			t.Fatalf("first Read error = %v", err)
		}
		// Frames 0..1 mix short + steady: L=0.5, R=0.
		if gotL := int16(binary.LittleEndian.Uint16(buf[0:2])); gotL != 16383 {
			t.Fatalf("mixed frame 0 left = %d, want 16383", gotL)
		}
		if gotR := int16(binary.LittleEndian.Uint16(buf[2:4])); gotR != 0 {
			t.Fatalf("mixed frame 0 right = %d, want 0", gotR)
		}
		// Frames 2..3 carry only steady: L=0.25, R=-0.25.
		if gotL := int16(binary.LittleEndian.Uint16(buf[8:10])); gotL != 8191 {
			t.Fatalf("frame 2 left = %d, want 8191", gotL)
		}
		if out.single != steady || len(out.multi) != 0 {
			t.Fatalf("after short drained: single=%v len(multi)=%d, want steady and 0", out.single, len(out.multi))
		}
	})
}

func BenchmarkSpeakerRead(b *testing.B) {
	out := &audioOutput{}
	out.Play(&levelDecoder{level: 0.5, left: -1})
	r := &mixerReader{out: out}
	pcm := make([]byte, 1024*outputBytesPerFrame)
	b.ReportAllocs()
	for b.Loop() {
		_, _ = r.Read(pcm)
	}
}
