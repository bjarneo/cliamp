package model

import (
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

// Undoing a replace of the whole queue brings back the album headers of the
// old queue: a list of single tracks from many albums shows none, though the
// album that replaced it did.
func TestUndoRecountsAlbumHeaders(t *testing.T) {
	m := keybindingTestModel()
	singles := []playlist.Track{{Path: "a.mp3", Album: "A"}, {Path: "b.mp3", Album: "B"}, {Path: "c.mp3", Album: "C"}}
	m.playlist.Replace(singles)
	m.setHeaderStateFromTracks(singles)
	snapshot := m.playlist.Snapshot()

	album := []playlist.Track{{Path: "x1.mp3", Album: "X"}, {Path: "x2.mp3", Album: "X"}, {Path: "x3.mp3", Album: "X"}}
	m.replacePlaylist(album)
	m.setHeaderStateFromTracks(album)
	m.recordPlaylistUndo(playlistUndo{snapshot: snapshot})
	if !m.showAlbumHeaders {
		t.Fatal("the album shows no album headers")
	}

	m.undoPlaylistMutation()
	if m.showAlbumHeaders {
		t.Fatal("undo kept the album's headers on a list of single tracks")
	}
}
