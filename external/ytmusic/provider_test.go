package ytmusic

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
)

func TestRefreshInvalidatesAllCaches(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	b := newBase(nil, "client-id", "client-secret", false)

	b.allPlaylists = []playlistEntry{{ID: "p1", Name: "One", TrackCount: 5}}
	b.classified = map[string]bool{"p1": true}
	b.trackCache["p1"] = []playlist.Track{{Path: "https://example/v", Title: "t"}}

	dc := b.ensureDiskCache()
	dc.setPlaylists(b.allPlaylists)
	dc.setTracks("p1", b.trackCache["p1"])
	saveSnapshot(dc.snapshot())

	if !dc.playlistsFresh() {
		t.Fatal("disk cache should be fresh before refresh")
	}

	b.refresh()

	if b.allPlaylists != nil {
		t.Error("allPlaylists not cleared")
	}
	if b.classified != nil {
		t.Error("classified not cleared")
	}
	if len(b.trackCache) != 0 {
		t.Errorf("trackCache not cleared: %d entries", len(b.trackCache))
	}

	if b.disk.playlistsFresh() {
		t.Error("disk cache still reports fresh after refresh")
	}
	if !b.disk.PlaylistsAt.IsZero() {
		t.Errorf("PlaylistsAt should be zero, got %v", b.disk.PlaylistsAt)
	}
	if len(b.disk.Playlists) != 0 {
		t.Errorf("disk Playlists not cleared: %d entries", len(b.disk.Playlists))
	}
	if len(b.disk.Tracks) != 0 {
		t.Errorf("disk Tracks not cleared: %d entries", len(b.disk.Tracks))
	}

	reloaded := loadYTCache(storedOAuthCacheScope("client-id"))
	if reloaded.playlistsFresh() {
		t.Error("reloaded disk cache still fresh after refresh")
	}
	if !reloaded.PlaylistsAt.Equal(time.Time{}) {
		t.Errorf("reloaded PlaylistsAt should be zero, got %v", reloaded.PlaylistsAt)
	}
	if len(reloaded.Tracks) != 0 {
		t.Errorf("reloaded disk Tracks not cleared: %d entries", len(reloaded.Tracks))
	}
}

// The three OAuth providers share one base. The Close of each one ends the
// sign-in in progress and drops the session, and every later Close on any of
// the three does nothing more. So a shutdown that closes all three is safe.
func TestProvidersCloseSharedBase(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, tt := range []struct {
		name  string
		close func(Providers)
	}{
		{name: "music", close: func(p Providers) { p.Music.Close() }},
		{name: "video", close: func(p Providers) { p.Video.Close() }},
		{name: "all", close: func(p Providers) { p.All.Close() }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			provs := New(&Session{}, "client-id", "client-secret", false)
			cancels := 0
			provs.Music.base.authCancel = func() { cancels++ }

			tt.close(provs)
			if cancels != 1 {
				t.Fatalf("sign-in cancels = %d, want 1", cancels)
			}
			if provs.Music.base.session != nil {
				t.Fatal("session still set after Close")
			}

			provs.Music.Close()
			provs.Video.Close()
			provs.All.Close()
			if cancels != 1 {
				t.Fatalf("sign-in cancels after a second Close = %d, want 1", cancels)
			}
		})
	}
}

// TestAuthenticateCancelsEarlierFlow runs three overlapping sign-ins. Each
// new call must cancel the one before it, also after an older call returns
// late, and Close must cancel the last one.
func TestAuthenticateCancelsEarlierFlow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	type flow struct {
		ctx     context.Context
		release chan struct{}
	}
	started := make(chan flow)
	orig := signIn
	t.Cleanup(func() { signIn = orig })
	signIn = func(ctx context.Context, _, _ string) (*Session, error) {
		f := flow{ctx, make(chan struct{})}
		started <- f
		<-f.release
		return nil, ctx.Err()
	}

	p := New(nil, "client-id", "client-secret", false).Music
	errs := make(chan error, 3)
	var flows []flow
	// finish lets flow i return and checks that it ended as canceled.
	finish := func(i int) {
		t.Helper()
		close(flows[i].release)
		if err := <-errs; !errors.Is(err, context.Canceled) {
			t.Fatalf("sign-in %d error = %v, want context.Canceled", i+1, err)
		}
	}
	for i := range 3 {
		go func() { errs <- p.Authenticate() }()
		flows = append(flows, <-started)
		if i == 0 {
			continue
		}
		if flows[i-1].ctx.Err() == nil {
			t.Fatalf("sign-in %d did not cancel sign-in %d", i+1, i)
		}
		// The older call returns after the newer call took over.
		finish(i - 1)
	}
	p.Close()
	if flows[2].ctx.Err() == nil {
		t.Fatal("Close did not cancel the last sign-in")
	}
	finish(2)
}

// TestSilentSessionCheckKeepsSignIn starts a browser sign-in on YouTube
// Music. A silent session check from any of the three providers must leave
// that sign-in running and report that sign-in is required.
func TestSilentSessionCheckKeepsSignIn(t *testing.T) {
	for _, tt := range []struct {
		name  string
		check func(Providers) error
	}{
		{name: "ensure session", check: func(p Providers) error { return p.Video.base.ensureSession() }},
		{name: "video playlists", check: func(p Providers) error { _, err := p.Video.Playlists(); return err }},
		{name: "all playlists", check: func(p Providers) error { _, err := p.All.Playlists(); return err }},
		{name: "music tracks", check: func(p Providers) error { _, err := p.Music.Tracks("p1"); return err }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())

			started := make(chan context.Context)
			release := make(chan struct{})
			orig := signIn
			t.Cleanup(func() { signIn = orig })
			signIn = func(ctx context.Context, _, _ string) (*Session, error) {
				started <- ctx
				<-release
				return nil, ctx.Err()
			}

			provs := New(nil, "client-id", "client-secret", false)
			done := make(chan error, 1)
			go func() { done <- provs.Music.Authenticate() }()
			flow := <-started

			if err := tt.check(provs); !errors.Is(err, playlist.ErrNeedsAuth) {
				t.Errorf("silent check error = %v, want ErrNeedsAuth", err)
			}
			if flow.Err() != nil {
				t.Errorf("silent check cancelled the sign-in: %v", flow.Err())
			}

			provs.Music.Close()
			close(release)
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("sign-in error = %v, want context.Canceled", err)
			}
		})
	}
}
