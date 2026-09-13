package model

import (
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
		wantControls bool
	}{
		{"10 rows, no room for controls", 10, false},
		{"11 rows, still none", 11, false},
		{"12 rows, controls appear", 12, true},
		{"15 rows, controls stay", 15, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withFrameWidth(t, 60)
			m := newColumnTestModel(60, tt.height)

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

// Drawing the control rows is only useful if they can be focused, so the
// minimal tier's focus ring must open up with them.
func TestMinimalTierFocusFollowsControls(t *testing.T) {
	tests := []struct {
		name   string
		height int
		want   []focusArea
	}{
		{"without controls", 10, []focusArea{focusPlaylist}},
		{"with controls", 12, []focusArea{focusPlaylist, focusProvPill, focusVolume, focusEQ}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withFrameWidth(t, 60)
			m := newColumnTestModel(60, tt.height)

			got := m.mainFocusAreas()
			for _, want := range tt.want {
				if !m.mainFocusAllowed(want) {
					t.Errorf("focus %v not allowed; areas = %v", want, got)
				}
			}
			if !tt.want[len(tt.want)-1].equalsAny(focusEQ) && m.mainFocusAllowed(focusEQ) {
				t.Errorf("EQ focus allowed without a row to show it; areas = %v", got)
			}
		})
	}
}

func (f focusArea) equalsAny(others ...focusArea) bool {
	for _, o := range others {
		if f == o {
			return true
		}
	}
	return false
}
