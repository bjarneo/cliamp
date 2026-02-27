package player

import (
	"io"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/vorbis"
)

// Compile-time interface checks.
var (
	_ io.ReadCloser         = noCloseReader{}
	_ beep.StreamSeekCloser = (*chainedOggStreamer)(nil)
)

// noCloseReader wraps an io.Reader with a Close that is a no-op.
// This prevents vorbis.Decode from closing the underlying HTTP body
// when we re-initialize the decoder for a new logical bitstream.
type noCloseReader struct{ io.Reader }

func (noCloseReader) Close() error { return nil }

// chainedOggStreamer handles chained OGG/Vorbis streams (e.g., Icecast radio).
// When the current decoder hits EOS (end of a logical bitstream), it
// re-initializes the vorbis decoder on the same underlying reader to
// continue with the next song — achieving seamless chain transitions.
type chainedOggStreamer struct {
	rc       io.ReadCloser         // underlying HTTP body (stays open across chains)
	decoder  beep.StreamSeekCloser // current vorbis decoder
	format   beep.Format
	targetSR beep.SampleRate
	stream   beep.Streamer // decoder + optional resample
	err      error
}

func newChainedOggStreamer(rc io.ReadCloser, targetSR beep.SampleRate) (*chainedOggStreamer, beep.Format, error) {
	decoder, format, err := vorbis.Decode(noCloseReader{rc})
	if err != nil {
		return nil, beep.Format{}, err
	}

	cs := &chainedOggStreamer{
		rc:       rc,
		decoder:  decoder,
		format:   format,
		targetSR: targetSR,
	}
	cs.stream = cs.buildStream(decoder, format)

	return cs, format, nil
}

// buildStream wraps a decoder with a resampler if needed.
func (cs *chainedOggStreamer) buildStream(decoder beep.StreamSeekCloser, format beep.Format) beep.Streamer {
	if format.SampleRate != cs.targetSR {
		return beep.Resample(4, format.SampleRate, cs.targetSR, decoder)
	}
	return decoder
}

func (cs *chainedOggStreamer) Stream(samples [][2]float64) (int, bool) {
	n, ok := cs.stream.Stream(samples)
	if n > 0 || ok {
		return n, ok
	}

	// Decoder returned (0, false) — try to chain to the next logical bitstream.
	cs.decoder.Close()

	newDecoder, newFormat, err := vorbis.Decode(noCloseReader{cs.rc})
	if err != nil {
		// Real EOF or broken stream — propagate.
		cs.err = err
		return 0, false
	}

	cs.decoder = newDecoder
	cs.format = newFormat
	cs.stream = cs.buildStream(newDecoder, newFormat)

	// Fill remaining samples from the new chain.
	return cs.stream.Stream(samples[n:])
}

func (cs *chainedOggStreamer) Err() error {
	if cs.err != nil {
		return cs.err
	}
	return cs.decoder.Err()
}

// Len returns 0 — live streams have no known length.
func (cs *chainedOggStreamer) Len() int { return 0 }

// Position returns 0 — live streams are not seekable.
func (cs *chainedOggStreamer) Position() int { return 0 }

// Seek is a no-op for live streams.
func (cs *chainedOggStreamer) Seek(int) error { return nil }

// Close closes the current decoder and the underlying reader.
func (cs *chainedOggStreamer) Close() error {
	cs.decoder.Close()
	return cs.rc.Close()
}
