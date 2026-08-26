package model

import (
	"strings"

	"github.com/bjarneo/cliamp/provider"
)

// navSortLabel resolves a sort-type ID to its human label for the active
// provider's album browser, falling back to the raw ID.
func (m Model) navSortLabel(sortID string) string {
	if ab, ok := m.navBrowser.prov.(provider.AlbumBrowser); ok {
		for _, st := range ab.AlbumSortTypes() {
			if st.ID == sortID {
				return st.Label
			}
		}
	}
	return sortID
}

func (m Model) navSourceName() string {
	if m.navBrowser.prov != nil {
		return m.navBrowser.prov.Name()
	}
	return "Provider"
}

// navBreadcrumb keeps source, location, and scope visible while drilling into
// a provider catalog. The separator header clips the completed path safely.
func (m Model) navBreadcrumb() string {
	labels := m.navLabels()
	parts := []string{m.navSourceName()}
	switch m.navView() {
	case navViewArtists:
		parts = append(parts, labels.artistsTitle())
	case navViewAlbums:
		if m.navBrowser.mode == navBrowseModeByArtistAlbum && m.navBrowser.selArtist.Name != "" {
			parts = append(parts, m.navBrowser.selArtist.Name)
		}
		parts = append(parts, labels.albumsTitle())
		if m.navBrowser.mode == navBrowseModeByAlbum {
			if sort := m.navSortLabel(m.navBrowser.sortType); sort != "" {
				parts = append(parts, "Sort: "+sort)
			}
		}
	case navViewTracks:
		switch m.navBrowser.mode {
		case navBrowseModeByArtist:
			if m.navBrowser.selArtist.Name != "" {
				parts = append(parts, m.navBrowser.selArtist.Name)
			}
		case navBrowseModeByAlbum:
			if m.navBrowser.selAlbum.Name != "" {
				parts = append(parts, m.navBrowser.selAlbum.Name)
			}
		case navBrowseModeByArtistAlbum:
			if m.navBrowser.selArtist.Name != "" {
				parts = append(parts, m.navBrowser.selArtist.Name)
			}
			if m.navBrowser.selAlbum.Name != "" {
				parts = append(parts, m.navBrowser.selAlbum.Name)
			}
		case navBrowseModeByGenre:
			parts = append(parts, "Genres")
			if m.navBrowser.selGenre.Name != "" {
				parts = append(parts, m.navBrowser.selGenre.Name)
			}
			if m.navBrowser.selGenreSort.Label != "" {
				parts = append(parts, m.navBrowser.selGenreSort.Label)
			}
		}
		parts = append(parts, "Tracks")
	case navViewGenres:
		parts = append(parts, "Genres")
		if m.navBrowser.genreQuery != "" {
			parts = append(parts, "Search: "+m.navBrowser.genreQuery)
		}
	case navViewGenreSorts:
		parts = append(parts, "Genres")
		if m.navBrowser.selGenre.Name != "" {
			parts = append(parts, m.navBrowser.selGenre.Name)
		}
	default:
		parts = append(parts, "Browse")
	}
	return strings.Join(parts, " / ")
}
