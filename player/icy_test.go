package player

import (
	"bytes"
	"io"
	"os/exec"
	"testing"
	"time"

	"github.com/gopxl/beep/v2"
)

func TestParseStreamTitle(t *testing.T) {
	tests := []struct {
		name string
		meta string
		want string
	}{
		{"artist and title", "StreamTitle='Daft Punk - Aerodynamic';StreamUrl='';", "Daft Punk - Aerodynamic"},
		{"title only", "StreamTitle='Some Show';", "Some Show"},
		{"empty title", "StreamTitle='';StreamUrl='';", ""},
		{"no stream title key", "StreamUrl='https://example.com';", ""},
		{"empty block", "", ""},
		{"missing trailing semicolon", "StreamTitle='No Semicolon'", "No Semicolon"},
		{"title containing semicolon", "StreamTitle='A; B - C';StreamUrl='';", "A; B - C"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseStreamTitle(tt.meta); got != tt.want {
				t.Errorf("parseStreamTitle(%q) = %q, want %q", tt.meta, got, tt.want)
			}
		})
	}
}

// icyBlock encodes one metadata block: a 1-byte length prefix (size/16) followed
// by the null-padded metadata, matching the SHOUTcast/Icecast wire format.
func icyBlock(meta string) []byte {
	if meta == "" {
		return []byte{0}
	}
	n := (len(meta) + 15) / 16
	out := make([]byte, 1+n*16)
	out[0] = byte(n)
	copy(out[1:], meta)
	return out
}

func TestIcyReaderStripsMetadataAndReportsTitles(t *testing.T) {
	const metaInt = 8
	var raw bytes.Buffer
	raw.WriteString("AAAAAAAA") // audio block 1
	raw.Write(icyBlock("StreamTitle='Song One';"))
	raw.WriteString("BBBBBBBB") // audio block 2
	raw.Write(icyBlock(""))     // empty metadata block (no change)
	raw.WriteString("CCCCCCCC") // audio block 3
	raw.Write(icyBlock("StreamTitle='Song Two';"))
	raw.WriteString("DDDDDDDD") // audio block 4 (partial, no trailing meta)

	var titles []string
	r := newIcyReader(io.NopCloser(&raw), metaInt, func(s string) {
		titles = append(titles, s)
	})

	// Read in small chunks to exercise the metaint-boundary clamping.
	got, err := io.ReadAll(&chunkedReader{r: r, n: 3})
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	const wantAudio = "AAAAAAAABBBBBBBBCCCCCCCCDDDDDDDD"
	if string(got) != wantAudio {
		t.Errorf("audio = %q, want %q", got, wantAudio)
	}

	wantTitles := []string{"Song One", "Song Two"}
	if len(titles) != len(wantTitles) {
		t.Fatalf("titles = %v, want %v", titles, wantTitles)
	}
	for i, w := range wantTitles {
		if titles[i] != w {
			t.Errorf("titles[%d] = %q, want %q", i, titles[i], w)
		}
	}
}

// chunkedReader caps each Read at n bytes so tests can drive a reader with many
// small reads, exercising boundary handling.
type chunkedReader struct {
	r io.Reader
	n int
}

func (c *chunkedReader) Read(p []byte) (int, error) {
	if len(p) > c.n {
		p = p[:c.n]
	}
	return c.r.Read(p)
}

// TestFFmpegPipeStreamCloseUnblocks verifies that closing the stdin-fed ffmpeg
// streamer does not hang when its source is a live stream parked in Read. os/exec
// copies src -> ffmpeg stdin in a goroutine that cmd.Wait() joins, so Close must
// close src first to unblock it. Regression guard for the AAC ICY fix.
func TestFFmpegPipeStreamCloseUnblocks(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}

	// io.Pipe with no writer simulates an open-but-idle radio body: Read blocks.
	pr, pw := io.Pipe()
	t.Cleanup(func() { pw.Close() })

	dec, _, err := decodeFFmpegPipeStream(pr, beep.SampleRate(44100), 16)
	if err != nil {
		t.Fatalf("decodeFFmpegPipeStream: %v", err)
	}

	done := make(chan struct{})
	go func() {
		dec.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close() hung: stdin-copy goroutine was not unblocked")
	}
}
