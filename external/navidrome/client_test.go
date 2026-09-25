package navidrome

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/internal/subsonicapi"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// Protocol-level tests live in internal/subsonicapi; these cover only the
// adapter surface: constructor gating and Navidrome-specific wiring.

// oneTrackServer answers getPlaylist with a single track so tests can inspect
// the stream URL the adapter produces.
func oneTrackServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"subsonic-response": {
				"status": "ok",
				"playlist": {"entry": [{"id":"1","title":"T","artist":"A","album":"Al","duration":60}]}
			}
		}`))
	}))
}

func streamQuery(t *testing.T, c *NavidromeClient) url.Values {
	t.Helper()
	tracks, err := c.Tracks("pl-1")
	if err != nil {
		t.Fatalf("Tracks() error: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("expected 1 track, got %d", len(tracks))
	}
	u, err := url.Parse(tracks[0].Path)
	if err != nil {
		t.Fatalf("stream URL invalid: %v", err)
	}
	if !IsSubsonicStreamURL(tracks[0].Path) {
		t.Errorf("IsSubsonicStreamURL(%q) = false, want true", tracks[0].Path)
	}
	return u.Query()
}

func TestName(t *testing.T) {
	c := New("http://localhost", "u", "p")
	if c.Name() != "Navidrome" {
		t.Errorf("Name() = %q, want %q", c.Name(), "Navidrome")
	}
}

func TestNewFromEnv(t *testing.T) {
	t.Setenv("NAVIDROME_URL", "")
	t.Setenv("NAVIDROME_USER", "")
	t.Setenv("NAVIDROME_PASS", "")
	if c := NewFromEnv(config.NavidromeConfig{}); c != nil {
		t.Error("NewFromEnv(config.NavidromeConfig{}) should return nil when env vars are empty")
	}

	t.Setenv("NAVIDROME_URL", "https://music.test")
	t.Setenv("NAVIDROME_USER", "alice")
	t.Setenv("NAVIDROME_PASS", "secret")
	c := NewFromEnv(config.NavidromeConfig{})
	if c == nil {
		t.Fatal("NewFromEnv(config.NavidromeConfig{}) returned nil with all env vars set")
	}
	if c.BaseURL() != "https://music.test" {
		t.Errorf("BaseURL() = %q, want the NAVIDROME_URL value", c.BaseURL())
	}
}

func TestNewFromConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.NavidromeConfig
		wantNil bool
	}{
		{"empty", config.NavidromeConfig{}, true},
		{"no password", config.NavidromeConfig{URL: "http://localhost", User: "u"}, true},
		{"no url", config.NavidromeConfig{User: "u", Password: "p"}, true},
		{"valid", config.NavidromeConfig{URL: "http://localhost", User: "u", Password: "p"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewFromConfig(tt.cfg)
			if (c == nil) != tt.wantNil {
				t.Errorf("NewFromConfig(%+v) nil=%v, want nil=%v", tt.cfg, c == nil, tt.wantNil)
			}
		})
	}
}

func TestNewFromEnv_ConfigSettings(t *testing.T) {
	srv := oneTrackServer(t)
	defer srv.Close()

	cfg := config.NavidromeConfig{
		URL: "https://config.test", User: "config-user", Password: "config-password",
		Format: " RAW ", BrowseSort: subsonicapi.SortNewest, ScrobbleDisabled: true,
	}
	for _, missing := range []string{"", "NAVIDROME_URL", "NAVIDROME_USER", "NAVIDROME_PASS"} {
		name := missing
		if name == "" {
			name = "complete credentials"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("NAVIDROME_URL", srv.URL)
			t.Setenv("NAVIDROME_USER", "env-user")
			t.Setenv("NAVIDROME_PASS", "env-password")
			if missing != "" {
				t.Setenv(missing, "")
			}
			c := NewFromEnv(cfg)
			if missing != "" {
				if c != nil {
					t.Fatal("expected nil when an environment credential is missing")
				}
				return
			}
			if c == nil {
				t.Fatal("expected client with complete environment credentials")
			}
			if c.BaseURL() != srv.URL {
				t.Error("client did not use environment credentials")
			}
			q := streamQuery(t, c)
			if got := q.Get("u"); got != "env-user" {
				t.Errorf("user = %q, want env-user", got)
			}
			if got := q.Get("format"); got != "raw" {
				t.Errorf("format = %q, want raw", got)
			}
			if got := c.DefaultAlbumSort(); got != subsonicapi.SortNewest {
				t.Errorf("browse sort = %q, want %q", got, subsonicapi.SortNewest)
			}
			if c.CanReportPlayback(trackWithMeta("song-1")) {
				t.Error("scrobbling should stay disabled from config")
			}
		})
	}
}

func TestNewFromConfig_BrowseSort(t *testing.T) {
	cfg := config.NavidromeConfig{
		URL: "http://localhost", User: "u", Password: "p",
		BrowseSort: subsonicapi.SortNewest,
	}
	c := NewFromConfig(cfg)
	if c.DefaultAlbumSort() != subsonicapi.SortNewest {
		t.Errorf("DefaultAlbumSort() = %q, want %q", c.DefaultAlbumSort(), subsonicapi.SortNewest)
	}
}

func TestNewFromConfig_ScrobbleDisabled(t *testing.T) {
	cfg := config.NavidromeConfig{
		URL: "http://localhost", User: "u", Password: "p",
		ScrobbleDisabled: true,
	}
	c := NewFromConfig(cfg)
	if c.CanReportPlayback(trackWithMeta("song-1")) {
		t.Error("CanReportPlayback() = true when scrobbling is disabled")
	}
}

func TestStreamURLFormat(t *testing.T) {
	srv := oneTrackServer(t)
	defer srv.Close()

	tests := []struct {
		name       string
		format     string
		wantFormat string
	}{
		{"default lets the server decide", "", ""},
		{"raw requests the original file", "raw", "raw"},
		{"explicit format is passed through", "mp3", "mp3"},
		{"uppercase raw", "RAW", "raw"},
		{"surrounding whitespace and uppercase", " MP3 ", "mp3"},
		{"whitespace only lets the server decide", " \t", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewFromConfig(config.NavidromeConfig{URL: srv.URL, User: "alice", Password: "secret", Format: tt.format})
			q := streamQuery(t, c)
			if !strings.HasSuffix(q.Get("id"), "1") {
				t.Errorf("id = %q, want 1", q.Get("id"))
			}
			if _, present := q["format"]; present != (tt.wantFormat != "") {
				t.Errorf("format param present = %v, want %v", present, tt.wantFormat != "")
			}
			if got := q.Get("format"); got != tt.wantFormat {
				t.Errorf("format = %q, want %q", got, tt.wantFormat)
			}
		})
	}
}

func trackWithMeta(id string) playlist.Track {
	return playlist.Track{ProviderMeta: map[string]string{provider.MetaNavidromeID: id}}
}
