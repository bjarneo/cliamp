package local

import (
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

// The TUI, the daemon and the `cliamp playlist` CLI each build their own
// Provider. Two Provider values that change one playlist at the same time
// must not lose each other's change.
func TestSeparateProvidersKeepConcurrentChanges(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fileutil.LockFile takes no lock on Windows")
	}
	const n = 15
	const name = "Mix"
	track := func(worker, i int) playlist.Track {
		return playlist.Track{Path: fmt.Sprintf("/w%d-%d.mp3", worker, i)}
	}
	addTrack := func(p *Provider, i int) error {
		_, _, err := p.AddTracks(name, []playlist.Track{track(1, i)})
		return err
	}
	addDirs := func(p *Provider, dirs []string) error {
		_, err := p.AddDirSources(name, dirs)
		return err
	}

	tests := []struct {
		name       string
		setup      func(p *Provider, dirs []string) error
		op         func(p *Provider, dirs []string, i int) error
		wantTracks int
		wantDirs   int
		wantRecurs bool
	}{
		{
			name: "AddTracks",
			op: func(p *Provider, _ []string, i int) error {
				_, _, err := p.AddTracks(name, []playlist.Track{track(0, i)})
				return err
			},
			wantTracks: 2 * n,
		},
		{
			name: "PrependTracks",
			op: func(p *Provider, _ []string, i int) error {
				_, _, _, err := p.PrependTracks(name, []playlist.Track{track(0, i)})
				return err
			},
			wantTracks: 2 * n,
		},
		{
			// Removals take index 0 and the adds append, so each removal
			// hits one of the n tracks that the setup saved.
			name: "RemoveTrack",
			setup: func(p *Provider, _ []string) error {
				base := make([]playlist.Track, n)
				for i := range base {
					base[i] = track(0, i)
				}
				return p.SavePlaylist(name, base)
			},
			op: func(p *Provider, _ []string, _ int) error {
				return p.RemoveTrack(name, 0)
			},
			wantTracks: n,
		},
		{
			name: "AddDirSources",
			op: func(p *Provider, dirs []string, i int) error {
				_, err := p.AddDirSources(name, []string{dirs[i]})
				return err
			},
			wantTracks: n,
			wantDirs:   n,
			wantRecurs: true,
		},
		{
			name: "UpdatePlaylist",
			setup: func(p *Provider, _ []string) error {
				return p.SavePlaylist(name, nil)
			},
			op: func(p *Provider, _ []string, i int) error {
				return p.UpdatePlaylist(name, func(tracks []playlist.Track) ([]playlist.Track, error) {
					return append(tracks, track(0, i)), nil
				})
			},
			wantTracks: 2 * n,
		},
		{
			name:  "RemoveDirSource",
			setup: addDirs,
			op: func(p *Provider, dirs []string, i int) error {
				return p.RemoveDirSource(name, dirs[i])
			},
			wantTracks: n,
		},
		{
			name:  "SetDirRecursive",
			setup: addDirs,
			op: func(p *Provider, dirs []string, i int) error {
				return p.SetDirRecursive(name, dirs[i], false)
			},
			wantTracks: n,
			wantDirs:   n,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name+" and AddTracks", func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "playlists")
			dirs := make([]string, n)
			for i := range dirs {
				dirs[i] = t.TempDir()
			}
			a, b := &Provider{dir: dir}, &Provider{dir: dir}
			if tt.setup != nil {
				if err := tt.setup(a, dirs); err != nil {
					t.Fatalf("setup: %v", err)
				}
			}

			errs := make(chan error, 2*n)
			var wg sync.WaitGroup
			wg.Go(func() {
				for i := range n {
					errs <- tt.op(a, dirs, i)
				}
			})
			wg.Go(func() {
				for i := range n {
					errs <- addTrack(b, i)
				}
			})
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatalf("concurrent change: %v", err)
				}
			}

			tracks, err := a.Tracks(name)
			if err != nil {
				t.Fatalf("Tracks: %v", err)
			}
			if len(tracks) != tt.wantTracks {
				t.Errorf("got %d tracks, want %d: %v", len(tracks), tt.wantTracks, paths(tracks))
			}
			got, err := a.DirSources(name)
			if err != nil {
				t.Fatalf("DirSources: %v", err)
			}
			if len(got) != tt.wantDirs {
				t.Errorf("got %d dir sources, want %d", len(got), tt.wantDirs)
			}
			for _, src := range got {
				if src.Recursive != tt.wantRecurs {
					t.Errorf("dir source %s recursive = %v, want %v", src.Path, src.Recursive, tt.wantRecurs)
				}
			}
		})
	}
}

// TestWritesDoNotReadDirsUnderLock verifies which directory work a playlist
// write does while it holds the playlist lock. The TUI takes the lock from
// its update loop, so a long scan there freezes the UI. AddTracks and
// PrependTracks check a path against each [[dir]] source without a walk.
// RemoveTrack walks the directories to map the index but reads no tags.
func TestWritesDoNotReadDirsUnderLock(t *testing.T) {
	tests := []struct {
		name            string
		write           func(p *Provider, music string) error
		wantLockedScans int
		wantPaths       []string // explicit tracks after the write, relative to the music dir or absolute
	}{
		{
			name: "AddTracks",
			write: func(p *Provider, music string) error {
				_, _, err := p.AddTracks("Mix", []playlist.Track{
					{Path: filepath.Join(music, "dir.mp3")},
					{Path: filepath.Join(music, "missing.mp3")},
					{Path: "/new.mp3"},
				})
				return err
			},
			wantPaths: []string{"/a.mp3", "missing.mp3", "/new.mp3"},
		},
		{
			name: "PrependTracks",
			write: func(p *Provider, music string) error {
				_, _, _, err := p.PrependTracks("Mix", []playlist.Track{
					{Path: filepath.Join(music, "dir.mp3")},
					{Path: filepath.Join(music, "missing.mp3")},
					{Path: "/new.mp3"},
				})
				return err
			},
			wantPaths: []string{"missing.mp3", "/new.mp3", "/a.mp3"},
		},
		{
			name:            "RemoveTrack",
			write:           func(p *Provider, _ string) error { return p.RemoveTrack("Mix", 0) },
			wantLockedScans: 1,
			wantPaths:       nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestProvider(t)
			music := t.TempDir()
			writeAudioFile(t, filepath.Join(music, "dir.mp3"))
			if err := p.SavePlaylist("Mix", []playlist.Track{{Path: "/a.mp3"}}); err != nil {
				t.Fatal(err)
			}
			if _, err := p.AddDirSources("Mix", []string{music}); err != nil {
				t.Fatal(err)
			}

			// held reports whether the caller holds the playlist lock.
			held := func() bool {
				if p.mu.TryLock() {
					p.mu.Unlock()
					return false
				}
				return true
			}
			lockedScans, lockedTagReads := 0, 0
			origScan, origTags := scanDir, readTags
			t.Cleanup(func() { scanDir, readTags = origScan, origTags })
			scanDir = func(dir string, recursive bool) ([]string, error) {
				if held() {
					lockedScans++
				}
				return origScan(dir, recursive)
			}
			readTags = func(files []string) []playlist.Track {
				if held() {
					lockedTagReads++
				}
				return origTags(files)
			}

			if err := tt.write(p, music); err != nil {
				t.Fatal(err)
			}
			if lockedScans != tt.wantLockedScans {
				t.Errorf("directory scans under the lock = %d, want %d", lockedScans, tt.wantLockedScans)
			}
			if lockedTagReads != 0 {
				t.Errorf("tag reads under the lock = %d, want 0", lockedTagReads)
			}

			doc, err := p.loadDocByName("Mix")
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			for _, path := range tt.wantPaths {
				// The test tracks /a.mp3 and /new.mp3 are not absolute on
				// Windows, but the playlist file keeps them as written.
				if !strings.HasPrefix(path, "/") {
					path = filepath.Join(music, path)
				}
				want = append(want, path)
			}
			if got := paths(doc.tracks); !slices.Equal(got, want) {
				t.Errorf("explicit tracks = %v, want %v", got, want)
			}
		})
	}
}
