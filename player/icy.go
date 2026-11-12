package player

import (
	"io"
	"regexp"
	"strings"
)

// Compile-time interface check.
var _ io.ReadCloser = (*icyReader)(nil)

// icyReader strips interleaved ICY metadata from a SHOUTcast/Icecast stream.
//
// The server sends icy-metaint bytes of audio, then a 1-byte length prefix
// (multiply by 16 = metadata size), then the metadata block, then repeats.
// This reader transparently removes the metadata so decoders only see audio.
type icyReader struct {
	r         io.ReadCloser
	metaInt   int          // audio bytes between metadata blocks
	remaining int          // audio bytes left before next metadata block
	onMeta    func(string) // called with parsed StreamTitle
}

func newIcyReader(r io.ReadCloser, metaInt int, onMeta func(string)) *icyReader {
	return &icyReader{
		r:         r,
		metaInt:   metaInt,
		remaining: metaInt,
		onMeta:    onMeta,
	}
}

func (ir *icyReader) Read(p []byte) (int, error) {
	if ir.remaining == 0 {
		// Read and discard the metadata block.
		if err := ir.consumeMeta(); err != nil {
			return 0, err
		}
		ir.remaining = ir.metaInt
	}

	// Clamp the read so we never cross into a metadata block.
	p = p[:min(len(p), ir.remaining)]
	n, err := ir.r.Read(p)
	ir.remaining -= n
	return n, err
}

func (ir *icyReader) Close() error {
	return ir.r.Close()
}

// consumeMeta reads the 1-byte length prefix, then the metadata block,
// and parses StreamTitle from it.
func (ir *icyReader) consumeMeta() error {
	// Length prefix: 1 byte, multiply by 16 for actual size.
	var lenBuf [1]byte
	if _, err := io.ReadFull(ir.r, lenBuf[:]); err != nil {
		return err
	}
	metaLen := int(lenBuf[0]) * 16
	if metaLen == 0 {
		return nil
	}

	buf := make([]byte, metaLen)
	if _, err := io.ReadFull(ir.r, buf); err != nil {
		return err
	}

	// Metadata is null-padded; trim before parsing.
	meta := strings.TrimRight(string(buf), "\x00")
	if title := cleanStreamTitle(parseStreamTitle(meta)); title != "" && ir.onMeta != nil {
		ir.onMeta(title)
	}
	return nil
}

// parseStreamTitle extracts the StreamTitle value from ICY metadata.
// Format: "StreamTitle='Artist - Title';StreamUrl='...';..."
func parseStreamTitle(meta string) string {
	const prefix = "StreamTitle='"
	_, after, ok := strings.Cut(meta, prefix)
	if !ok {
		return ""
	}
	j := strings.Index(after, "';")
	if j < 0 {
		// Tolerate missing semicolon at end of block.
		j = strings.LastIndex(after, "'")
		if j < 0 {
			return ""
		}
	}
	return after[:j]
}

// iheartRecord matches the cart record that iHeartRadio stations send as the
// StreamTitle: `Artist - text="Title" song_spot="M" length="00:04:15" ...`.
// The title ends at the last quote before the key="value" pairs, so a title
// can hold a quote or " - ".
var iheartRecord = regexp.MustCompile(`^(.*?)\s*-\s+text="(.*?)"(?:\s+\w+="[^"]*")+\s*$`)

// cleanStreamTitle reduces an iHeartRadio cart record to "Artist - Title".
// When one part is empty, it returns the other part. When both parts are
// empty, it returns "". It returns any other title unchanged.
func cleanStreamTitle(title string) string {
	m := iheartRecord.FindStringSubmatch(title)
	if m == nil {
		return title
	}
	artist, song := strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
	switch {
	case artist == "":
		return song
	case song == "":
		return artist
	}
	return artist + " - " + song
}
