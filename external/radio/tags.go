package radio

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"

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

// GenreLabel names the tag catalogue in the shared category browser.
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
		label := sanitizeTagLabel(tag.Name)
		if label == "" {
			continue
		}
		genres = append(genres, provider.GenreInfo{
			ID:   tag.Name,
			Name: fmt.Sprintf("%s (%d)", label, tag.StationCount),
		})
	}
	return genres, nil
}

// GenreSortTypes returns the station orders available for one tag.
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

// sanitizeTagLabel removes terminal escape sequences and control characters
// from a remote directory tag while retaining readable Unicode text.
func sanitizeTagLabel(name string) string {
	name = ansi.Strip(name)
	var b strings.Builder
	b.Grow(len(name))
	lastWasSpace := false
	for _, r := range name {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			if !lastWasSpace {
				b.WriteByte(' ')
				lastWasSpace = true
			}
		case unicode.IsControl(r) || (r >= 0x80 && r <= 0x9f):
		case unicode.IsSpace(r):
			if !lastWasSpace {
				b.WriteByte(' ')
				lastWasSpace = true
			}
		default:
			b.WriteRune(r)
			lastWasSpace = false
		}
	}
	return strings.TrimSpace(b.String())
}

// tagIndex returns the cached directory tags, fetching them when necessary.
// A generation check prevents an in-flight request from undoing Refresh.
func (p *Provider) tagIndex() ([]Tag, error) {
	p.mu.Lock()
	cached := p.tags
	generation := p.tagGeneration
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
	if p.tagGeneration == generation && p.tags == nil {
		p.tags = tags
	}
	p.mu.Unlock()
	return tags, nil
}
