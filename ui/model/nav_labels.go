package model

import (
	"strings"

	"github.com/bjarneo/cliamp/provider"
)

// navLabels holds the vocabulary the browse overlay uses for the catalog
// levels. Music providers keep the defaults; others supply their own via
// provider.BrowseLabeler and provider.GenreLabeler.
type navLabels struct {
	artist string
	album  string
	genre  string
}

// navLabels returns provider-specific vocabulary for the active browse route.
func (m Model) navLabels() navLabels {
	l := navLabels{artist: "Artist", album: "Album", genre: "Genres"}
	if bl, ok := m.navBrowser.prov.(provider.BrowseLabeler); ok {
		if artist, album := bl.BrowseLabels(); artist != "" && album != "" {
			l.artist, l.album = artist, album
		}
	}
	genreSource := any(m.navBrowser.prov)
	if browser := m.navGenreBrowser(); browser != nil {
		genreSource = browser
	}
	if gl, ok := genreSource.(provider.GenreLabeler); ok {
		if genre := gl.GenreLabel(); genre != "" {
			l.genre = genre
		}
	}
	return l
}

func (l navLabels) artistsTitle() string { return l.artist + "s" }

func (l navLabels) albumsTitle() string { return l.album + "s" }

func (l navLabels) artistsLower() string { return strings.ToLower(l.artistsTitle()) }

func (l navLabels) albumsLower() string { return strings.ToLower(l.albumsTitle()) }

// genresTitle is already plural: providers name the level, not one item.
func (l navLabels) genresTitle() string { return l.genre }

func (l navLabels) genresLower() string { return strings.ToLower(l.genre) }
