package playlist

import "testing"

func TestReplaceOwnsInputTracks(t *testing.T) {
	input := []Track{{
		Title:        "Original",
		Bookmark:     true,
		ProviderMeta: map[string]string{"provider.id": "original"},
	}}
	p := New()
	p.Replace(input)

	input[0].Title = "Changed"
	input[0].Bookmark = false
	input[0].ProviderMeta["provider.id"] = "changed"

	got := p.Tracks()[0]
	if got.Title != "Original" || !got.Bookmark || got.ProviderMeta["provider.id"] != "original" {
		t.Fatalf("track after caller mutation = %#v, want original values", got)
	}
	if got := p.BookmarkCount(); got != 1 {
		t.Fatalf("BookmarkCount() = %d after caller mutation, want 1", got)
	}

	returned := p.Tracks()
	returned[0].Bookmark = false
	returned[0].ProviderMeta["provider.id"] = "returned"
	if got := p.BookmarkCount(); got != 1 {
		t.Fatalf("BookmarkCount() = %d after Tracks mutation, want 1", got)
	}
	if got := p.Tracks()[0].ProviderMeta["provider.id"]; got != "original" {
		t.Fatalf("metadata after Tracks mutation = %q, want original", got)
	}
}

func TestTrackInputsAndSnapshotsOwnProviderMeta(t *testing.T) {
	p := New()
	added := Track{Title: "Added", ProviderMeta: map[string]string{"provider.id": "added"}}
	p.Add(added)
	added.ProviderMeta["provider.id"] = "changed"
	if got := p.Tracks()[0].ProviderMeta["provider.id"]; got != "added" {
		t.Fatalf("metadata after Add caller mutation = %q, want added", got)
	}

	replacement := Track{Title: "Replacement", ProviderMeta: map[string]string{"provider.id": "replacement"}}
	p.SetTrack(0, replacement)
	replacement.ProviderMeta["provider.id"] = "changed"
	if got := p.Tracks()[0].ProviderMeta["provider.id"]; got != "replacement" {
		t.Fatalf("metadata after SetTrack caller mutation = %q, want replacement", got)
	}

	tracks := p.Tracks()
	tracks[0].ProviderMeta["provider.id"] = "snapshot"
	if got := p.Tracks()[0].ProviderMeta["provider.id"]; got != "replacement" {
		t.Fatalf("metadata after Tracks mutation = %q, want replacement", got)
	}
}

func TestSnapshotAndRestoreOwnProviderMeta(t *testing.T) {
	p := New()
	p.Replace([]Track{{ProviderMeta: map[string]string{"provider.id": "original"}}})

	snapshot := p.Snapshot()
	snapshot.tracks[0].ProviderMeta["provider.id"] = "snapshot"
	if got := p.Tracks()[0].ProviderMeta["provider.id"]; got != "original" {
		t.Fatalf("metadata after Snapshot mutation = %q, want original", got)
	}

	snapshot = p.Snapshot()
	p.Restore(snapshot)
	snapshot.tracks[0].ProviderMeta["provider.id"] = "after-restore"
	if got := p.Tracks()[0].ProviderMeta["provider.id"]; got != "original" {
		t.Fatalf("metadata after restored Snapshot mutation = %q, want original", got)
	}
}

func TestReturnedTrackOwnsProviderMeta(t *testing.T) {
	p := New()
	p.Replace([]Track{{ProviderMeta: map[string]string{"provider.id": "original"}}})

	track, index := p.Current()
	if index != 0 {
		t.Fatalf("Current() index = %d, want 0", index)
	}
	track.ProviderMeta["provider.id"] = "changed"

	if got := p.Tracks()[0].ProviderMeta["provider.id"]; got != "original" {
		t.Fatalf("metadata after Current mutation = %q, want original", got)
	}
}
