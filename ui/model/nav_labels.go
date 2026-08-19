package model

import (
	"strings"

	"github.com/bjarneo/cliamp/provider"
)

// navLabels holds the vocabulary the browse overlay uses for the two catalog
// levels. Music providers keep the defaults; others supply their own via
// provider.BrowseLabeler.
type navLabels struct {
	artist string
	album  string
}

func (m Model) navLabels() navLabels {
	l := navLabels{artist: "Artist", album: "Album"}
	if bl, ok := m.navBrowser.prov.(provider.BrowseLabeler); ok {
		if artist, album := bl.BrowseLabels(); artist != "" && album != "" {
			l.artist, l.album = artist, album
		}
	}
	return l
}

func (l navLabels) artistsTitle() string { return l.artist + "s" }

func (l navLabels) albumsTitle() string { return l.album + "s" }

func (l navLabels) artistsLower() string { return strings.ToLower(l.artistsTitle()) }

func (l navLabels) albumsLower() string { return strings.ToLower(l.albumsTitle()) }
