package cmd

import (
	"slices"
	"strconv"
	"testing"

	"github.com/bjarneo/cliamp/config"
)

// TestSetupBodyRoundTrip saves each provider body with values that need
// escapes and checks that config.Load reads the same values back. It covers
// every provider spec.
func TestSetupBodyRoundTrip(t *testing.T) {
	const (
		backslash = `a\b`
		quote     = `p"w`
		single    = `'x'`
		mixed     = `p"w\#1 x`
		nbsp      = "pass\u00a0word"
	)
	t.Setenv("CLIAMP_TEST_SETUP_PASS", "from-env")
	tests := []struct {
		section string
		values  map[string]string
		got     func(config.Config) []string
		want    []string
	}{
		{
			section: "navidrome",
			values:  map[string]string{"url": "https://h/" + backslash, "user": single, "password": mixed},
			got: func(c config.Config) []string {
				return []string{c.Navidrome.URL, c.Navidrome.User, c.Navidrome.Password}
			},
			want: []string{"https://h/" + backslash, single, mixed},
		},
		{
			// Setup writes an environment reference as typed, and Load
			// reads the variable.
			section: "navidrome",
			values:  map[string]string{"url": "https://h", "user": "alice", "password": "${CLIAMP_TEST_SETUP_PASS}"},
			got: func(c config.Config) []string {
				return []string{c.Navidrome.Password}
			},
			want: []string{"from-env"},
		},
		{
			section: "lyrion",
			values:  map[string]string{"url": "http://nas:9000", "user": backslash + `\`, "password": nbsp},
			got: func(c config.Config) []string {
				return []string{c.Lyrion.URL, c.Lyrion.User, c.Lyrion.Password}
			},
			want: []string{"http://nas:9000", backslash + `\`, nbsp},
		},
		{
			section: "plex",
			values:  map[string]string{"url": "http://plex:32400", "token": mixed, "libraries": `Mus"ic, Ja\zz`},
			got: func(c config.Config) []string {
				return append([]string{c.Plex.URL, c.Plex.Token}, c.Plex.Libraries...)
			},
			want: []string{"http://plex:32400", mixed, `Mus"ic`, `Ja\zz`},
		},
		{
			section: "jellyfin",
			values:  map[string]string{keyJellyfinAuth: "password", "url": "https://jf", "user": single, "password": mixed},
			got: func(c config.Config) []string {
				return []string{c.Jellyfin.URL, c.Jellyfin.User, c.Jellyfin.Password}
			},
			want: []string{"https://jf", single, mixed},
		},
		{
			section: "emby",
			values:  map[string]string{keyEmbyAuth: "token", "url": "https://emby", "token": mixed, "user": quote},
			got: func(c config.Config) []string {
				return []string{c.Emby.URL, c.Emby.Token, c.Emby.User}
			},
			want: []string{"https://emby", mixed, quote},
		},
		{
			section: "audiobookshelf",
			values:  map[string]string{keyABSAuth: "password", "url": "https://abs", "user": backslash, "password": quote},
			got: func(c config.Config) []string {
				return []string{c.Audiobookshelf.URL, c.Audiobookshelf.User, c.Audiobookshelf.Password}
			},
			want: []string{"https://abs", backslash, quote},
		},
		{
			section: "spotify",
			values:  map[string]string{keySpotifyMode: "custom", "client_id": mixed, "bitrate": "160"},
			got: func(c config.Config) []string {
				return []string{c.Spotify.ClientID}
			},
			want: []string{mixed},
		},
		{
			section: "qobuz",
			values:  map[string]string{keyQobuzQuality: "27"},
			got: func(c config.Config) []string {
				return []string{strconv.FormatBool(c.Qobuz.IsSet()), strconv.Itoa(c.Qobuz.Quality)}
			},
			want: []string{"true", "27"},
		},
		{
			section: "tidal",
			values:  map[string]string{keyTidalQuality: "hires"},
			got: func(c config.Config) []string {
				return []string{strconv.FormatBool(c.Tidal.IsSet()), c.Tidal.Quality}
			},
			want: []string{"true", "hires"},
		},
		{
			section: "netease",
			values:  map[string]string{keyNetEaseBrowser: "custom", "cookies_from": `chrome:Profile "1"`, "user_id": backslash},
			got: func(c config.Config) []string {
				return []string{c.NetEase.CookiesFrom, c.NetEase.UserID}
			},
			want: []string{`chrome:Profile "1"`, backslash},
		},
		{
			section: "mixcloud",
			values: map[string]string{
				keyMixcloudBrowser: "custom",
				"username":         single,
				"access_token":     mixed,
				"cookies_from":     `firefox:` + backslash,
				"styles":           `deep"house, jazz\`,
			},
			got: func(c config.Config) []string {
				return append([]string{c.Mixcloud.Username, c.Mixcloud.AccessToken, c.Mixcloud.CookiesFrom}, c.Mixcloud.Styles...)
			},
			want: []string{single, mixed, `firefox:` + backslash, `deep"house`, `jazz\`},
		},
		{
			section: "ytmusic",
			values:  map[string]string{keyYTMusicMode: "custom", "client_id": quote, "client_secret": mixed, "cookies_from": backslash},
			got: func(c config.Config) []string {
				return []string{c.YouTubeMusic.ClientID, c.YouTubeMusic.ClientSecret, c.YouTubeMusic.CookiesFrom}
			},
			want: []string{quote, mixed, backslash},
		},
	}

	tested := map[string]bool{}
	for _, tt := range tests {
		tested[tt.section] = true
		t.Run(tt.section, func(t *testing.T) {
			t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
			saveSetup(t, tt.section, tt.values)
			cfg, err := config.Load()
			if err != nil {
				t.Fatalf("config.Load: %v", err)
			}
			if got := tt.got(cfg); !slices.Equal(got, tt.want) {
				t.Fatalf("round trip got  %q\nwant %q", got, tt.want)
			}
		})
	}
	for _, p := range providers() {
		if !tested[p.section] {
			t.Errorf("no round trip case for [%s]", p.section)
		}
	}
}
