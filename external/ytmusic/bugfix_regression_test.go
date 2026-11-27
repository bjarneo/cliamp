package ytmusic

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
	"golang.org/x/oauth2"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"
)

func TestClassifyPlaylistsSharedFirstVideoMarksAllMatchingPlaylists(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/youtube/v3/playlistItems":
			// Both PL1 and PL2 start with the same music video ID "vid-shared".
			_, _ = w.Write([]byte(`{"items":[{"contentDetails":{"videoId":"vid-shared"}}]}`))
		case "/youtube/v3/videos":
			_, _ = w.Write([]byte(`{"items":[{"id":"vid-shared","snippet":{"categoryId":"10"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	svc, err := youtube.NewService(context.Background(),
		option.WithHTTPClient(srv.Client()),
		option.WithEndpoint(srv.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatal(err)
	}

	got := classifyPlaylists(context.Background(), svc, []playlistEntry{
		{ID: "PL1", Name: "Mix 1", TrackCount: 5},
		{ID: "PL2", Name: "Mix 2", TrackCount: 5},
	}, nil, "test-scope")

	if !got["PL1"] || !got["PL2"] {
		t.Fatalf("classifyPlaylists = %v, want both PL1 and PL2 true", got)
	}
}

func TestIsAuthErrAndMapErr(t *testing.T) {
	b := newBase(&Session{}, "cid", "csec", false)
	origSess := b.session

	if err := b.mapErr(origSess, errors.New("temporary network outage")); errors.Is(err, playlist.ErrNeedsAuth) {
		t.Fatalf("network error wrongly mapped to ErrNeedsAuth: %v", err)
	}
	if b.session == nil {
		t.Fatal("network error cleared cached session")
	}

	revoked := &oauth2.RetrieveError{ErrorCode: "invalid_grant"}
	if err := b.mapErr(origSess, revoked); !errors.Is(err, playlist.ErrNeedsAuth) {
		t.Fatalf("invalid_grant mapped to %v, want ErrNeedsAuth", err)
	}
	if b.session != nil {
		t.Fatal("invalid_grant did not clear cached session")
	}

	b.session = &Session{}
	unauth := &googleapi.Error{Code: http.StatusUnauthorized}
	if err := b.mapErr(b.session, unauth); !errors.Is(err, playlist.ErrNeedsAuth) {
		t.Fatalf("401 googleapi.Error mapped to %v, want ErrNeedsAuth", err)
	}
	if b.session != nil {
		t.Fatal("401 googleapi.Error did not clear cached session")
	}
}

func TestClosePreventsStaleAuthenticateInstall(t *testing.T) {
	origSignIn := signIn
	t.Cleanup(func() { signIn = origSignIn })

	entered := make(chan struct{})
	release := make(chan struct{})
	signIn = func(context.Context, string, string) (*Session, error) {
		close(entered)
		<-release
		return &Session{cacheScope: "late"}, nil
	}

	b := newBase(nil, "cid", "csec", false)
	done := make(chan error, 1)
	go func() {
		done <- b.authenticate()
	}()
	<-entered
	b.close()
	close(release)
	<-done

	b.mu.Lock()
	got := b.session
	b.mu.Unlock()
	if got != nil {
		t.Fatal("stale Authenticate installed session after Close")
	}
}
