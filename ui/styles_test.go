package ui

import "testing"

func TestContrastingTextColor(t *testing.T) {
	tests := []struct {
		name   string
		accent string
		want   string
	}{
		{name: "light accent", accent: "#f7df50", want: "#000000"},
		{name: "dark accent", accent: "#3e4a5e", want: "#ffffff"},
		{name: "invalid accent", accent: "blue", want: "#ffffff"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := contrastingTextColor(tt.accent); got != tt.want {
				t.Errorf("contrastingTextColor(%q) = %q, want %q", tt.accent, got, tt.want)
			}
		})
	}
}

func TestAccentLacksHue(t *testing.T) {
	tests := []struct {
		name   string
		accent string
		text   string
		want   bool
	}{
		{name: "gray accent on dark text", accent: "#6e6e6e", text: "#000000", want: true},
		{name: "gray accent on light text", accent: "#ececec", text: "#ffffff", want: true},
		{name: "accent same as text", accent: "#dcd7ba", text: "#dcd7ba", want: true},
		{name: "blue accent on lavender text", accent: "#89b4fa", text: "#cdd6f4", want: false},
		{name: "green accent on white text", accent: "#00ff00", text: "#ffffff", want: false},
		{name: "invalid accent", accent: "blue", text: "#ffffff", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := accentLacksHue(tt.accent, tt.text); got != tt.want {
				t.Errorf("accentLacksHue(%q, %q) = %v, want %v", tt.accent, tt.text, got, tt.want)
			}
		})
	}
}
