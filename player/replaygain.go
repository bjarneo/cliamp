package player

import (
	"bytes"
	"math"
	"sync"

	"github.com/dhowden/tag"
	"github.com/gopxl/beep/v2"

	"github.com/bjarneo/cliamp/internal/replaygain"
)

// replayGainState is the player's normalisation setting.
type replayGainState struct {
	mu     sync.Mutex
	mode   string
	preamp float64
}

// SetReplayGain sets the normalisation mode ("off", "track" or "album") and the
// preamp in dB. It applies from the next track that starts or is preloaded.
func (p *Player) SetReplayGain(mode string, preampDB float64) {
	p.replayGain.mu.Lock()
	defer p.replayGain.mu.Unlock()
	p.replayGain.mode, p.replayGain.preamp = mode, preampDB
}

// applyReplayGain scales a freshly built pipeline by its track's ReplayGain,
// read from the tags at the head of a buffered stream or from a local file.
// The gain belongs to the pipeline, so a gapless transition changes it on the
// first sample of the next track.
func (p *Player) applyReplayGain(tp *trackPipeline) {
	p.replayGain.mu.Lock()
	mode, preamp := p.replayGain.mode, p.replayGain.preamp
	p.replayGain.mu.Unlock()

	if mode == "" || mode == replaygain.ModeOff {
		return
	}
	var v replaygain.Values
	switch {
	case tp.download != nil:
		v = streamReplayGain(tp.download)
	case !isURL(tp.path):
		v = replaygain.ReadFile(tp.path)
	}
	db, used, ok := v.GainFrom(mode, preamp)
	if !ok {
		return
	}
	tp.replayGainDB, tp.replayGainFrom = db, used
	if db != 0 {
		tp.stream = &gainStreamer{s: tp.stream, gain: math.Pow(10, db/20)}
	}
}

// ReplayGainMode returns the normalisation mode.
func (p *Player) ReplayGainMode() string {
	p.replayGain.mu.Lock()
	defer p.replayGain.mu.Unlock()
	return p.replayGain.mode
}

// ReplayGainApplied reports the gain applied to the current track and the
// value it came from, ModeTrack or ModeAlbum; used is "" when the track plays
// without one.
func (p *Player) ReplayGainApplied() (db float64, used string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.current == nil {
		return 0, ""
	}
	return p.current.replayGainDB, p.current.replayGainFrom
}

// gainStreamer applies a fixed linear gain.
type gainStreamer struct {
	s    beep.Streamer
	gain float64
}

func (g *gainStreamer) Stream(samples [][2]float64) (int, bool) {
	n, ok := g.s.Stream(samples)
	for i := range n {
		samples[i][0] *= g.gain
		samples[i][1] *= g.gain
	}
	return n, ok
}

func (g *gainStreamer) Err() error { return g.s.Err() }

// streamHeadSize is how much of a stream streamReplayGain reads when the
// format does not say where its tags end. Ogg keeps them in the second packet.
const streamHeadSize = 64 << 10

// streamTagLimit caps the tag section streamReplayGain waits for. A tag
// larger than this, such as one carrying very large cover art, is skipped.
const streamTagLimit = 16 << 20

// streamReplayGain reads the ReplayGain tags at the start of a downloading
// stream. ID3v2 and FLAC state where their tags end, and those bytes come
// before the first audio frame, so waiting for them adds nothing to the time
// the decoder needs anyway. Tags that are not in the head of the stream, as
// in an MP4 file with its metadata at the end, give no values.
func streamReplayGain(nb *navBuffer) replaygain.Values {
	size := streamHeadSize
	head := readStream(nb, 0, 10)
	switch {
	case len(head) == 10 && string(head[:3]) == "ID3":
		// ID3v2 header: the tag size is a 28-bit syncsafe integer, plus
		// a 10-byte footer when flag 0x10 is set.
		size = 10 + (int(head[6])<<21 | int(head[7])<<14 | int(head[8])<<7 | int(head[9]))
		if head[5]&0x10 != 0 {
			size += 10
		}
	case len(head) >= 4 && string(head[:4]) == "fLaC":
		size = flacMetadataSize(nb)
	}
	if size <= 0 || size > streamTagLimit {
		return replaygain.Values{}
	}
	m, err := tag.ReadFrom(bytes.NewReader(readStream(nb, 0, size)))
	if err != nil || m == nil {
		return replaygain.Values{}
	}
	return replaygain.FromTags(m.Raw())
}

// flacMetadataSize walks the FLAC metadata block headers and returns where the
// last block ends, or 0 when the stream ends first or the size passes the
// limit.
func flacMetadataSize(nb *navBuffer) int {
	for off := 4; off <= streamTagLimit; {
		h := readStream(nb, off, 4)
		if len(h) < 4 {
			return 0
		}
		off += 4 + (int(h[1])<<16 | int(h[2])<<8 | int(h[3]))
		if h[0]&0x80 != 0 {
			return off
		}
	}
	return 0
}

// readStream returns n bytes of the download from off, waiting for them, or
// fewer when the stream is shorter or the download fails.
func readStream(nb *navBuffer, off, n int) []byte {
	_ = nb.waitFor(int64(off+n), nil)
	nb.readMu.Lock()
	defer nb.readMu.Unlock()
	buf := make([]byte, n)
	got, _ := nb.readAt(buf, int64(off), nil)
	return buf[:got]
}
