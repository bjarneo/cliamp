package local

import (
	"path/filepath"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

func paths(tracks []playlist.Track) []string {
	out := make([]string, len(tracks))
	for i, t := range tracks {
		out[i] = t.Path
	}
	return out
}

func equalPaths(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestPrependTracksPutsTracksFirst(t *testing.T) {
	p := newTestProvider(t)
	if _, _, err := p.AddTracks("show", []playlist.Track{{Path: "/old1.mp3"}, {Path: "/old2.mp3"}}); err != nil {
		t.Fatalf("AddTracks: %v", err)
	}

	added, moved, skipped, err := p.PrependTracks("show", []playlist.Track{{Path: "/new1.mp3"}, {Path: "/new2.mp3"}})
	if err != nil {
		t.Fatalf("PrependTracks: %v", err)
	}
	if added != 2 || moved != 0 || skipped != 0 {
		t.Errorf("added=%d moved=%d skipped=%d, want 2/0/0", added, moved, skipped)
	}

	tracks, err := p.Tracks("show")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	want := []string{"/new1.mp3", "/new2.mp3", "/old1.mp3", "/old2.mp3"}
	if got := paths(tracks); !equalPaths(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// Prepending is an ordering request, so a track already in the playlist moves
// to the front rather than being skipped as a duplicate.
func TestPrependTracksMovesExistingToFront(t *testing.T) {
	p := newTestProvider(t)
	if _, _, err := p.AddTracks("show", []playlist.Track{{Path: "/a.mp3"}, {Path: "/b.mp3"}, {Path: "/c.mp3"}}); err != nil {
		t.Fatalf("AddTracks: %v", err)
	}

	added, moved, skipped, err := p.PrependTracks("show", []playlist.Track{{Path: "/c.mp3"}, {Path: "/new.mp3"}})
	if err != nil {
		t.Fatalf("PrependTracks: %v", err)
	}
	if added != 1 || moved != 1 || skipped != 0 {
		t.Errorf("added=%d moved=%d skipped=%d, want 1/1/0", added, moved, skipped)
	}

	tracks, _ := p.Tracks("show")
	want := []string{"/c.mp3", "/new.mp3", "/a.mp3", "/b.mp3"}
	if got := paths(tracks); !equalPaths(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestPrependTracksDedupesWithinTheBatch(t *testing.T) {
	p := newTestProvider(t)

	added, moved, skipped, err := p.PrependTracks("fresh", []playlist.Track{
		{Path: "/a.mp3"}, {Path: "/b.mp3"}, {Path: "/a.mp3"},
	})
	if err != nil {
		t.Fatalf("PrependTracks: %v", err)
	}
	if added != 2 || moved != 0 || skipped != 1 {
		t.Errorf("added=%d moved=%d skipped=%d, want 2/0/1", added, moved, skipped)
	}

	tracks, _ := p.Tracks("fresh")
	want := []string{"/a.mp3", "/b.mp3"}
	if got := paths(tracks); !equalPaths(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestPrependTracksCreatesMissingPlaylist(t *testing.T) {
	p := newTestProvider(t)

	added, _, _, err := p.PrependTracks("brand-new", []playlist.Track{{Path: "/a.mp3"}})
	if err != nil {
		t.Fatalf("PrependTracks: %v", err)
	}
	if added != 1 {
		t.Errorf("added = %d, want 1", added)
	}
	if tracks, err := p.Tracks("brand-new"); err != nil || len(tracks) != 1 {
		t.Errorf("Tracks() = %v, %v; want one track", tracks, err)
	}
}

// A track the playlist only holds through a [[dir]] source is generated at
// load time, so it cannot be reordered and must be left alone.
func TestPrependTracksSkipsDirSourcedPaths(t *testing.T) {
	p := newTestProvider(t)
	audio := t.TempDir()
	writeAudioFile(t, filepath.Join(audio, "a.mp3"))
	if err := p.CreateDirPlaylist("music", []string{audio}); err != nil {
		t.Fatalf("CreateDirPlaylist: %v", err)
	}

	added, moved, skipped, err := p.PrependTracks("music", []playlist.Track{
		{Path: filepath.Join(audio, "a.mp3")},
		{Path: "/elsewhere.mp3"},
	})
	if err != nil {
		t.Fatalf("PrependTracks: %v", err)
	}
	if added != 1 || moved != 0 || skipped != 1 {
		t.Errorf("added=%d moved=%d skipped=%d, want 1/0/1", added, moved, skipped)
	}
}

func TestPrependTracksRejectsReservedNames(t *testing.T) {
	p := newTestProvider(t)
	for _, name := range []string{"Recently Played", "Favorites"} {
		t.Run(name, func(t *testing.T) {
			if _, _, _, err := p.PrependTracks(name, []playlist.Track{{Path: "/a.mp3"}}); err == nil {
				t.Errorf("PrependTracks(%q) = nil error, want a rejection", name)
			}
		})
	}
}

func TestPrependTracksEmptyBatchIsANoOp(t *testing.T) {
	p := newTestProvider(t)
	if _, _, err := p.AddTracks("show", []playlist.Track{{Path: "/a.mp3"}}); err != nil {
		t.Fatalf("AddTracks: %v", err)
	}

	added, moved, skipped, err := p.PrependTracks("show", nil)
	if err != nil || added != 0 || moved != 0 || skipped != 0 {
		t.Errorf("PrependTracks(nil) = %d/%d/%d, %v; want 0/0/0 and no error", added, moved, skipped, err)
	}
	if tracks, _ := p.Tracks("show"); len(tracks) != 1 {
		t.Errorf("playlist changed: %v", paths(tracks))
	}
}

// Prepending rewrites the document's section order by hand, so the [[dir]]
// sources must survive it.
func TestPrependTracksKeepsDirSources(t *testing.T) {
	p := newTestProvider(t)
	audio := t.TempDir()
	writeAudioFile(t, filepath.Join(audio, "a.mp3"))
	if err := p.CreateDirPlaylist("mixed", []string{audio}); err != nil {
		t.Fatalf("CreateDirPlaylist: %v", err)
	}
	if _, _, err := p.AddTracks("mixed", []playlist.Track{{Path: "/explicit.mp3"}}); err != nil {
		t.Fatalf("AddTracks: %v", err)
	}

	if _, _, _, err := p.PrependTracks("mixed", []playlist.Track{{Path: "/first.mp3"}}); err != nil {
		t.Fatalf("PrependTracks: %v", err)
	}

	dirs, err := p.DirSources("mixed")
	if err != nil {
		t.Fatalf("DirSources: %v", err)
	}
	if len(dirs) != 1 || dirs[0].Path != audio {
		t.Errorf("dir sources = %+v, want the one source at %q", dirs, audio)
	}
	tracks, _ := p.Tracks("mixed")
	if len(tracks) == 0 || tracks[0].Path != "/first.mp3" {
		t.Errorf("first track = %v, want /first.mp3", paths(tracks))
	}
}

// Moving a track to the front keeps what the playlist already knows about it.
// The incoming copy may be the bare path a picker hands over, and a move must
// not shed the bookmark or the podcast identity the file holds.
func TestPrependTracksKeepsTheStoredTrackOnMove(t *testing.T) {
	p := newTestProvider(t)
	stored := playlist.Track{
		Path:     "/a.mp3",
		Title:    "Full Title",
		Bookmark: true,
		ProviderMeta: map[string]string{
			provider.MetaPodcastFeed: "https://example.com/feed",
			provider.MetaPodcastGUID: "guid-1",
		},
	}
	if _, _, err := p.AddTracks("show", []playlist.Track{{Path: "/first.mp3"}, stored}); err != nil {
		t.Fatalf("AddTracks: %v", err)
	}

	_, moved, _, err := p.PrependTracks("show", []playlist.Track{{Path: "/a.mp3"}})
	if err != nil {
		t.Fatalf("PrependTracks: %v", err)
	}
	if moved != 1 {
		t.Fatalf("moved = %d, want 1", moved)
	}

	tracks, _ := p.Tracks("show")
	got := tracks[0]
	if got.Path != "/a.mp3" {
		t.Fatalf("first = %q, want /a.mp3", got.Path)
	}
	if got.Title != "Full Title" {
		t.Errorf("title = %q, want the stored title kept", got.Title)
	}
	if !got.Bookmark {
		t.Error("bookmark lost on move")
	}
	if got.Meta(provider.MetaPodcastGUID) != "guid-1" {
		t.Errorf("guid = %q, want the stored identity kept", got.Meta(provider.MetaPodcastGUID))
	}
}
