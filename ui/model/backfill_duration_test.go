package model

import (
	"errors"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

// backfillTestProvider is a local playlist store that records its calls.
// tracks is the content of the playlist file.
type backfillTestProvider struct {
	commandsTestProvider
	tracks  []playlist.Track
	updates int
	saves   [][]playlist.Track
}

func (p *backfillTestProvider) UpdatePlaylist(_ string, fn func([]playlist.Track) ([]playlist.Track, error)) error {
	p.updates++
	tracks, err := fn(cloneTracks(p.tracks))
	if errors.Is(err, playlist.ErrPlaylistUnchanged) {
		return nil
	}
	if err != nil {
		return err
	}
	p.saves = append(p.saves, cloneTracks(tracks))
	p.tracks = tracks
	return nil
}

// A track start without a known duration sets the decoded duration in the
// queue. Only an explicit track of a playlist file gets the duration written
// back, and that write runs in a command, not in Update. The command changes
// the file only while the file track has no duration.
func TestBackfillLoadedPlaylistDuration(t *testing.T) {
	explicit := playlist.Track{Path: "/music/a.mp3", Title: "A"}
	fromDir := playlist.Track{Path: "/music/dir/b.mp3", Title: "B", DirSourced: true}
	for _, tc := range []struct {
		name       string
		loaded     string
		track      playlist.Track
		fileTrack  playlist.Track // the explicit track in the file
		wantQueue  int
		wantUpdate bool
		wantSaved  bool
	}{
		{name: "explicit track", loaded: "Mix", track: explicit, fileTrack: explicit, wantQueue: 240, wantUpdate: true, wantSaved: true},
		{name: "another writer set the duration", loaded: "Mix", track: explicit, fileTrack: playlist.Track{Path: explicit.Path, DurationSecs: 180}, wantQueue: 240, wantUpdate: true},
		{name: "directory track", loaded: "Mix", track: fromDir, fileTrack: explicit, wantQueue: 240},
		{name: "favorites", loaded: favorites.PlaylistName, track: explicit, fileTrack: explicit},
		{name: "history", loaded: history.PlaylistName, track: explicit, fileTrack: explicit},
		{name: "no saved playlist", track: explicit, fileTrack: explicit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &backfillTestProvider{
				commandsTestProvider: commandsTestProvider{name: "Local"},
				tracks:               []playlist.Track{tc.fileTrack, fromDir},
			}
			pl := playlist.New()
			pl.Add(tc.track)
			pl.SetIndex(0)
			m := Model{
				player:         &playbackFakeEngine{duration: 240 * time.Second},
				playlist:       pl,
				vis:            ui.NewVisualizer(44100),
				localProvider:  store,
				loadedPlaylist: tc.loaded,
			}

			cmd := m.playTrack(tc.track)
			if store.updates != 0 {
				t.Fatalf("Update changed the playlist %d times, want none", store.updates)
			}
			if got, _ := m.playlist.Track(0); got.DurationSecs != tc.wantQueue {
				t.Fatalf("queue duration = %d, want %d", got.DurationSecs, tc.wantQueue)
			}
			runCmd(cmd)
			if updated := store.updates > 0; updated != tc.wantUpdate {
				t.Fatalf("the command updated the playlist %d times, want an update %v", store.updates, tc.wantUpdate)
			}
			if !tc.wantSaved {
				if len(store.saves) != 0 {
					t.Fatalf("the command saved the playlist %d times, want none", len(store.saves))
				}
				return
			}
			if len(store.saves) != 1 {
				t.Fatalf("saves = %d, want 1", len(store.saves))
			}
			if got := store.saves[0][0]; got.Path != explicit.Path || got.DurationSecs != 240 {
				t.Fatalf("saved track = %+v, want %s with 240 s", got, explicit.Path)
			}
		})
	}
}

// A local ffmpeg format starts in a command. Its decoded duration fills the
// queue and the playlist file when the start reports back. A provider URI
// that an older file saved without the stream flag also starts there, and
// it keeps no duration, because it is not a local file.
func TestBackfillLoadedPlaylistDurationAfterAsyncStart(t *testing.T) {
	for _, tc := range []struct {
		name      string
		path      string
		wantQueue int
		wantSaves int
	}{
		{name: "local ffmpeg format", path: "/music/a.m4a", wantQueue: 240, wantSaves: 1},
		{name: "provider uri", path: "qobuz://track/42"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			track := playlist.Track{Path: tc.path, Title: "A"}
			store := &backfillTestProvider{
				commandsTestProvider: commandsTestProvider{name: "Local"},
				tracks:               []playlist.Track{track},
			}
			pl := playlist.New()
			pl.Add(track)
			pl.SetIndex(0)
			engine := &playbackFakeEngine{duration: 240 * time.Second}
			m := Model{
				player:         sourceResolverEngine{engine, []string{"qobuz://track/"}},
				playlist:       pl,
				vis:            ui.NewVisualizer(44100),
				localProvider:  store,
				loadedPlaylist: "Mix",
			}

			cmd := m.playTrack(track)
			if len(engine.playCalls) != 0 || !m.buffering {
				t.Fatalf("playTrack started the track in Update: play calls %v, buffering %v", engine.playCalls, m.buffering)
			}
			played := streamPlayedFrom(t, cmd)
			if got, _ := m.playlist.Track(0); got.DurationSecs != 0 {
				t.Fatalf("queue duration before the start reported back = %d, want 0", got.DurationSecs)
			}

			updated, cmd := m.Update(played)
			m = updated.(Model)
			if got, _ := m.playlist.Track(0); got.DurationSecs != tc.wantQueue {
				t.Fatalf("queue duration = %d, want %d", got.DurationSecs, tc.wantQueue)
			}
			runCmd(cmd)
			if len(store.saves) != tc.wantSaves {
				t.Fatalf("saves = %+v, want %d", store.saves, tc.wantSaves)
			}
			if tc.wantSaves > 0 && store.saves[0][0].DurationSecs != tc.wantQueue {
				t.Fatalf("saved track = %+v, want %d s", store.saves[0][0], tc.wantQueue)
			}
		})
	}
}
