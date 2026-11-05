package player

import (
	"bytes"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bjarneo/cliamp/internal/replaygain"
)

// id3TXXX builds an ID3v2.3 TXXX frame with a Latin-1 description and value.
func id3TXXX(desc, value string) []byte {
	body := append([]byte{0}, desc...)
	body = append(append(body, 0), value...)
	f := append([]byte("TXXX"), 0, 0, 0, 0, 0, 0)
	binary.BigEndian.PutUint32(f[4:8], uint32(len(body)))
	return append(f, body...)
}

// id3Frame builds an ID3v2.3 frame with a raw body.
func id3Frame(id string, body []byte) []byte {
	f := append([]byte(id), 0, 0, 0, 0, 0, 0)
	binary.BigEndian.PutUint32(f[4:8], uint32(len(body)))
	return append(f, body...)
}

// id3Tag wraps frames in an ID3v2.3 header, followed by padding and audio.
func id3Tag(padding int, frames ...[]byte) []byte {
	var body []byte
	for _, f := range frames {
		body = append(body, f...)
	}
	body = append(body, make([]byte, padding)...)
	n := len(body)
	h := []byte{'I', 'D', '3', 3, 0, 0, byte(n >> 21 & 0x7f), byte(n >> 14 & 0x7f), byte(n >> 7 & 0x7f), byte(n & 0x7f)}
	return append(append(h, body...), bytes.Repeat([]byte{0xff, 0xfb}, 4096)...)
}

// flacFile builds a FLAC stream: STREAMINFO, a padding block, then a Vorbis
// comment block holding comments.
func flacFile(padding int, comments ...string) []byte {
	block := func(typ byte, last bool, body []byte) []byte {
		if last {
			typ |= 0x80
		}
		n := len(body)
		return append([]byte{typ, byte(n >> 16), byte(n >> 8), byte(n)}, body...)
	}
	vc := binary.LittleEndian.AppendUint32(nil, 6)
	vc = append(vc, "cliamp"...)
	vc = binary.LittleEndian.AppendUint32(vc, uint32(len(comments)))
	for _, c := range comments {
		vc = binary.LittleEndian.AppendUint32(vc, uint32(len(c)))
		vc = append(vc, c...)
	}
	out := []byte("fLaC")
	out = append(out, block(0, false, make([]byte, 34))...)
	out = append(out, block(1, false, make([]byte, padding))...)
	out = append(out, block(4, true, vc)...)
	return append(out, bytes.Repeat([]byte{0xff, 0xf8}, 4096)...)
}

// The tags at the head of a downloading stream give its ReplayGain, even when
// they end beyond the fixed head size.
func TestStreamReplayGain(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		want replaygain.Values
	}{
		{
			// Cover art usually comes first, pushing the gain past the fixed head.
			name: "ID3v2 TXXX frames after large cover art",
			body: id3Tag(0, id3Frame("APIC", make([]byte, 200<<10)), id3TXXX("REPLAYGAIN_TRACK_GAIN", "-6.99 dB"), id3TXXX("REPLAYGAIN_TRACK_PEAK", "0.999969")),
			want: replaygain.Values{TrackGain: -6.99, TrackPeak: 0.999969, HasTrack: true},
		},
		{
			name: "FLAC Vorbis comments after a large block",
			body: flacFile(300<<10, "REPLAYGAIN_ALBUM_GAIN=-4.20 dB", "REPLAYGAIN_ALBUM_PEAK=0.95"),
			want: replaygain.Values{AlbumGain: -4.2, AlbumPeak: 0.95, HasAlbum: true},
		},
		{
			name: "ID3v2 without ReplayGain",
			body: id3Tag(0, id3TXXX("fBPM", "140")),
		},
		{
			name: "no tags at all",
			body: bytes.Repeat([]byte{0xff, 0xfb}, 40000),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(tt.body)
			}))
			defer srv.Close()
			nb, _, err := newNavBuffer(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer nb.Close()

			if got := streamReplayGain(nb); got != tt.want {
				t.Fatalf("streamReplayGain = %+v, want %+v", got, tt.want)
			}
		})
	}
}
