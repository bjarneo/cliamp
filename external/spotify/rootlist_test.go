package spotify

import (
	"testing"

	"github.com/bjarneo/cliamp/playlist"
	playlist4pb "github.com/devgianlu/go-librespot/proto/spotify/playlist4"
	"google.golang.org/protobuf/proto"
)

func rootlistContent(uris []string, metas []*playlist4pb.MetaItem) *playlist4pb.SelectedListContent {
	items := make([]*playlist4pb.Item, len(uris))
	for i, u := range uris {
		items[i] = &playlist4pb.Item{Uri: proto.String(u)}
	}
	return &playlist4pb.SelectedListContent{
		Contents: &playlist4pb.ListItems{Items: items, MetaItems: metas},
	}
}

func meta(name string, length int32, owner string) *playlist4pb.MetaItem {
	return &playlist4pb.MetaItem{
		Attributes:    &playlist4pb.ListAttributes{Name: proto.String(name)},
		Length:        proto.Int32(length),
		OwnerUsername: proto.String(owner),
	}
}

// A response carrying playlists but no metadata at all cannot be named, and
// unnamed entries are hidden -- so it must fail into the Web API rather than
// present an empty library as if that were the truth.
func TestParseRootlistRejectsMissingMetadata(t *testing.T) {
	content := rootlistContent([]string{"spotify:playlist:a", "spotify:playlist:b"}, nil)
	if _, err := parseRootlist(content); err == nil {
		t.Fatal("parsed a metadata-less response, want an error so the Web API serves the list")
	}
}

func TestParseRootlistEmptyLibraryIsNotAnError(t *testing.T) {
	got, err := parseRootlist(rootlistContent(nil, nil))
	if err != nil {
		t.Fatalf("empty library: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d entries, want 0", len(got))
	}
}

func TestParseRootlistReadsFoldersAndPlaylists(t *testing.T) {
	content := rootlistContent(
		[]string{
			"spotify:start-group:f1:Late+Night+%26+Chill",
			"spotify:playlist:inner",
			"spotify:end-group:f1",
			"spotify:playlist:outer",
		},
		[]*playlist4pb.MetaItem{nil, meta("Inner", 3, "listener"), nil, meta("Outer", 7, "listener")},
	)
	got, err := parseRootlist(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d entries, want 4", len(got))
	}
	if got[0].Name != "Late Night & Chill" || !got[0].FolderOpen {
		t.Errorf("folder start = %+v, want the decoded name and FolderOpen", got[0])
	}
	if got[1].Name != "Inner" || got[1].TrackCount != 3 {
		t.Errorf("inner playlist = %+v", got[1])
	}
	if !got[2].isFolder() || got[2].FolderOpen {
		t.Errorf("folder end = %+v, want a close boundary", got[2])
	}
}

// Folder sections come from the open-group stack. An end-group naming a folder
// that is not the innermost one must not silently reparent what follows it.
func TestRootlistPlaylistsIgnoresMismatchedEndGroup(t *testing.T) {
	entries := []rootlistEntry{
		{URI: "spotify:start-group:f1:Outer", Name: "Outer", FolderID: "f1", FolderOpen: true},
		{URI: "spotify:start-group:f2:Inner", Name: "Inner", FolderID: "f2", FolderOpen: true},
		{URI: "spotify:playlist:a", Name: "Nested", TrackCount: 1, Owner: "listener"},
		{URI: "spotify:end-group:f1", FolderID: "f1"}, // closes the outer one out of order
		{URI: "spotify:playlist:b", Name: "After", TrackCount: 1, Owner: "listener"},
		// A reference Spotify no longer serves: unnamed, and 404s when opened.
		{URI: "spotify:playlist:gone", TrackCount: 2, Owner: "listener"},
	}
	p := &SpotifyProvider{}
	got := p.rootlistPlaylists(entries, "listener")
	if len(got) != 2 {
		t.Fatalf("got %d playlists, want 2 (the unopenable entry must be hidden)", len(got))
	}
	if got[0].Section != "Outer / Inner" {
		t.Errorf("nested playlist section = %q, want %q", got[0].Section, "Outer / Inner")
	}
	if got[1].Section != "Outer / Inner" {
		t.Errorf("section after a mismatched end-group = %q, want the stack left intact", got[1].Section)
	}
}

func revEntry(id string, rev []byte) rootlistEntry {
	return rootlistEntry{URI: "spotify:playlist:" + id, Name: id, TrackCount: 1, Owner: "listener", Revision: rev}
}

// In auto mode the client protocol leads the listing, so the Web API's
// snapshot_id compare never runs. Without the rootlist revision standing in for
// it, a playlist edited elsewhere would serve its cached tracks forever.
func TestApplyRevisionsDropsCacheWhenRevisionMoves(t *testing.T) {
	p := &SpotifyProvider{
		trackCache:       map[string]*playlistCache{},
		pending:          map[string]*pendingTracks{},
		declinedByWeb:    map[string]bool{},
		declinedByClient: map[string]bool{},
	}
	p.applyRevisions([]rootlistEntry{revEntry("a", []byte{1})})
	p.trackCache["a"].tracks = []playlist.Track{{Path: "spotify:track:x"}}
	p.pending["a"] = &pendingTracks{want: 1, total: 1, uris: []string{"spotify:track:x"}}

	p.applyRevisions([]rootlistEntry{revEntry("a", []byte{1})})
	if p.trackCache["a"] == nil || len(p.trackCache["a"].tracks) != 1 {
		t.Error("an unchanged revision dropped the cache")
	}
	if pend := p.pending["a"]; pend == nil || len(pend.uris) != 1 {
		t.Error("an unchanged revision discarded a live read")
	}

	p.applyRevisions([]rootlistEntry{revEntry("a", []byte{2})})
	if c := p.trackCache["a"]; c == nil || len(c.tracks) != 0 {
		t.Error("a changed revision left the cached tracks in place")
	}
	if _, ok := p.pending["a"]; ok {
		t.Error("a changed revision left the read, and its resolve, in place")
	}
}

// The library does not report a revision for every entry, so an entry without
// one must not be read as "changed" and throw away a good cache every listing.
func TestApplyRevisionsIgnoresEntriesWithoutOne(t *testing.T) {
	p := &SpotifyProvider{
		trackCache:       map[string]*playlistCache{"a": {revision: "01", tracks: []playlist.Track{{Path: "spotify:track:x"}}}},
		pending:          map[string]*pendingTracks{},
		declinedByWeb:    map[string]bool{},
		declinedByClient: map[string]bool{},
	}
	p.applyRevisions([]rootlistEntry{revEntry("a", nil)})
	if c := p.trackCache["a"]; c == nil || len(c.tracks) != 1 {
		t.Error("an entry with no revision invalidated the cache")
	}
}

// A cache seeded by the Web API's snapshot_id must not be invalidated by a
// rootlist revision: they version the same playlist in different id spaces.
func TestApplyRevisionsDoesNotCollideWithSnapshotIDs(t *testing.T) {
	p := &SpotifyProvider{
		trackCache:       map[string]*playlistCache{},
		pending:          map[string]*pendingTracks{},
		declinedByWeb:    map[string]bool{},
		declinedByClient: map[string]bool{},
	}
	p.applyRevisions([]rootlistEntry{revEntry("a", []byte{0xAB})})
	if got := p.trackCache["a"].snapshotID; got != "" {
		t.Errorf("revision leaked into snapshotID as %q", got)
	}
	if got := p.trackCache["a"].revision; got != "ab" {
		t.Errorf("revision = %q, want %q", got, "ab")
	}
}

// A listing that refreshes while a playlist is being paged must discard the
// accumulation, not just the resolve behind it. Dropping only the resolve makes
// the next page re-resolve against the new revision, and the two snapshots are
// then spliced into one committed list that no later check can catch when the
// edit left the total alone.
func TestApplyRevisionsDiscardsAnInFlightRead(t *testing.T) {
	p := &SpotifyProvider{
		trackCache:       map[string]*playlistCache{},
		pending:          map[string]*pendingTracks{},
		declinedByWeb:    map[string]bool{},
		declinedByClient: map[string]bool{},
	}
	p.applyRevisions([]rootlistEntry{revEntry("a", []byte{1})})

	// A chain is midway through reading the list.
	p.pending["a"] = &pendingTracks{want: 1, total: 1, tracks: make([]playlist.Track, 1), uris: []string{"spotify:track:x"}}

	p.applyRevisions([]rootlistEntry{revEntry("a", []byte{2})})

	if _, ok := p.pending["a"]; ok {
		t.Error("the accumulation, and the resolve it holds, survived a revision change")
	}
}

// A read that finishes before any listing commits its tracks with neither id.
// The Web API listing must still drop such an entry: adopting its snapshot
// would pin a list committed before an edit to the snapshot taken after one,
// and nothing would ever invalidate it again. Only an entry carrying a revision
// is evidence that the client protocol seeded it.
func TestWebListingOnlyAdoptsClientSeededEntries(t *testing.T) {
	stale := []playlist.Track{{Path: "spotify:track:old"}}

	t.Run("no provenance is dropped", func(t *testing.T) {
		cache := map[string]*playlistCache{"a": {tracks: stale, total: 1}}
		if !adoptSnapshot(cache, "a", "snap-2") {
			t.Fatal("a cache with neither id was not reported dropped")
		}
		if c, ok := cache["a"]; ok && len(c.tracks) > 0 {
			t.Error("a cache with neither id survived the listing, so a stale list is pinned forever")
		}
	})

	t.Run("client seeded is adopted", func(t *testing.T) {
		cache := map[string]*playlistCache{"a": {revision: "ab", tracks: stale, total: 1}}
		if adoptSnapshot(cache, "a", "snap-2") {
			t.Fatal("an adopted entry was reported dropped")
		}
		c, ok := cache["a"]
		if !ok || len(c.tracks) != 1 {
			t.Fatal("a client-seeded cache was dropped instead of adopting the snapshot")
		}
		if c.snapshotID != "snap-2" {
			t.Errorf("snapshotID = %q, want it adopted", c.snapshotID)
		}
	})

	t.Run("a moved snapshot still invalidates", func(t *testing.T) {
		cache := map[string]*playlistCache{"a": {snapshotID: "snap-1", tracks: stale, total: 1}}
		if !adoptSnapshot(cache, "a", "snap-2") {
			t.Fatal("a moved snapshot was not reported dropped")
		}
		if c, ok := cache["a"]; ok && len(c.tracks) > 0 {
			t.Error("a changed snapshot left the cached tracks in place")
		}
	})

	t.Run("a never-cached playlist is left alone", func(t *testing.T) {
		cache := map[string]*playlistCache{}
		if adoptSnapshot(cache, "a", "snap-2") {
			t.Fatal("a playlist the cache has never seen was reported dropped")
		}
	})
}

// The library keeps references Spotify no longer serves, which arrive unnamed
// and 404 when opened, and generated entries such as DJ that are not lists at
// all. Showing either gives the user a row that cannot open. A user's own empty
// playlist is still worth showing, so emptiness alone must not hide anything.
func TestSkipRootlistEntry(t *testing.T) {
	for _, tc := range []struct {
		name string
		e    rootlistEntry
		skip bool
	}{
		{"unnamed reference", rootlistEntry{Name: "", TrackCount: 3, Owner: "listener"}, true},
		{"spotify-owned and empty", rootlistEntry{Name: "DJ", TrackCount: 0, Owner: spotifyOwner}, true},
		{"spotify-owned with tracks", rootlistEntry{Name: "Discover Weekly", TrackCount: 30, Owner: spotifyOwner}, false},
		{"own empty playlist", rootlistEntry{Name: "Later", TrackCount: 0, Owner: "listener"}, false},
		{"ordinary playlist", rootlistEntry{Name: "good stuff", TrackCount: 12, Owner: "listener"}, false},
	} {
		if got := skipRootlistEntry(tc.e); got != tc.skip {
			t.Errorf("%s: skip = %v, want %v", tc.name, got, tc.skip)
		}
	}
}
