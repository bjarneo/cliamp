package radio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestProviderSearchCatalogPendingLookup(t *testing.T) {
	for _, tt := range []struct {
		name          string
		update        func(*testing.T, *Provider)
		wantSearching bool
		wantID        string
		wantName      string
		wantCount     int
	}{
		{
			name:     "clear before results",
			update:   func(_ *testing.T, p *Provider) { p.ClearSearch() },
			wantID:   "l:0",
			wantName: builtinName,
		},
		{
			name: "newer search wins",
			update: func(t *testing.T, p *Provider) {
				if n, err := p.SearchCatalog("new"); n != 1 || err != nil {
					t.Fatalf("new SearchCatalog = (%d, %v), want (1, nil)", n, err)
				}
			},
			wantSearching: true,
			wantID:        "s:0",
			wantName:      "new",
		},
		{
			name: "explicit results win",
			update: func(_ *testing.T, p *Provider) {
				p.SetSearchResults([]CatalogStation{{Name: "explicit", URL: "https://explicit.example/stream"}})
			},
			wantSearching: true,
			wantID:        "s:0",
			wantName:      "explicit",
		},
		{
			name:          "refresh preserves search",
			update:        func(_ *testing.T, p *Provider) { p.Refresh() },
			wantSearching: true,
			wantID:        "s:0",
			wantName:      "old",
			wantCount:     1,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestProvider(t)
			started := make(chan struct{})
			release := make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				name := r.URL.Query().Get("name")
				if name == "old" {
					close(started)
					<-release
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode([]CatalogStation{{Name: name, URL: "https://" + name + ".example/stream"}})
			}))
			t.Cleanup(srv.Close)
			installCatalogClient(t, srv.URL)

			var count int
			var err error
			done := make(chan struct{})
			go func() {
				count, err = p.SearchCatalog("old")
				close(done)
			}()
			t.Cleanup(func() {
				unblock()
				<-done
			})

			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for the old search request")
			}
			if p.IsSearching() {
				t.Fatal("pending lookup should not activate search results")
			}
			tt.update(t, p)
			unblock()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for the old search to finish")
			}
			if err != nil {
				t.Fatalf("old SearchCatalog: %v", err)
			}
			if count != tt.wantCount {
				t.Errorf("old SearchCatalog count = %d, want %d", count, tt.wantCount)
			}
			if got := p.IsSearching(); got != tt.wantSearching {
				t.Errorf("IsSearching = %v, want %v", got, tt.wantSearching)
			}
			infos, err := p.Playlists()
			if err != nil {
				t.Fatalf("Playlists: %v", err)
			}
			if len(infos) != 1 || infos[0].ID != tt.wantID || infos[0].Name != tt.wantName {
				t.Fatalf("Playlists = %+v, want %s (%s)", infos, tt.wantName, tt.wantID)
			}
		})
	}
}

func TestProviderSearchStations(t *testing.T) {
	const body = `[
		{"name":"Jazz One","url_resolved":"https://one.example/stream","country":"Norway","bitrate":128},
		{"name":"Bad","url_resolved":"ssh://attacker.example/cmd"},
		{"name":"Jazz Two","url_resolved":"http://two.example/stream"}]`
	for _, tt := range []struct {
		name      string
		limit     int
		cancelled bool
		want      []string
		wantErr   bool
	}{
		{name: "streamable stations", limit: 10, want: []string{"Jazz One", "Jazz Two"}},
		{name: "limit", limit: 1, want: []string{"Jazz One"}},
		{name: "no limit", limit: 0, want: []string{"Jazz One", "Jazz Two"}},
		{name: "cancelled", limit: 10, cancelled: true, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				if q.Get("name") != "jazz" || q.Get("order") != string(SortVotes) || q.Get("limit") != "200" {
					t.Errorf("query = %v, want name jazz, order votes and limit 200", q)
				}
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			installCatalogClient(t, srv.URL)
			p := newTestProvider(t)
			p.SetSearchResults([]CatalogStation{{Name: "pane", URL: "https://pane.example/stream"}})

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.cancelled {
				cancel()
			}
			tracks, err := p.SearchStations(ctx, "jazz", tt.limit)
			if (err != nil) != tt.wantErr {
				t.Fatalf("SearchStations() error = %v, wantErr %v", err, tt.wantErr)
			}
			var names []string
			for _, track := range tracks {
				if !track.Stream || !track.Realtime || track.ProviderMeta["radio.url"] != track.Path {
					t.Errorf("track = %+v, want a live stream with its radio.url", track)
				}
				names = append(names, track.Title)
			}
			if !slices.Equal(names, tt.want) {
				t.Errorf("SearchStations() titles = %q, want %q", names, tt.want)
			}
			// The pane search keeps its own results.
			lists, err := p.Playlists()
			if err != nil {
				t.Fatal(err)
			}
			if len(lists) != 1 || lists[0].Name != "pane" {
				t.Errorf("pane rows = %+v, want the pane search row", lists)
			}
		})
	}
}
