package playlist

import (
	"bytes"
	"encoding/binary"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// maxWAVInfoSize limits the LIST chunk that readWAVInfo loads. A larger
// LIST chunk is skipped.
const maxWAVInfoSize = 1 << 20

// readWAVInfo reads the tags of a WAV file from its LIST INFO chunk.
// dhowden/tag does not read WAV files. ok is false when r is not a WAV file
// or its INFO chunk has no title.
func readWAVInfo(r io.ReadSeeker, path string) (Track, bool) {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return Track{}, false
	}
	var header [12]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return Track{}, false
	}
	if string(header[0:4]) != "RIFF" || string(header[8:12]) != "WAVE" {
		return Track{}, false
	}
	for {
		var chunk [8]byte
		if _, err := io.ReadFull(r, chunk[:]); err != nil {
			return Track{}, false
		}
		size := int64(binary.LittleEndian.Uint32(chunk[4:8]))
		// Each chunk has an even size on disk.
		padded := size + size%2
		if string(chunk[0:4]) != "LIST" || size < 4 || size > maxWAVInfoSize {
			if _, err := r.Seek(padded, io.SeekCurrent); err != nil {
				return Track{}, false
			}
			continue
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(r, data); err != nil {
			return Track{}, false
		}
		if string(data[0:4]) != "INFO" {
			if _, err := r.Seek(padded-size, io.SeekCurrent); err != nil {
				return Track{}, false
			}
			continue
		}
		return wavInfoTrack(data[4:], path)
	}
}

// wavInfoTrack builds a Track from the fields of a LIST INFO chunk.
func wavInfoTrack(data []byte, path string) (Track, bool) {
	fields := map[string]string{}
	for len(data) >= 8 {
		id := string(data[0:4])
		size := int(binary.LittleEndian.Uint32(data[4:8]))
		data = data[8:]
		if size > len(data) {
			break
		}
		fields[id] = wavInfoString(data[:size])
		data = data[min(size+size%2, len(data)):]
	}
	if fields["INAM"] == "" {
		return Track{}, false
	}
	t := Track{
		Path:   path,
		Title:  fields["INAM"],
		Artist: fields["IART"],
		Album:  fields["IPRD"],
		Genre:  fields["IGNR"],
		Year:   leadingNumber(fields["ICRD"]),
	}
	t.TrackNumber = leadingNumber(fields["ITRK"])
	if t.TrackNumber == 0 {
		t.TrackNumber = leadingNumber(fields["IPRT"])
	}
	return t, true
}

// wavInfoString returns an INFO value up to its NUL. A value that is not
// UTF-8 is read as Latin-1, the usual encoding of older RIFF writers.
func wavInfoString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	if !utf8.Valid(b) {
		if decoded, err := charmap.ISO8859_1.NewDecoder().Bytes(b); err == nil {
			b = decoded
		}
	}
	return sanitizeTag(strings.TrimSpace(string(b)))
}

// leadingNumber returns the number at the start of s, such as 2024 in
// "2024-05-01" or 3 in "3/12". It returns 0 when s starts with no digit.
func leadingNumber(s string) int {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, _ := strconv.Atoi(s[:end])
	return n
}
