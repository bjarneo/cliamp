package model

import (
	"slices"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// The minimal tier gives every row it can to the playlist: no spacer above
// the footer, and the compact control rows only once the height allows them.
func TestMinimalTierRowBudget(t *testing.T) {
	tests := []struct {
		name         string
		height       int
		hideHelpBar  bool
		wantControls bool
	}{
		{"10 rows, no room for controls", 10, false, false},
		{"11 rows, still none", 11, false, false},
		{"12 rows, controls appear", 12, false, true},
		{"15 rows, controls stay", 15, false, true},
		// Hiding the hint bar frees a row, so the controls arrive one row sooner.
		{"10 rows with the hint bar hidden, still none", 10, true, false},
		{"11 rows with the hint bar hidden, controls appear", 11, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withFrameWidth(t, 60)
			m := newColumnTestModel(60, tt.height)
			if tt.hideHelpBar {
				m.SetHideHelpBar(true)
			}

			if m.layout.tier != layoutMinimal {
				t.Fatalf("tier = %v, want minimal", m.layout.tier)
			}
			if got := m.layout.minimalControls; got != tt.wantControls {
				t.Errorf("minimalControls = %v, want %v", got, tt.wantControls)
			}

			frame := stripAnsi(m.View().Content)
			if got := lipgloss.Height(frame); got > tt.height {
				t.Errorf("frame height = %d, want <= %d\n%s", got, tt.height, frame)
			}
			if got := strings.Contains(frame, "EQ ["); got != tt.wantControls {
				t.Errorf("EQ row drawn = %v, want %v\n%s", got, tt.wantControls, frame)
			}
			if got := m.effectivePlaylistVisible(); got < 1 {
				t.Errorf("playlist rows = %d, want at least one track visible", got)
			}
		})
	}
}

// Drawing the control rows is only useful if they can be focused. With them
// drawn, the minimal tier's focus ring is the compact tier's: the settings
// the rows show, the shuffle and repeat badges when the header has room for
// them, and speed, which the status line shows and highlights in every tier.
func TestMinimalTierFocusFollowsControls(t *testing.T) {
	tests := []struct {
		name   string
		height int
		want   []focusArea
	}{
		{"without controls", 10, []focusArea{focusPlaylist}},
		{"with controls", 12, []focusArea{focusPlaylist, focusProvPill, focusVolume, focusEQ, focusShuffle, focusRepeat, focusSpeed}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withFrameWidth(t, 60)
			m := newColumnTestModel(60, tt.height)

			got := m.mainFocusAreas()
			if !slices.Equal(got, tt.want) {
				t.Errorf("mainFocusAreas() = %v, want %v", got, tt.want)
			}
		})
	}
}

// The ring with the controls drawn must be the compact tier's, so a Tab stop
// added to one tier is not silently missing from the other.
func TestMinimalTierWithControlsMatchesCompactFocusRing(t *testing.T) {
	withFrameWidth(t, 60)
	minimal := newColumnTestModel(60, 12)
	compact := newColumnTestModel(60, 16)

	if minimal.layout.tier != layoutMinimal || compact.layout.tier != layoutCompact {
		t.Fatalf("tiers = %v, %v; want minimal and compact", minimal.layout.tier, compact.layout.tier)
	}
	if got, want := minimal.mainFocusAreas(), compact.mainFocusAreas(); !slices.Equal(got, want) {
		t.Errorf("minimal ring = %v, compact ring = %v", got, want)
	}
}
