package player

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/mp3"
)

// MP3 has no native gapless support. Encoders put a Xing or Info header in
// a silent first frame, and LAME (and ffmpeg's libmp3lame) adds the encoder
// delay and the end padding to it. go-mp3 ignores all of this: it decodes the
// header frame as silence and plays the delay and the padding, which leaves
// about 50 ms of silence between two gapless tracks. The decoder below trims
// them the way ffmpeg and other gapless players do.

// mp3DecoderDelay is the delay of a standard MP3 synthesis filterbank, in
// samples. The LAME delay and padding fields leave it out.
const mp3DecoderDelay = 529

// mp3SyncScan bounds the search for the first frame after the ID3v2 tag.
const mp3SyncScan = 64 << 10

// mp3FrameProbe is the number of bytes of the first frame that the parser
// reads. It covers the header, the side info, the Xing fields and the LAME tag.
const mp3FrameProbe = 256

// mp3Gapless holds the samples that a gapless decoder drops.
type mp3Gapless struct {
	skip    int // at the start: the header frame, the encoder delay and the decoder delay
	padding int // at the end
}

// decodeMP3 decodes MP3 with go-mp3 and trims the samples that the gapless
// metadata of the file marks as not part of the track.
func decodeMP3(rc io.ReadCloser) (beep.StreamSeekCloser, beep.Format, error) {
	rc, g := readMP3Gapless(rc)
	d, format, err := mp3.Decode(rc)
	if err != nil {
		return nil, beep.Format{}, err
	}
	return newMP3GaplessDecoder(d, g), format, nil
}

// readMP3Gapless reads the gapless metadata at the start of rc and returns a
// reader that starts where rc started. A seekable rc is seeked back, so
// go-mp3 can still seek it. Otherwise the bytes read are replayed.
func readMP3Gapless(rc io.ReadCloser) (io.ReadCloser, mp3Gapless) {
	if s, ok := rc.(io.ReadSeeker); ok {
		start, err := s.Seek(0, io.SeekCurrent)
		if err != nil {
			return rc, mp3Gapless{}
		}
		g := parseMP3Gapless(s)
		if _, err := s.Seek(start, io.SeekStart); err != nil {
			return rc, mp3Gapless{}
		}
		return rc, g
	}
	var prefix bytes.Buffer
	g := parseMP3Gapless(io.TeeReader(rc, &prefix))
	return replayReadCloser{Reader: io.MultiReader(&prefix, rc), Closer: rc}, g
}

// replayReadCloser reads a replayed prefix and then the rest of the source.
type replayReadCloser struct {
	io.Reader
	io.Closer
}

// parseMP3Gapless skips an ID3v2 tag, finds the first MPEG audio frame and
// parses its gapless metadata. It returns the zero value when there is none.
func parseMP3Gapless(r io.Reader) mp3Gapless {
	br := bufio.NewReaderSize(r, 4096)
	if hdr, err := br.Peek(10); err == nil && string(hdr[:3]) == "ID3" {
		size := int(hdr[6]&0x7f)<<21 | int(hdr[7]&0x7f)<<14 | int(hdr[8]&0x7f)<<7 | int(hdr[9]&0x7f)
		size += 10
		if hdr[5]&0x10 != 0 { // footer present
			size += 10
		}
		if _, err := br.Discard(size); err != nil {
			return mp3Gapless{}
		}
	}
	for range mp3SyncScan {
		hdr, err := br.Peek(4)
		if err != nil {
			return mp3Gapless{}
		}
		if mp3FrameHeaderValid(hdr) {
			frame, _ := br.Peek(mp3FrameProbe)
			return parseMP3Frame(frame)
		}
		if _, err := br.Discard(1); err != nil {
			return mp3Gapless{}
		}
	}
	return mp3Gapless{}
}

// mp3FrameHeaderValid reports whether h starts a Layer III frame header.
func mp3FrameHeaderValid(h []byte) bool {
	if len(h) < 4 || h[0] != 0xff || h[1]&0xe0 != 0xe0 {
		return false
	}
	version := (h[1] >> 3) & 3
	layer := (h[1] >> 1) & 3
	bitrate := h[2] >> 4
	rate := (h[2] >> 2) & 3
	return version != 1 && layer == 1 && bitrate != 0 && bitrate != 15 && rate != 3
}

// parseMP3Frame parses the Xing, Info or VBRI header and the LAME tag of the
// first frame. frame starts at the frame header.
func parseMP3Frame(frame []byte) mp3Gapless {
	if !mp3FrameHeaderValid(frame) {
		return mp3Gapless{}
	}
	mpeg1 := (frame[1]>>3)&3 == 3
	mono := frame[3]>>6 == 3
	samplesPerFrame := 576
	sideInfo := 9
	switch {
	case mpeg1 && mono:
		samplesPerFrame, sideInfo = 1152, 17
	case mpeg1:
		samplesPerFrame, sideInfo = 1152, 32
	case !mono:
		sideInfo = 17
	}

	// A VBRI header (Fraunhofer) sits at a fixed offset and has no delay fields.
	if len(frame) >= 40 && string(frame[36:40]) == "VBRI" {
		return mp3Gapless{skip: samplesPerFrame}
	}

	off := 4 + sideInfo
	if len(frame) < off+8 {
		return mp3Gapless{}
	}
	if tag := string(frame[off : off+4]); tag != "Xing" && tag != "Info" {
		return mp3Gapless{}
	}
	g := mp3Gapless{skip: samplesPerFrame}
	flags := binary.BigEndian.Uint32(frame[off+4:])
	off += 8
	if flags&1 != 0 { // frame count
		off += 4
	}
	if flags&2 != 0 { // byte count
		off += 4
	}
	if flags&4 != 0 { // seek table
		off += 100
	}
	if flags&8 != 0 { // quality
		off += 4
	}
	if len(frame) < off+24 {
		return g
	}
	switch string(frame[off : off+4]) {
	case "LAME", "Lavf", "Lavc":
	default:
		return g
	}
	// 9 bytes of encoder version, then revision, lowpass, replay gain (8),
	// encoding flags and bitrate. Then 12 bits of delay, 12 of padding.
	b := frame[off+21 : off+24]
	delay := int(b[0])<<4 | int(b[1])>>4
	padding := int(b[1]&0x0f)<<8 | int(b[2])
	g.skip += delay + mp3DecoderDelay
	g.padding = max(padding-mp3DecoderDelay, 0)
	return g
}

// mp3GaplessDecoder hides the samples that gapless metadata marks as not
// part of the track. Len, Position and Seek count only the track samples.
type mp3GaplessDecoder struct {
	beep.StreamSeekCloser
	skip   int
	length int // track samples; 0 when the source length is unknown
	pos    int
}

// newMP3GaplessDecoder drops the first g.skip samples of d and stops
// g.padding samples before its end. It returns d unchanged when there is
// nothing to trim or the trim does not fit d.
func newMP3GaplessDecoder(d beep.StreamSeekCloser, g mp3Gapless) beep.StreamSeekCloser {
	if g.skip == 0 && g.padding == 0 {
		return d
	}
	length := 0
	if n := d.Len(); n > 0 {
		length = n - g.skip - g.padding
		if length <= 0 {
			return d
		}
	}
	buf := make([][2]float64, 512)
	for left := g.skip; left > 0; {
		n, ok := d.Stream(buf[:min(left, len(buf))])
		left -= n
		if !ok {
			break
		}
	}
	return &mp3GaplessDecoder{StreamSeekCloser: d, skip: g.skip, length: length}
}

func (d *mp3GaplessDecoder) Stream(samples [][2]float64) (int, bool) {
	if d.length > 0 {
		left := d.length - d.pos
		if left <= 0 {
			return 0, false
		}
		if len(samples) > left {
			samples = samples[:left]
		}
	}
	n, ok := d.StreamSeekCloser.Stream(samples)
	d.pos += n
	return n, ok
}

func (d *mp3GaplessDecoder) Len() int {
	if d.length > 0 {
		return d.length
	}
	return max(d.StreamSeekCloser.Len()-d.skip, 0)
}

func (d *mp3GaplessDecoder) Position() int { return d.pos }

func (d *mp3GaplessDecoder) Seek(p int) error {
	if p < 0 || (d.length > 0 && p > d.length) {
		return fmt.Errorf("mp3: seek position %v out of range [0, %v]", p, d.length)
	}
	if err := d.StreamSeekCloser.Seek(p + d.skip); err != nil {
		return err
	}
	d.pos = p
	return nil
}
