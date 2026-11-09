package cellwidth

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestStringMatchesAnsi(t *testing.T) {
	var cases []string
	for r := rune(0); r <= 0x2fff; r++ {
		if narrow(r) {
			cases = append(cases, string(r), "a"+string(r)+"b", string(r)+string(r))
		}
	}
	cases = append(cases,
		"",
		"\x1b[38;2;255;0;0m⣿⣿⡇\x1b[0m  ▸ Album · 1:30",
		"\x1b[1;4mbold\x1b[m",
		"wide 日本語 text",
		"emoji \u2764\ufe0f here",
		"combining e\u0301",
		"\x1b]8;;https://x\x1b\\link\x1b]8;;\x1b\\",
		"tab\there",
		"soft\u00adhyphen",
		"\x1b[",
		"\x1b[38;5",
		"bad \xff utf-8",
	)
	for _, s := range cases {
		if got, want := String(s), ansi.StringWidth(s); got != want {
			t.Errorf("%q: width %d, want %d", s, got, want)
		}
	}
}

func BenchmarkString(b *testing.B) {
	line := strings.Repeat("\x1b[38;2;120;200;255m⣿⡇⠀⢸\x1b[0m", 50)
	b.Run("cellwidth", func(b *testing.B) {
		for b.Loop() {
			String(line)
		}
	})
	b.Run("ansi", func(b *testing.B) {
		for b.Loop() {
			ansi.StringWidth(line)
		}
	})
}
