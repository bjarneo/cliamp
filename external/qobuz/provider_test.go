package qobuz

import (
	"encoding/json"
	"math/rand/v2"
	"strconv"
	"testing"
)

func TestTrackArtist(t *testing.T) {
	withPerformer := apiTrack{Performer: apiArtist{Name: "Performer"}}
	if got := trackArtist(withPerformer, nil); got != "Performer" {
		t.Errorf("performer name: got %q want %q", got, "Performer")
	}

	album := &apiAlbum{Artist: apiArtist{Name: "AlbumArtist"}}
	if got := trackArtist(apiTrack{}, album); got != "AlbumArtist" {
		t.Errorf("album fallback: got %q want %q", got, "AlbumArtist")
	}

	if got := trackArtist(apiTrack{}, nil); got != "" {
		t.Errorf("no artist: got %q want empty", got)
	}
}

func TestDedupeTracksByID(t *testing.T) {
	tracks := []apiTrack{
		{ID: "1", Title: "first"},
		{ID: "2", Title: "second"},
		{ID: "1", Title: "dup of first"},
		{ID: "3", Title: "third"},
		{ID: "2", Title: "dup of second"},
		{ID: "", Title: "no id a"},
		{ID: "", Title: "no id b"},
	}

	got := dedupeTracksByID(tracks)

	want := []struct {
		id    string
		title string
	}{
		{"1", "first"}, // first occurrence wins
		{"2", "second"},
		{"3", "third"},
		{"", "no id a"}, // empty-ID tracks are always kept
		{"", "no id b"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d tracks, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].ID.String() != w.id || got[i].Title != w.title {
			t.Errorf("track %d = {%q, %q}, want {%q, %q}",
				i, got[i].ID.String(), got[i].Title, w.id, w.title)
		}
	}
}

func TestDedupeTracksByIDEmpty(t *testing.T) {
	if got := dedupeTracksByID(nil); len(got) != 0 {
		t.Errorf("dedupeTracksByID(nil) = %v, want empty", got)
	}
}

func TestSampleTracks(t *testing.T) {
	mk := func(n int) []apiTrack {
		ts := make([]apiTrack, n)
		for i := range ts {
			ts[i] = apiTrack{ID: json.Number(strconv.Itoa(i))}
		}
		return ts
	}
	idSet := func(ts []apiTrack) map[string]bool {
		m := make(map[string]bool, len(ts))
		for _, tr := range ts {
			m[tr.ID.String()] = true
		}
		return m
	}

	r := rand.New(rand.NewPCG(42, 1024))

	// Under the cap: every track is kept, but the list must still be shuffled.
	// This is the case that used to be returned in playlist order unchanged.
	in := mk(100)
	got := sampleTracks(in, 500, r.Shuffle)
	if len(got) != 100 {
		t.Fatalf("under cap: len = %d, want 100", len(got))
	}
	want := idSet(in)
	for _, tr := range got {
		if !want[tr.ID.String()] {
			t.Errorf("under cap: track %s not from input", tr.ID)
		}
	}
	sameOrder := true
	for i := range got {
		if got[i].ID != in[i].ID {
			sameOrder = false
			break
		}
	}
	if sameOrder {
		t.Error("under cap: list was not shuffled")
	}

	// Over the cap: exactly n tracks, all from the input, no duplicates.
	big := mk(1000)
	all := idSet(big)
	for range 20 {
		s := sampleTracks(big, 10, r.Shuffle)
		if len(s) != 10 {
			t.Fatalf("over cap: len = %d, want 10", len(s))
		}
		seen := make(map[string]bool, len(s))
		for _, tr := range s {
			id := tr.ID.String()
			if !all[id] {
				t.Fatalf("over cap: track %q not from input", id)
			}
			if seen[id] {
				t.Fatalf("over cap: duplicate track %q", id)
			}
			seen[id] = true
		}
	}
}

func TestNewQualityNormalization(t *testing.T) {
	for _, q := range []int{5, 6, 7, 27} {
		if got := New(q).quality; got != q {
			t.Errorf("New(%d).quality = %d, want %d", q, got, q)
		}
	}
	for _, q := range []int{0, 1, 99} {
		if got := New(q).quality; got != defaultQuality {
			t.Errorf("New(%d).quality = %d, want default %d", q, got, defaultQuality)
		}
	}
}
