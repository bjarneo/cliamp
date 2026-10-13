package player

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// mp3TestFrame builds the start of a first frame. hdr is the 4-byte frame
// header, tag is written at the Xing offset, and lame (when set) adds a LAME
// tag with the given delay and padding after a Xing header with no fields.
func mp3TestFrame(hdr [4]byte, sideInfo int, tag string, lame bool, delay, padding int) []byte {
	f := make([]byte, mp3FrameProbe)
	copy(f, hdr[:])
	off := 4 + sideInfo
	if tag == "VBRI" {
		off = 36
	}
	copy(f[off:], tag)
	off += 8 // tag and zero flags
	if lame {
		copy(f[off:], "LAME3.100")
		f[off+21] = byte(delay >> 4)
		f[off+22] = byte(delay&0x0f)<<4 | byte(padding>>8)
		f[off+23] = byte(padding)
	}
	return f
}

var (
	mpeg1Stereo = [4]byte{0xff, 0xfb, 0x90, 0x00}
	mpeg1Mono   = [4]byte{0xff, 0xfb, 0x90, 0xc0}
	mpeg2Stereo = [4]byte{0xff, 0xf3, 0x90, 0x00}
)

func TestParseMP3Frame(t *testing.T) {
	tests := []struct {
		name  string
		frame []byte
		want  mp3Gapless
	}{
		{"info with lame", mp3TestFrame(mpeg1Stereo, 32, "Info", true, 576, 672), mp3Gapless{skip: 1152 + 576 + 529, padding: 672 - 529}},
		{"xing mono with lame", mp3TestFrame(mpeg1Mono, 17, "Xing", true, 576, 1000), mp3Gapless{skip: 1152 + 576 + 529, padding: 1000 - 529}},
		{"mpeg2 with lame", mp3TestFrame(mpeg2Stereo, 17, "Info", true, 576, 600), mp3Gapless{skip: 576 + 576 + 529, padding: 600 - 529}},
		{"padding below decoder delay", mp3TestFrame(mpeg1Stereo, 32, "Info", true, 576, 100), mp3Gapless{skip: 1152 + 576 + 529}},
		{"xing without lame", mp3TestFrame(mpeg1Stereo, 32, "Xing", false, 0, 0), mp3Gapless{skip: 1152}},
		{"vbri", mp3TestFrame(mpeg1Stereo, 32, "VBRI", false, 0, 0), mp3Gapless{skip: 1152}},
		{"audio frame", mp3TestFrame(mpeg1Stereo, 32, "", false, 0, 0), mp3Gapless{}},
		{"xing at wrong offset", mp3TestFrame(mpeg1Stereo, 17, "Xing", true, 576, 672), mp3Gapless{}},
		{"not a frame", []byte("not an mp3 frame"), mp3Gapless{}},
		{"truncated", mp3TestFrame(mpeg1Stereo, 32, "Info", true, 576, 672)[:40], mp3Gapless{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseMP3Frame(tt.frame); got != tt.want {
				t.Errorf("parseMP3Frame = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseMP3GaplessSkipsID3AndJunk(t *testing.T) {
	frame := mp3TestFrame(mpeg1Stereo, 32, "Info", true, 576, 672)
	id3 := []byte{'I', 'D', '3', 4, 0, 0, 0, 0, 0x01, 0x00} // 128-byte tag
	var b bytes.Buffer
	b.Write(id3)
	b.Write(make([]byte, 128))
	b.Write([]byte{0, 0, 0xff, 0x00}) // junk with a false sync byte
	b.Write(frame)
	want := mp3Gapless{skip: 1152 + 576 + 529, padding: 672 - 529}
	if got := parseMP3Gapless(&b); got != want {
		t.Errorf("parseMP3Gapless = %+v, want %+v", got, want)
	}
}

// nonSeekableFile hides the Seek method of an *os.File, like an ssh pipe.
type nonSeekableFile struct{ f *os.File }

func (n nonSeekableFile) Read(p []byte) (int, error) { return n.f.Read(p) }
func (n nonSeekableFile) Close() error               { return n.f.Close() }

func TestDecodeMP3TrimsGaplessSamples(t *testing.T) {
	path := filepath.Join("testdata", "cliamp_whips_terminal_ass.mp3")
	// ffmpeg -i cliamp_whips_terminal_ass.mp3 -f s16le - | wc -c, over 4 bytes.
	// The file has a LAME tag with delay 576 and padding 738.
	const want = 102366

	drain := func(rc io.ReadCloser) ([][2]float64, int) {
		t.Helper()
		d, _, err := decodeWithExt(rc, ".mp3")
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close()
		var out [][2]float64
		buf := make([][2]float64, 1000)
		for {
			n, ok := d.Stream(buf)
			out = append(out, buf[:n]...)
			if !ok {
				break
			}
		}
		if d.Position() != len(out) {
			t.Errorf("Position = %d after %d samples", d.Position(), len(out))
		}
		return out, d.Len()
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	seekable, n := drain(f)
	if len(seekable) != want || n != want {
		t.Errorf("seekable: streamed %d samples, Len %d, want %d", len(seekable), n, want)
	}

	// A non-seekable source has no known length, so only the start is trimmed.
	f, err = os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	piped, _ := drain(nonSeekableFile{f})
	if len(piped) != want+738-mp3DecoderDelay {
		t.Errorf("non-seekable: streamed %d samples, want %d", len(piped), want+738-mp3DecoderDelay)
	}
	for i := range 2000 {
		if piped[i] != seekable[i] {
			t.Fatalf("non-seekable sample %d = %v, seekable %v", i, piped[i], seekable[i])
		}
	}
}

func TestMP3GaplessDecoderSeek(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "cliamp_whips_terminal_ass.mp3"))
	if err != nil {
		t.Fatal(err)
	}
	d, _, err := decodeWithExt(f, ".mp3")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	if err := d.Seek(5000); err != nil {
		t.Fatal(err)
	}
	if d.Position() != 5000 {
		t.Errorf("Position after Seek(5000) = %d", d.Position())
	}
	if err := d.Seek(d.Len()); err != nil {
		t.Fatal(err)
	}
	if n, ok := d.Stream(make([][2]float64, 10)); n != 0 || ok {
		t.Errorf("Stream at end = (%d, %v), want (0, false)", n, ok)
	}
	if err := d.Seek(d.Len() + 1); err == nil {
		t.Error("Seek past end succeeded")
	}
}
