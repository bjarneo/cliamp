package cmd

import (
	"strings"
	"testing"
)

func TestPlaylistCreateWithDirSource(t *testing.T) {
	setupTestEnv(t)
	audio := t.TempDir()
	writeAudioFile(t, audio+"/a.mp3")
	writeAudioFile(t, audio+"/b.flac")

	out, err := captureStdout(t, func() error { return PlaylistCreate("Music", nil, "", []string{audio}) })
	if err != nil {
		t.Fatalf("PlaylistCreate --dir: %v", err)
	}
	if !strings.Contains(out, "referencing directory") {
		t.Errorf("output = %q, want 'referencing directory'", out)
	}

	// The playlist must reference the directory rather than expand files.
	if out, err := captureStdout(t, func() error { return PlaylistDirs("Music") }); err != nil {
		t.Fatalf("PlaylistDirs: %v", err)
	} else if !strings.Contains(out, audio) {
		t.Errorf("PlaylistDirs = %q, want %q", out, audio)
	}

	// Tracks resolve dynamically.
	out, err = captureStdout(t, func() error { return PlaylistShow("Music", false) })
	if err != nil {
		t.Fatalf("PlaylistShow: %v", err)
	}
	if !strings.Contains(out, "2 tracks") {
		t.Errorf("PlaylistShow = %q, want 2 tracks", out)
	}

	// Recreate must fail.
	if err := PlaylistCreate("Music", nil, "", []string{audio}); err == nil {
		t.Fatal("recreating existing playlist should fail")
	}
}

func TestPlaylistCreateDirMissingDirFails(t *testing.T) {
	setupTestEnv(t)
	err := PlaylistCreate("bad", nil, "", []string{t.TempDir() + "/missing"})
	if err == nil {
		t.Fatal("PlaylistCreate --dir with missing directory should fail")
	}
}

func TestPlaylistCreateDirWithSSHFails(t *testing.T) {
	setupTestEnv(t)
	audio := t.TempDir()
	writeAudioFile(t, audio+"/a.mp3")
	err := PlaylistCreate("mix", []string{audio}, "host", []string{audio})
	if err == nil || !strings.Contains(err.Error(), "--dir cannot be combined with --ssh") {
		t.Fatalf("expected --dir/--ssh conflict, got: %v", err)
	}
}

func TestPlaylistCreateDirPlusExplicitTracks(t *testing.T) {
	setupTestEnv(t)
	audio := t.TempDir()
	writeAudioFile(t, audio+"/a.mp3")
	outside := t.TempDir()
	writeAudioFile(t, outside+"/b.mp3")

	out, err := captureStdout(t, func() error {
		return PlaylistCreate("mix", []string{outside}, "", []string{audio})
	})
	if err != nil {
		t.Fatalf("PlaylistCreate mixed: %v", err)
	}
	if !strings.Contains(out, "1 directory and 1 explicit track") {
		t.Errorf("output = %q, want mixed count", out)
	}
	if out, err := captureStdout(t, func() error { return PlaylistShow("mix", false) }); err != nil {
		t.Fatalf("PlaylistShow: %v", err)
	} else if !strings.Contains(out, "2 tracks") {
		t.Errorf("PlaylistShow = %q, want 2 tracks", out)
	}
}

func TestPlaylistAddDirSource(t *testing.T) {
	setupTestEnv(t)
	audio := t.TempDir()
	writeAudioFile(t, audio+"/a.mp3")
	if err := PlaylistCreate("empty", nil, "", nil); err != nil {
		t.Fatalf("PlaylistCreate: %v", err)
	}

	out, err := captureStdout(t, func() error { return PlaylistAdd("empty", nil, []string{audio}) })
	if err != nil {
		t.Fatalf("PlaylistAdd --dir: %v", err)
	}
	if !strings.Contains(out, "Added directory") {
		t.Errorf("output = %q, want 'Added directory'", out)
	}

	// Adding the same directory again reports it as already present.
	out, err = captureStdout(t, func() error { return PlaylistAdd("empty", nil, []string{audio}) })
	if err != nil {
		t.Fatalf("PlaylistAdd duplicate: %v", err)
	}
	if !strings.Contains(out, "No new directories") {
		t.Errorf("output = %q, want 'No new directories'", out)
	}
}

func TestPlaylistAddDirToMissingPlaylistFails(t *testing.T) {
	setupTestEnv(t)
	err := PlaylistAdd("ghost", nil, []string{t.TempDir()})
	if err == nil {
		t.Fatal("PlaylistAdd --dir to missing playlist should fail")
	}
}

func TestPlaylistDirsNone(t *testing.T) {
	setupTestEnv(t)
	if err := PlaylistCreate("plain", nil, "", nil); err != nil {
		t.Fatalf("PlaylistCreate: %v", err)
	}
	out, err := captureStdout(t, func() error { return PlaylistDirs("plain") })
	if err != nil {
		t.Fatalf("PlaylistDirs: %v", err)
	}
	if !strings.Contains(out, "no directory sources") {
		t.Errorf("output = %q, want 'no directory sources'", out)
	}
}

func TestPlaylistRemoveDirSourcedTrackFails(t *testing.T) {
	setupTestEnv(t)
	audio := t.TempDir()
	writeAudioFile(t, audio+"/a.mp3")
	if err := PlaylistCreate("Music", nil, "", []string{audio}); err != nil {
		t.Fatalf("PlaylistCreate: %v", err)
	}
	err := PlaylistRemove("Music", 1)
	if err == nil || !strings.Contains(err.Error(), "directory source") {
		t.Fatalf("expected dir-source removal error, got: %v", err)
	}
}

func TestPlaylistBookmarkMaterializesDirTrack(t *testing.T) {
	setupTestEnv(t)
	audio := t.TempDir()
	writeAudioFile(t, audio+"/a.mp3")
	if err := PlaylistCreate("Music", nil, "", []string{audio}); err != nil {
		t.Fatalf("PlaylistCreate: %v", err)
	}
	out, err := captureStdout(t, func() error { return PlaylistBookmark("Music", 1) })
	if err != nil {
		t.Fatalf("PlaylistBookmark: %v", err)
	}
	if !strings.Contains(out, "★") {
		t.Errorf("output = %q, want bookmarked marker", out)
	}
	// The bookmark persists after reload because the track was materialized.
	out, err = captureStdout(t, func() error { return PlaylistBookmarks() })
	if err != nil {
		t.Fatalf("PlaylistBookmarks: %v", err)
	}
	if !strings.Contains(out, "1 bookmarks") {
		t.Errorf("PlaylistBookmarks = %q, want 1 bookmark", out)
	}
}

func TestPlaylistCreateNoPartialWhenNoAudio(t *testing.T) {
	setupTestEnv(t)
	audio := t.TempDir()
	writeAudioFile(t, audio+"/a.mp3")
	empty := t.TempDir()

	// Audio collection fails before the playlist is created, so a dirs+paths
	// create with no audio must leave nothing behind.
	if err := PlaylistCreate("mix", []string{empty}, "", []string{audio}); err == nil {
		t.Fatal("create with no audio should fail")
	}
	p, err := newProvider()
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}
	if p.Exists("mix") {
		t.Fatal("failed create left a playlist behind")
	}
}

func TestPlaylistAddDirNoPartialWhenAudioFails(t *testing.T) {
	setupTestEnv(t)
	audio := t.TempDir()
	writeAudioFile(t, audio+"/a.mp3")
	if err := PlaylistCreate("mix", nil, "", nil); err != nil {
		t.Fatalf("PlaylistCreate: %v", err)
	}

	// The bad path fails audio collection before any directory is added.
	bad := t.TempDir()
	if err := PlaylistAdd("mix", []string{bad}, []string{audio}); err == nil {
		t.Fatal("add with no audio should fail")
	}
	out, err := captureStdout(t, func() error { return PlaylistDirs("mix") })
	if err != nil {
		t.Fatalf("PlaylistDirs: %v", err)
	}
	if !strings.Contains(out, "no directory sources") {
		t.Fatalf("directory source persisted despite failed add: %q", out)
	}
}

func TestPlaylistAddDirBatchAtomic(t *testing.T) {
	setupTestEnv(t)
	audio := t.TempDir()
	writeAudioFile(t, audio+"/a.mp3")
	if err := PlaylistCreate("mix", nil, "", nil); err != nil {
		t.Fatalf("PlaylistCreate: %v", err)
	}

	valid := t.TempDir()
	missing := t.TempDir() + "/missing"
	if err := PlaylistAdd("mix", nil, []string{valid, missing}); err == nil {
		t.Fatal("add with missing dir should fail")
	}
	out, err := captureStdout(t, func() error { return PlaylistDirs("mix") })
	if err != nil {
		t.Fatalf("PlaylistDirs: %v", err)
	}
	if !strings.Contains(out, "no directory sources") {
		t.Fatalf("valid dir persisted despite failing batch: %q", out)
	}
}
