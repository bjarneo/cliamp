package yandex

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestResolveSourceCancellation(t *testing.T) {
	for _, stage := range []string{"download-info", "signed-info"} {
		t.Run(stage, func(t *testing.T) {
			started := make(chan struct{})
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if stage == "signed-info" && r.URL.Path != "/signed" {
					writeResult(w, []downloadInfo{{Codec: "mp3", BitrateInKbps: 320, DownloadInfoURL: server.URL + "/signed?x=1"}})
					return
				}
				close(started)
				<-r.Context().Done()
			}))
			defer server.Close()
			oldGuard := fullDownloadInfoGuard
			fullDownloadInfoGuard = func(string) error { return nil }
			defer func() { fullDownloadInfoGuard = oldGuard }()
			p := &Provider{api: &client{http: server.Client(), apiBase: server.URL}, urlCache: make(map[string]urlEntry)}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := p.ResolveSource(ctx, TrackURIPrefix+"1"); done <- err }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("resolution did not reach request")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error = %v, want cancellation", err)
				}
			case <-time.After(time.Second):
				t.Fatal("resolution did not cancel")
			}
		})
	}
}
