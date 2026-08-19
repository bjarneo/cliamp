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
