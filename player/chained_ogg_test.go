package player

import (
	"errors"
	"io"
	"testing"
)

type fakeVorbisRead struct {
	data []float32
	err  error
}

type fakeVorbisReader struct {
	reads []fakeVorbisRead
	calls int
}

func (f *fakeVorbisReader) Read(p []float32) (int, error) {
	if f.calls >= len(f.reads) {
		return 0, io.EOF
	}
	step := f.reads[f.calls]
	f.calls++
	n := copy(p, step.data)
	return n, step.err
}

func TestRawVorbisStreamerBatchedAndErrorPreservation(t *testing.T) {
	readErr := errors.New("network reset")
	tests := []struct {
		name      string
		channels  int
		reads     []fakeVorbisRead
		reqFrames int
		want      [][2]float64
		wantOK    bool
		wantErr   error
		wantCalls int
	}{
		{
			name:     "batches stereo frames in one read",
			channels: 2,
			reads: []fakeVorbisRead{
				{data: []float32{0.1, -0.1, 0.2, -0.2, 0.3, -0.3}},
			},
			reqFrames: 3,
			want: [][2]float64{
				{float64(float32(0.1)), float64(float32(-0.1))},
				{float64(float32(0.2)), float64(float32(-0.2))},
				{float64(float32(0.3)), float64(float32(-0.3))},
			},
			wantOK:    true,
			wantCalls: 1,
		},
		{
			name:     "maps mono channel to stereo",
			channels: 1,
			reads: []fakeVorbisRead{
				{data: []float32{0.5, -0.5}},
			},
			reqFrames: 2,
			want:      [][2]float64{{0.5, 0.5}, {-0.5, -0.5}},
			wantOK:    true,
			wantCalls: 1,
		},
		{
			name:     "preserves non-EOF error when dn is zero",
			channels: 2,
			reads: []fakeVorbisRead{
				{data: nil, err: readErr},
			},
			reqFrames: 4,
			wantOK:    false,
			wantErr:   readErr,
			wantCalls: 1,
		},
		{
			name:     "clean EOF when dn is zero leaves err nil",
			channels: 2,
			reads: []fakeVorbisRead{
				{data: nil, err: io.EOF},
			},
			reqFrames: 4,
			wantOK:    false,
			wantErr:   nil,
			wantCalls: 1,
		},
		{
			name:     "preserves non-EOF error after partial batch",
			channels: 2,
			reads: []fakeVorbisRead{
				{data: []float32{0.25, 0.75}, err: nil},
				{data: nil, err: readErr},
			},
			reqFrames: 4,
			want:      [][2]float64{{0.25, 0.75}},
			wantOK:    true,
			wantErr:   readErr,
			wantCalls: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fr := &fakeVorbisReader{reads: tt.reads}
			left, right := vorbisChannelIndices(tt.channels)
			s := &rawVorbisStreamer{
				reader:   fr,
				channels: tt.channels,
				tmp:      make([]float32, vorbisBatchFrames*tt.channels),
				left:     left,
				right:    right,
			}
			out := make([][2]float64, tt.reqFrames)
			n, ok := s.Stream(out)
			if n != len(tt.want) || ok != tt.wantOK {
				t.Fatalf("Stream() = (%d, %v), want (%d, %v)", n, ok, len(tt.want), tt.wantOK)
			}
			for i := range tt.want {
				if out[i] != tt.want[i] {
					t.Fatalf("out[%d] = %v, want %v", i, out[i], tt.want[i])
				}
			}
			if !errors.Is(s.Err(), tt.wantErr) {
				t.Fatalf("Err() = %v, want %v", s.Err(), tt.wantErr)
			}
			if fr.calls != tt.wantCalls {
				t.Fatalf("reader Read calls = %d, want %d", fr.calls, tt.wantCalls)
			}
		})
	}
}

func TestChainedOggStreamerReturnsImmediatelyOnError(t *testing.T) {
	wantErr := errors.New("chain failed")
	calls := 0
	cs := &chainedOggStreamer{
		err: wantErr,
		stream: &countingFailStreamer{
			onStream: func() { calls++ },
		},
	}
	n, ok := cs.Stream(make([][2]float64, 4))
	if n != 0 || ok {
		t.Fatalf("Stream() = (%d, %v), want (0, false)", n, ok)
	}
	if calls != 0 {
		t.Fatalf("underlying Stream called %d times after cs.err was set, want 0", calls)
	}
	if !errors.Is(cs.Err(), wantErr) {
		t.Fatalf("Err() = %v, want %v", cs.Err(), wantErr)
	}
}

type countingFailStreamer struct {
	onStream func()
}

func (c *countingFailStreamer) Stream([][2]float64) (int, bool) {
	if c.onStream != nil {
		c.onStream()
	}
	return 0, false
}

func (*countingFailStreamer) Err() error { return nil }
