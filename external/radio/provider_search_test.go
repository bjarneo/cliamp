package radio

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
