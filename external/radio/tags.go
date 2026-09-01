package radio

import (
	"fmt"
	"strings"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// browseTagsID is the provider-pane shortcut into the tag browser.
const browseTagsID = "browse:tags"

// tagTrackLimit caps one tag result at the same navigable size as a country.
const tagTrackLimit = 200

// tagBrowser adapts the directory's tag index to the shared category browser
// without replacing Radio's default country browser.
type tagBrowser struct {
	provider *Provider
}

var (
	_ provider.GenreBrowser = (*tagBrowser)(nil)
	_ provider.GenreLabeler = (*tagBrowser)(nil)
)

// GenreBrowserFor resolves Radio's two independent category routes.
func (p *Provider) GenreBrowserFor(entryID string) provider.GenreBrowser {
	switch entryID {
	case browseCountriesID:
		return p
	case browseTagsID:
		return &tagBrowser{provider: p}
	default:
		return nil
	}
}

func (*tagBrowser) GenreLabel() string { return "Genres & Tags" }

// Genres lists the complete Radio Browser tag index. The directory's tags are
// community-maintained: besides genres they intentionally include formats,
// eras, languages, and other descriptors that listeners may filter on.
func (b *tagBrowser) Genres() ([]provider.GenreInfo, error) {
	tags, err := b.provider.tagIndex()
	if err != nil {
		return nil, err
	}
	genres := make([]provider.GenreInfo, 0, len(tags))
	for _, tag := range tags {
		genres = append(genres, provider.GenreInfo{
			ID:   tag.Name,
			Name: fmt.Sprintf("%s (%d)", tag.Name, tag.StationCount),
		})
	}
	return genres, nil
}

func (*tagBrowser) GenreSortTypes() []provider.SortType {
	return radioSortTypes()
}

// GenreTracks loads stations carrying exactly the selected tag. Exact matching
// keeps a tag such as "rock" from also selecting "classic rock" unless a
// station explicitly carries both tags.
func (*tagBrowser) GenreTracks(tag, sortType string) ([]playlist.Track, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return nil, fmt.Errorf("radio: empty station tag")
	}
	stations, err := Stations(StationQuery{
		Tag:   tag,
		Order: sortType,
		Limit: tagTrackLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("radio: list stations tagged %q: %w", tag, err)
	}
	return stationTracks(streamableStations(stations)), nil
}

func (p *Provider) tagIndex() ([]Tag, error) {
	p.mu.Lock()
	cached := p.tags
	p.mu.Unlock()
	if cached != nil {
		return cached, nil
	}

	tags, err := FetchTags()
	if err != nil {
		return nil, fmt.Errorf("radio: list tags: %w", err)
	}
	if tags == nil {
		tags = []Tag{}
	}
	p.mu.Lock()
	p.tags = tags
	p.mu.Unlock()
	return tags, nil
}
