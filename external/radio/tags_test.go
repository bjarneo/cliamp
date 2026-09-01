package radio

import (
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/provider"
)

func TestFetchTagsCleansDuplicatesAndSortsByStationCount(t *testing.T) {
	d := &directory{tags: []Tag{
		{Name: "jazz", StationCount: 100},
		{Name: " Jazz ", StationCount: 25},
		{Name: "rock", StationCount: 200},
		{Name: "empty", StationCount: 0},
		{Name: "  ", StationCount: 10},
	}}
	d.serve(t)

	tags, err := FetchTags()
	if err != nil {
		t.Fatalf("FetchTags: %v", err)
	}
	if len(tags) != 2 {
		t.Fatalf("tags = %+v, want rock and jazz", tags)
	}
	if tags[0].Name != "rock" || tags[0].StationCount != 200 {
		t.Errorf("first tag = %+v, want rock with 200 stations", tags[0])
	}
	if tags[1].Name != "jazz" || tags[1].StationCount != 125 {
		t.Errorf("second tag = %+v, want merged jazz with 125 stations", tags[1])
	}
	if d.lastQuery.Get("order") != "stationcount" || d.lastQuery.Get("reverse") != "true" {
		t.Errorf("tag query = %v, want most stations first", d.lastQuery)
	}
	if d.lastQuery.Get("limit") != "100000" {
		t.Errorf("tag query limit = %q, want the complete index", d.lastQuery.Get("limit"))
	}
}

func TestTagBrowserListsTheDirectoryIndex(t *testing.T) {
	d := &directory{tags: []Tag{
		{Name: "pop", StationCount: 5723},
		{Name: "jazz", StationCount: 1134},
	}}
	d.serve(t)
	p := newPlaceProvider(t, "")

	browser := p.GenreBrowserFor(browseTagsID)
	if browser == nil {
		t.Fatal("GenreBrowserFor(tags) returned nil")
	}
	genres, err := browser.Genres()
	if err != nil {
		t.Fatalf("Genres: %v", err)
	}
	if len(genres) != 2 || genres[0].ID != "pop" || genres[0].Name != "pop (5723)" {
		t.Fatalf("genres = %+v, want directory tags with station counts", genres)
	}
	labeler, ok := browser.(provider.GenreLabeler)
	if !ok {
		t.Fatal("tag browser does not implement GenreLabeler")
	}
	if got := labeler.GenreLabel(); got != "Genres & Tags" {
		t.Fatalf("tag browser label = %q, want Genres & Tags", got)
	}
}

func TestTagBrowserLoadsExactTagWithSelectedSort(t *testing.T) {
	d := &directory{stations: []CatalogStation{
		{Name: "Jazz FM", URL: "https://jazz.example/stream", Tags: "jazz,smooth jazz"},
	}}
	d.serve(t)
	p := newPlaceProvider(t, "")
	browser := p.GenreBrowserFor(browseTagsID)

	tracks, err := browser.GenreTracks("jazz", SortTrend)
	if err != nil {
		t.Fatalf("GenreTracks: %v", err)
	}
	if len(tracks) != 1 || tracks[0].Title != "Jazz FM" {
		t.Fatalf("tracks = %+v, want Jazz FM", tracks)
	}
	if d.lastQuery.Get("tag") != "jazz" || d.lastQuery.Get("tagExact") != "true" {
		t.Errorf("query = %v, want exact jazz tag", d.lastQuery)
	}
	if d.lastQuery.Get("order") != SortTrend || d.lastQuery.Get("limit") != "200" {
		t.Errorf("query = %v, want trending order and 200 results", d.lastQuery)
	}
}

func TestTagBrowserRejectsAnEmptyTag(t *testing.T) {
	p := newPlaceProvider(t, "")
	browser := p.GenreBrowserFor(browseTagsID)
	if _, err := browser.GenreTracks("  ", SortVotes); err == nil {
		t.Fatal("GenreTracks should reject an empty tag")
	}
}

func TestRadioBrowseEntriesOfferCountriesAndTags(t *testing.T) {
	p := newPlaceProvider(t, "")
	entries := p.BrowseEntries()
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	if !slices.Contains(ids, browseCountriesID) || !slices.Contains(ids, browseTagsID) {
		t.Fatalf("browse entries = %v, want countries and tags", ids)
	}
	if p.GenreBrowserFor("browse:unknown") != nil {
		t.Fatal("unknown browse route should not resolve a genre browser")
	}
}
