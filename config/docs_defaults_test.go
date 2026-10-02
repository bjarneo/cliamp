package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestDocsNameAudioDefaults checks the audio defaults that the flag table of
// cli.md and the defaults block of audio-quality.md name against
// defaultConfig.
func TestDocsNameAudioDefaults(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("..", "docs", name))
		if err != nil {
			t.Fatalf("read docs/%s: %v", name, err)
		}
		// A Windows checkout can turn the line ends into CRLF.
		return strings.ReplaceAll(string(data), "\r\n", "\n")
	}
	cli := read("cli.md")
	quality := read("audio-quality.md")

	def := defaultConfig()
	tests := []struct {
		flag string
		key  string
		want int
	}{
		{"--sample-rate", "sample_rate", def.SampleRate},
		{"--buffer-ms", "buffer_ms", def.BufferMs},
		{"--resample-quality", "resample_quality", def.ResampleQuality},
		{"--bit-depth", "bit_depth", def.BitDepth},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			want := strconv.Itoa(tt.want)
			row := regexp.MustCompile("(?m)^\\| `" + regexp.QuoteMeta(tt.flag) + "` \\| int \\| ([^|]*?) \\|")
			if m := row.FindStringSubmatch(cli); m == nil {
				t.Errorf("docs/cli.md has no row for %s", tt.flag)
			} else if m[1] != want {
				t.Errorf("docs/cli.md gives %s the default %q, want %q", tt.flag, m[1], want)
			}
			// The first assignment of the key is in the defaults block.
			line := regexp.MustCompile(`(?m)^` + tt.key + ` = (\d+)$`)
			if m := line.FindStringSubmatch(quality); m == nil {
				t.Errorf("docs/audio-quality.md does not set %s", tt.key)
			} else if m[1] != want {
				t.Errorf("docs/audio-quality.md gives %s the default %s, want %s", tt.key, m[1], want)
			}
		})
	}
}
