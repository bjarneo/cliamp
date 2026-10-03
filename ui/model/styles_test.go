package model

import (
	"fmt"
	"regexp"
	"strconv"
	"testing"

	"github.com/bjarneo/cliamp/theme"
)

var truecolorFG = regexp.MustCompile(`\x1b\[38;2;(\d+);(\d+);(\d+)m`)

// renderedFG returns the foreground color lipgloss actually emitted for a
// rendered string, as lowercase CSS hex, so a test can compare it with the
// theme value it came from instead of with a style object.
func renderedFG(t *testing.T, rendered string) string {
	t.Helper()
	match := truecolorFG.FindStringSubmatch(rendered)
	if match == nil {
		t.Fatalf("rendered %q carries no truecolor foreground", rendered)
	}
	rgb := make([]int, 3)
	for i := range rgb {
		value, err := strconv.Atoi(match[i+1])
		if err != nil {
			t.Fatalf("parse color component %d of %q: %v", i, rendered, err)
		}
		rgb[i] = value
	}
	return fmt.Sprintf("#%02x%02x%02x", rgb[0], rgb[1], rgb[2])
}

// TestFavoriteMarksFollowThemeChange covers the favorite markers shown in the
// status bar after a toggle. Switching themes goes through SetTheme, which is
// where the IPC `theme` op and the theme picker both land, so the markers have
// to come out in the new theme's colors without a restart.
func TestFavoriteMarksFollowThemeChange(t *testing.T) {
	before := theme.Theme{
		Name:     "marker-before",
		Accent:   "#88aacc",
		BrightFG: "#ffffff",
		FG:       "#445566",
		Green:    "#88cc88",
		Yellow:   "#ddcc77",
		Red:      "#ee1122",
	}
	after := theme.Theme{
		Name:     "marker-after",
		Accent:   "#aa88cc",
		BrightFG: "#eeeeee",
		FG:       "#cc99aa",
		Green:    "#66bb66",
		Yellow:   "#ccbb66",
		Red:      "#3344ff",
	}

	tests := []struct {
		name       string
		mark       func() string
		wantBefore string
		wantAfter  string
	}{
		{name: "favorited", mark: favAddedMark, wantBefore: before.Red, wantAfter: after.Red},
		{name: "unfavorited", mark: favRemovedMark, wantBefore: before.FG, wantAfter: after.FG},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := Model{themes: []theme.Theme{before, after}}
			t.Cleanup(func() { applyThemeAll(theme.Default()) })

			if !m.SetTheme(before.Name) {
				t.Fatalf("SetTheme(%q) = false, want the theme to be found", before.Name)
			}
			if got := renderedFG(t, tc.mark()); got != tc.wantBefore {
				t.Fatalf("marker = %s under %s, want %s", got, before.Name, tc.wantBefore)
			}

			if !m.SetTheme(after.Name) {
				t.Fatalf("SetTheme(%q) = false, want the theme to be found", after.Name)
			}
			got := renderedFG(t, tc.mark())
			if got == tc.wantBefore {
				t.Fatalf("marker still %s from %s after switching to %s", got, before.Name, after.Name)
			}
			if got != tc.wantAfter {
				t.Fatalf("marker = %s under %s, want %s", got, after.Name, tc.wantAfter)
			}
		})
	}
}
