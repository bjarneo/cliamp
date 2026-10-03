package jellyfin

import (
	"testing"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// The shared provider body is tested in internal/embyapi. These tests cover
// what the Jellyfin package adds.

func TestProviderName(t *testing.T) {
	p := newProvider(NewClient("https://jf.example.com", "tok", "user-1", "", ""))
	if p.Name() != "Jellyfin" {
		t.Fatalf("Name() = %q, want Jellyfin", p.Name())
	}
}

func TestProviderDefaultBrowseMode(t *testing.T) {
	p := newProvider(NewClient("https://jf.example.com", "tok", "user-1", "", ""))
	if got := p.DefaultBrowseMode(); got != provider.BrowseArtistAlbums {
		t.Fatalf("DefaultBrowseMode() = %d, want BrowseArtistAlbums", got)
	}
}

func TestProviderCanReportPlayback(t *testing.T) {
	p := newProvider(NewClient("https://jf.example.com", "tok", "user-1", "", ""))
	tests := []struct {
		key  string
		want bool
	}{
		{provider.MetaJellyfinID, true},
		{provider.MetaEmbyID, false},
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
		cfg  config.JellyfinConfig
		want bool
	}{
		{"empty", config.JellyfinConfig{}, false},
		{"token", config.JellyfinConfig{URL: "https://jf.example.com", Token: "tok"}, true},
		{"password", config.JellyfinConfig{URL: "https://jf.example.com", User: "u", Password: "p"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewFromConfig(tt.cfg) != nil; got != tt.want {
				t.Fatalf("NewFromConfig() returned a provider = %v, want %v", got, tt.want)
			}
		})
	}
}
