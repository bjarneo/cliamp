package player

import (
	"io"
	"strings"

	"github.com/gopxl/beep/v2"
	"github.com/jfreymuth/oggvorbis"
)

// Compile-time interface checks.
var (
	_ io.ReadCloser         = noCloseReader{}
	_ beep.StreamSeekCloser = (*chainedOggStreamer)(nil)
)

// noCloseReader wraps an io.Reader with a Close that is a no-op.
// This prevents the underlying HTTP body from being closed when we
// re-initialize the decoder for a new logical bitstream.
type noCloseReader struct{ io.Reader }

func (noCloseReader) Close() error { return nil }

// chainedOggStreamer handles chained OGG/Vorbis streams (e.g., Icecast radio).
// When the current decoder hits EOS (end of a logical bitstream), it
// re-initializes the vorbis decoder on the same underlying reader to
// continue with the next song — achieving seamless chain transitions.
//
// Unlike the beep/vorbis.Decode wrapper, this uses jfreymuth/oggvorbis
// directly so it can read Vorbis comment headers (TITLE, ARTIST) at each
// chain boundary and report them via the onMeta callback.
type chainedOggStreamer struct {
	rc              io.ReadCloser      // underlying HTTP body (stays open across chains)
	reader          *oggvorbis.Reader  // current logical bitstream
	raw             *rawVorbisStreamer // raw audio from reader
	format          beep.Format
	targetSR        beep.SampleRate
	resampleQuality int
	stream          beep.Streamer // raw or resampled (fed to gapless)
	onMeta          func(string)  // called with "Artist - Title" on chain
	err             error
}

func newChainedOggStreamer(rc io.ReadCloser, targetSR beep.SampleRate, resampleQuality int, onMeta func(string)) (*chainedOggStreamer, beep.Format, error) {
	reader, err := oggvorbis.NewReader(noCloseReader{rc})
	if err != nil {
		return nil, beep.Format{}, err
	}

	cs := &chainedOggStreamer{
		rc:              rc,
		targetSR:        targetSR,
		resampleQuality: resampleQuality,
		onMeta:          onMeta,
	}
	cs.initDecoder(reader)
	cs.notifyMeta()

	return cs, cs.format, nil
}

// initDecoder sets up the raw decoder and resampler for a new logical bitstream.
func (cs *chainedOggStreamer) initDecoder(reader *oggvorbis.Reader) {
	cs.reader = reader

	channels := min(reader.Channels(), 2)
	cs.format = beep.Format{
		SampleRate:  beep.SampleRate(reader.SampleRate()),
		NumChannels: channels,
		Precision:   2,
	}

	left, right := vorbisChannelIndices(reader.Channels())
	cs.raw = &rawVorbisStreamer{
		reader: reader,
		tmp:    make([]float32, reader.Channels()),
		left:   left,
		right:  right,
	}

	var s beep.Streamer = cs.raw
	if cs.format.SampleRate != cs.targetSR {
		s = beep.Resample(cs.resampleQuality, cs.format.SampleRate, cs.targetSR, s)
	}
	cs.stream = s
}

// notifyMeta extracts ARTIST/TITLE from Vorbis comments and fires onMeta.
func (cs *chainedOggStreamer) notifyMeta() {
	if cs.onMeta == nil {
		return
	}
	title := vorbisCommentTitle(cs.reader.CommentHeader().Comments)
	if title != "" {
		cs.onMeta(title)
	}
}

// Stream fills the sample buffer, chaining to the next logical bitstream
// when the current one is exhausted. It always fills the full buffer for
// live radio — this prevents the gapless streamer from treating a partial
// fill at a chain boundary as track exhaustion.
func (cs *chainedOggStreamer) Stream(samples [][2]float64) (int, bool) {
	total := 0
	for total < len(samples) {
		n, ok := cs.stream.Stream(samples[total:])
		total += n
		if ok {
			continue
		}
		// Stream exhausted — chain to the next logical bitstream.
		if err := cs.chain(); err != nil {
			cs.err = err
			break
		}
	}
	return total, total > 0
}

// chain re-initializes the decoder for the next logical bitstream in the
// chained OGG stream and extracts its Vorbis comment metadata.
func (cs *chainedOggStreamer) chain() error {
	reader, err := oggvorbis.NewReader(noCloseReader{cs.rc})
	if err != nil {
		return err
	}
	cs.initDecoder(reader)
	cs.notifyMeta()
	return nil
}

func (cs *chainedOggStreamer) Err() error {
	if cs.err != nil {
		return cs.err
	}
	return cs.raw.err
}

// Len returns 0 — live streams have no known length.
func (cs *chainedOggStreamer) Len() int { return 0 }

// Position returns 0 — live streams are not seekable.
func (cs *chainedOggStreamer) Position() int { return 0 }

// Seek is a no-op for live streams.
func (cs *chainedOggStreamer) Seek(int) error { return nil }

// Close closes the underlying HTTP body.
func (cs *chainedOggStreamer) Close() error {
	return cs.rc.Close()
}

// rawVorbisStreamer reads audio samples from an oggvorbis.Reader.
// It mirrors beep's vorbis decoder but gives us direct access to the
// underlying reader for Vorbis comment extraction.
type rawVorbisStreamer struct {
	reader      *oggvorbis.Reader
	tmp         []float32 // per-frame buffer (channels wide)
	left, right int       // channel indices
	err         error
}

func (s *rawVorbisStreamer) Stream(samples [][2]float64) (int, bool) {
	if s.err != nil {
		return 0, false
	}
	var n int
	for i := range samples {
		dn, err := s.reader.Read(s.tmp)
		if dn == 0 {
			break
		}
		if dn < len(s.tmp) {
			break // partial frame — treat as EOS
		}
		samples[i][0] = float64(s.tmp[s.left])
		samples[i][1] = float64(s.tmp[s.right])
		n++
		if err != nil {
			if err != io.EOF {
				s.err = err
			}
			break
		}
	}
	return n, n > 0
}

func (s *rawVorbisStreamer) Err() error { return s.err }

// vorbisChannelIndices returns the left and right channel indices for
// the given channel count, matching the Vorbis I spec channel mapping.
func vorbisChannelIndices(channels int) (left, right int) {
	switch channels {
	case 1:
		return 0, 0
	case 2, 4:
		return 0, 1
	default:
		return 0, 2
	}
}

// vorbisCommentTitle extracts a display title from Vorbis comment fields.
// Returns "Artist - Title" if both are present, just the title otherwise.
func vorbisCommentTitle(comments []string) string {
	var artist, title string
	for _, c := range comments {
		k, v, ok := strings.Cut(c, "=")
		if !ok {
			continue
		}
		switch strings.ToUpper(k) {
		case "TITLE":
			title = v
		case "ARTIST":
			artist = v
		}
	}
	if title == "" {
		return ""
	}
	if artist != "" {
		return artist + " - " + title
	}
	return title
}
