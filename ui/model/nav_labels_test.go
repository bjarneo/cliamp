package model

import (
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

// labelProv supplies non-music browse vocabulary.
type labelProv struct{ plainProv }

func (p *labelProv) BrowseLabels() (string, string) { return "Author", "Book" }

func TestNavLabelsDefaultsToMusic(t *testing.T) {
	m := Model{navBrowser: navBrowserState{prov: &plainProv{}}}

	l := m.navLabels()
	if l.artistsTitle() != "Artists" || l.albumsTitle() != "Albums" {
		t.Fatalf("labels = %q/%q, want Artists/Albums", l.artistsTitle(), l.albumsTitle())
	}
	if l.artistsLower() != "artists" || l.albumsLower() != "albums" {
		t.Fatalf("lower labels = %q/%q", l.artistsLower(), l.albumsLower())
	}
}

func TestNavLabelsFromProvider(t *testing.T) {
	m := Model{navBrowser: navBrowserState{prov: &labelProv{}}}

	l := m.navLabels()
	if l.artistsTitle() != "Authors" || l.albumsTitle() != "Books" {
		t.Fatalf("labels = %q/%q, want Authors/Books", l.artistsTitle(), l.albumsTitle())
	}
	if l.artist != "Author" || l.album != "Book" {
		t.Fatalf("singular labels = %q/%q", l.artist, l.album)
	}
}

func TestNavLabelsNilProvider(t *testing.T) {
	m := Model{}

	l := m.navLabels()
	if l.artistsTitle() != "Artists" {
		t.Fatalf("labels with nil provider = %q, want Artists", l.artistsTitle())
	}
}

var _ playlist.Provider = (*labelProv)(nil)
