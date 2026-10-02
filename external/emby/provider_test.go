package emby

import (
	"testing"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// The shared provider body is tested in internal/embyapi. These tests cover
// what the Emby package adds.

func TestProviderName(t *testing.T) {
	p := newProvider(NewClient("https://emby.example.com", "tok", "user-1", "", ""))
	if p.Name() != "Emby" {
		t.Fatalf("Name() = %q, want Emby", p.Name())
	}
}

// Emby opens as a flat album list. A default browse mode would open the
// artist browser on switch and take the N key for the mode chooser.
func TestProviderHasNoDefaultBrowseMode(t *testing.T) {
	var p any = newProvider(NewClient("https://emby.example.com", "tok", "user-1", "", ""))
	if _, ok := p.(provider.DefaultBrowseModeProvider); ok {
		t.Fatal("Emby implements DefaultBrowseModeProvider, want a flat album list")
	}
}

func TestProviderCanReportPlayback(t *testing.T) {
	p := newProvider(NewClient("https://emby.example.com", "tok", "user-1", "", ""))
	tests := []struct {
		key  string
		want bool
	}{
		{provider.MetaEmbyID, true},
		{provider.MetaJellyfinID, false},
		{provider.MetaNavidromeID, false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			track := playlist.Track{ProviderMeta: map[string]string{tt.key: "track-1"}}
			if got := p.CanReportPlayback(track); got != tt.want {
				t.Fatalf("CanReportPlayback() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewFromConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.EmbyConfig
		want bool
	}{
		{"empty", config.EmbyConfig{}, false},
		{"token", config.EmbyConfig{URL: "https://emby.example.com", Token: "tok"}, true},
		{"password", config.EmbyConfig{URL: "https://emby.example.com", User: "u", Password: "p"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewFromConfig(tt.cfg) != nil; got != tt.want {
				t.Fatalf("NewFromConfig() returned a provider = %v, want %v", got, tt.want)
			}
		})
	}
}
