package theme

import (
	"math"
	"strconv"
	"strings"
	"testing"
)

func TestBuiltinThemesMeetTextContrast(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	themes := LoadAll()
	if len(themes) != 21 {
		t.Fatalf("LoadAll() returned %d built-in themes, want 21", len(themes))
	}

	for _, th := range themes {
		if th.BG == "" {
			t.Errorf("built-in theme %q has no background", th.Name)
			continue
		}
		for _, role := range []struct {
			name  string
			color string
		}{
			{"accent", th.Accent},
			{"bright_fg", th.BrightFG},
			{"fg", th.FG},
			{"green", th.Green},
			{"yellow", th.Yellow},
			{"red", th.Red},
		} {
			if ratio := contrastRatio(role.color, th.BG); ratio < 4.5 {
				t.Errorf("theme %q %s contrast = %.2f:1, want at least 4.5:1", th.Name, role.name, ratio)
			}
		}
	}
}

func contrastRatio(a, b string) float64 {
	lighter := relativeLuminance(a)
	darker := relativeLuminance(b)
	if lighter < darker {
		lighter, darker = darker, lighter
	}
	return (lighter + 0.05) / (darker + 0.05)
}

func relativeLuminance(hex string) float64 {
	value, err := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 24)
	if err != nil {
		return 0
	}
	linear := func(channel uint64) float64 {
		component := float64(channel) / 255
		if component <= 0.04045 {
			return component / 12.92
		}
		return math.Pow((component+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(value>>16) + 0.7152*linear((value>>8)&0xff) + 0.0722*linear(value&0xff)
}
