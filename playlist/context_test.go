package playlist

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestPlaybackContextOwnsImmutableSnapshot(t *testing.T) {
	input := []Track{
		{Path: "a", ProviderMeta: map[string]string{"id": "a"}},
		{Path: "b", ProviderMeta: map[string]string{"id": "b"}},
	}
	want := cloneTracks(input)
	tracks := WithPlaybackContext(input)
	if !reflect.DeepEqual(input, want) {
		t.Fatal("WithPlaybackContext mutated its input")
	}
	if &tracks[0].playbackContext[0] != &tracks[1].playbackContext[0] {
		t.Fatal("batch entries do not share a source snapshot")
	}
	input[0].Path = "changed input"
	input[0].ProviderMeta["id"] = "changed input"
	if tracks[0].Path != "a" || tracks[0].Meta("id") != "a" {
		t.Fatal("returned tracks alias the input")
	}
	tracks[1].Path = "changed entry"
	tracks[1].ProviderMeta["id"] = "changed entry"
	context, _ := tracks[0].PlaybackContext()
	context[0].Path = "changed result"
	context[0].ProviderMeta["id"] = "changed result"
	for i, track := range tracks {
		context, index := track.PlaybackContext()
		if index != i || !reflect.DeepEqual(context, want) {
			t.Fatalf("PlaybackContext() = (%+v, %d), want (%+v, %d)", context, index, want, i)
		}
	}
}

func TestWithPlaybackContextReplacesPriorContexts(t *testing.T) {
	prior := WithPlaybackContext([]Track{{Path: "a"}, {Path: "b"}, {Path: "a"}, {Path: "c"}})
	tracks := []Track{prior[2], prior[3]}
	want := []Track{{Path: "a"}, {Path: "c"}}
	for range 3 {
		tracks = WithPlaybackContext(tracks)
		for i, track := range tracks {
			context, index := track.PlaybackContext()
			if index != i || !reflect.DeepEqual(context, want) {
				t.Fatalf("rebased context = (%+v, %d), want (%+v, %d)", context, index, want, i)
			}
			for _, entry := range context {
				if nested, index := entry.PlaybackContext(); nested != nil || index != -1 {
					t.Fatal("source snapshot retained nested provenance")
				}
			}
		}
	}
}

func TestPlaybackContextIsNotSerialized(t *testing.T) {
	input := []Track{{Path: "a"}, {Path: "b"}}
	data, err := json.Marshal(WithPlaybackContext(input))
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(want) {
		t.Fatalf("runtime context changed JSON: %s", data)
	}
	var restored []Track
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored, input) {
		t.Fatalf("restored tracks = %+v, want %+v without provenance", restored, input)
	}
}

func TestPlaybackContextSurvivesPlaylistMutations(t *testing.T) {
	source := []Track{{Path: "a"}, {Path: "b"}, {Path: "a"}, {Path: "c"}}
	p := New()
	p.Add(WithPlaybackContext(source)...)
	p.SetIndex(2)
	p.Queue(3)
	p.Queue(2)
	snapshot := p.Snapshot()
	check := func(track Track, wantIndex int) {
		t.Helper()
		context, index := track.PlaybackContext()
		if index != wantIndex || !reflect.DeepEqual(context, source) {
			t.Fatalf("source after mutation = (%+v, %d), want index %d in original list", context, index, wantIndex)
		}
	}
	if !p.Move(2, 3) || !p.Remove(0) {
		t.Fatal("move/remove failed")
	}
	track, _ := p.Current()
	check(track, 2)
	entries := p.QueueEntries()
	check(entries[0].Track, 3)
	check(entries[1].Track, 2)
	p.Restore(snapshot)
	track, index := p.Current()
	if index != 2 || p.QueueLen() != 2 {
		t.Fatal("undo did not restore selection and queue")
	}
	check(track, 2)
	for _, wantIndex := range []int{3, 2, 3} {
		track, ok := p.Next()
		if !ok {
			t.Fatal("Next failed")
		}
		check(track, wantIndex)
	}
	p.SetRepeat(RepeatOne)
	track, ok := p.Next()
	if !ok {
		t.Fatal("repeat failed")
	}
	check(track, 3)
}
