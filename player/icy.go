package player

import (
	"io"
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
	if title := parseStreamTitle(meta); title != "" && ir.onMeta != nil {
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
