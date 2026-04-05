package theme

import (
	"strings"
	"testing"
)

func TestDefault(t *testing.T) {
	d := Default()
	if d.Name != DefaultName {
		t.Errorf("Name = %q, want %q", d.Name, DefaultName)
	}
	if !d.IsDefault() {
		t.Error("IsDefault() should be true for default theme")
	}
}

func TestIsDefault(t *testing.T) {
	tests := []struct {
		name  string
		theme Theme
		want  bool
	}{
		{"empty hex values", Theme{Name: "Default"}, true},
		{"has accent", Theme{Name: "Custom", Accent: "#ff0000"}, false},
		{"has green", Theme{Name: "Custom", Green: "#00ff00"}, false},
		{"has bright fg", Theme{Name: "Custom", BrightFG: "#ffffff"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.theme.IsDefault(); got != tt.want {
				t.Errorf("IsDefault() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParse(t *testing.T) {
	input := `# Solarized Dark theme
accent = "#268bd2"
bright_fg = "#93a1a1"
fg = "#839496"
green = "#859900"
yellow = "#b58900"
red = "#dc322f"
`
	th, err := Parse("solarized-dark", strings.NewReader(input))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if th.Name != "solarized-dark" {
		t.Errorf("Name = %q, want solarized-dark", th.Name)
	}
	if th.Accent != "#268bd2" {
		t.Errorf("Accent = %q, want #268bd2", th.Accent)
	}
	if th.BrightFG != "#93a1a1" {
		t.Errorf("BrightFG = %q, want #93a1a1", th.BrightFG)
	}
	if th.FG != "#839496" {
		t.Errorf("FG = %q, want #839496", th.FG)
	}
	if th.Green != "#859900" {
		t.Errorf("Green = %q, want #859900", th.Green)
	}
	if th.Yellow != "#b58900" {
		t.Errorf("Yellow = %q, want #b58900", th.Yellow)
	}
	if th.Red != "#dc322f" {
		t.Errorf("Red = %q, want #dc322f", th.Red)
	}
}

func TestParseSkipsComments(t *testing.T) {
	input := `# comment
accent = "#ff0000"
# another comment
`
	th, err := Parse("test", strings.NewReader(input))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if th.Accent != "#ff0000" {
		t.Errorf("Accent = %q, want #ff0000", th.Accent)
	}
}

func TestParseEmpty(t *testing.T) {
	th, err := Parse("empty", strings.NewReader(""))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if !th.IsDefault() {
		t.Error("empty parse should produce default-like theme")
	}
}

func TestParseStripsQuotes(t *testing.T) {
	// Both single and double quotes should be stripped
	input := `accent = '#ff0000'
fg = "#00ff00"
`
	th, err := Parse("test", strings.NewReader(input))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if th.Accent != "#ff0000" {
		t.Errorf("Accent = %q, want #ff0000", th.Accent)
	}
	if th.FG != "#00ff00" {
		t.Errorf("FG = %q, want #00ff00", th.FG)
	}
}

func TestParsedThemeNotDefault(t *testing.T) {
	input := `accent = "#ff0000"`
	th, err := Parse("custom", strings.NewReader(input))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if th.IsDefault() {
		t.Error("theme with accent should not be IsDefault()")
	}
}
