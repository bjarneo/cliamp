// Package cmd implements interactive subcommands invoked from the CLI.
// setup.go contains the provider onboarding wizard reachable via
// `cliamp setup`. It walks the user through configuring each remote
// provider (Navidrome, Lyrion, Bandcamp, Plex, Jellyfin, Emby, Audiobookshelf,
// Spotify, Qobuz, Tidal, NetEase, Mixcloud, YouTube Music, Telegram),
// validates the connection where possible, and writes the resulting
// TOML section to ~/.config/cliamp/config.toml.
//
// The UI is a small standalone Bubbletea+Lipgloss program — separate from
// the main player Model — because setup runs to completion and exits.
package cmd

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/external/audiobookshelf"
	"github.com/bjarneo/cliamp/external/bandcamp"
	"github.com/bjarneo/cliamp/external/emby"
	"github.com/bjarneo/cliamp/external/jellyfin"
	"github.com/bjarneo/cliamp/external/lyrion"
	"github.com/bjarneo/cliamp/external/mixcloud"
	"github.com/bjarneo/cliamp/external/navidrome"
	"github.com/bjarneo/cliamp/external/netease"
	"github.com/bjarneo/cliamp/external/plex"
	"github.com/bjarneo/cliamp/external/telegram"
)

// Setup launches the interactive wizard. Returns nil on clean exit.
func Setup() error {
	final, err := tea.NewProgram(newSetupModel()).Run()
	if err != nil {
		return err
	}
	// The sign-in prompts on the terminal, so it runs after the wizard exits
	// and gives the terminal back.
	sm, ok := final.(*setupModel)
	if !ok || !sm.signInRequested || sm.signInAction == nil {
		return nil
	}
	fmt.Fprintln(os.Stderr, "Connecting to Telegram...")
	if err := sm.signInAction(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "Sign-in failed: %v\nRun cliamp and it will ask again.\n", err)
		return nil
	}
	fmt.Fprintln(os.Stderr, "Signed in. Start cliamp when you are ready.")
	return nil
}

// ----- Provider specs -----------------------------------------------------

type fieldSpec struct {
	key      string
	label    string
	help     string // shown faintly under the field
	required bool
	secret   bool
	defaultV string
	// onlyIf, when non-nil, hides the field unless the predicate returns true
	// against the current values map. Used by Jellyfin for token-vs-password.
	onlyIf func(map[string]string) bool
}

// chatOption is one row of the chat picker: a dialog of the account.
type chatOption struct {
	ID      string
	Name    string
	Section string
}

type providerSpec struct {
	key     string
	name    string
	section string
	intro   []string // pre-form blurb (with help URLs)
	picker  *pickerSpec
	fields  []fieldSpec
	// validate runs a probe against the live server. Nil skips validation.
	validate func(map[string]string) error
	// body returns the keys that setup writes into the section.
	body func(map[string]string) []config.KeyValue
	// owned lists every key that setup manages in the section. A save
	// removes each owned key that body leaves out, such as token when
	// Jellyfin moves to password login. All other keys and comments stay.
	owned []string
	// extraValidate runs after the form before validate, e.g. to enforce
	// "token OR (user+password)" for Jellyfin.
	extraValidate func(map[string]string) error
	// signIn, when non-nil, offers an action on the success screen, such as
	// the Telegram sign-in. Setup runs it after the wizard exits because it
	// may prompt on the terminal. It returns the hint label and the action.
	signIn func(map[string]string) (string, func(context.Context) error)
	// prefill returns the values the form starts with, so a second run edits
	// the current config instead of blanking it.
	prefill func() map[string]string
	// summary, when non-nil, adds a short note to the saved result line, such
	// as the number of chats kept.
	summary func(map[string]string) string
	// pickChats lists the dialogs of the account for the chats field, plus the
	// ids the form already selects. Nil hides the picker.
	pickChats func(map[string]string) (chatPick, error)
}

// chatPick is what the picker receives: the dialogs, the ids already
// selected, and a warning for a selection that could not be resolved.
type chatPick struct {
	options  []chatOption
	selected []string
	warn     error
}

// pickerSpec is an optional radio choice shown before the fields. The
// selected option's key is stored in values[key]. fieldSpec.onlyIf can
// reference this to show different fields per choice.
type pickerSpec struct {
	key     string
	label   string
	options []pickerOption
}

type pickerOption struct {
	value string
	label string
}

// Picker keys are stored in the values map alongside real field keys; the
// leading underscore distinguishes them from TOML field names.
const (
	keyJellyfinAuth    = "_auth"
	keyEmbyAuth        = "_emby_auth"
	keyABSAuth         = "_abs_auth"
	keyNetEaseBrowser  = "_netease_browser"
	keyYTMusicMode     = "_mode"
	keySpotifyMode     = "_spotify_mode"
	keyQobuzQuality    = "_qobuz_quality"
	keyTidalQuality    = "_tidal_quality"
	keyMixcloudBrowser = "_mixcloud_browser"
	// keyPickChats is the form row that opens the chat checklist. It holds no
	// value: enter runs the picker.
	keyPickChats = "_pick"
)

func providers() []providerSpec {
	return []providerSpec{
		{
			key:     "navidrome",
			name:    "Navidrome / Subsonic",
			section: "navidrome",
			intro: []string{
				"Self-hosted music server using the Subsonic API.",
				"Docs: cliamp.stream → docs/navidrome.md",
			},
			fields: []fieldSpec{
				{key: "url", label: "Server URL", help: "e.g. https://music.example.com", required: true},
				{key: "user", label: "Username", required: true},
				{key: "password", label: "Password", required: true, secret: true},
			},
			validate: func(v map[string]string) error {
				return navidrome.New(v["url"], v["user"], v["password"]).Ping()
			},
			owned: []string{"url", "user", "password"},
			body: func(v map[string]string) []config.KeyValue {
				return []config.KeyValue{
					quotedKV("url", v["url"]),
					quotedKV("user", v["user"]),
					quotedKV("password", v["password"]),
				}
			},
		},
		{
			key:     "lyrion",
			name:    "Lyrion Music Server",
			section: "lyrion",
			intro: []string{
				"Self-hosted server for Squeezebox players, formerly Logitech Media Server.",
				"Username and password are only needed if the server has password protection.",
				"Docs: cliamp.stream → docs/lyrion.md",
			},
			fields: []fieldSpec{
				{key: "url", label: "Server URL", help: "e.g. http://nas.local:9000", required: true},
				{key: "user", label: "Username", help: "leave blank if the server is unprotected"},
				{key: "password", label: "Password", secret: true},
			},
			validate: func(v map[string]string) error {
				return lyrion.New(v["url"], v["user"], v["password"]).Ping()
			},
			owned: []string{"url", "user", "password"},
			body: func(v map[string]string) []config.KeyValue {
				return []config.KeyValue{
					quotedKV("url", v["url"]),
					quotedKV("user", v["user"]),
					quotedKV("password", v["password"]),
				}
			},
		},
		{
			key:     "bandcamp",
			name:    "Bandcamp",
			section: "bandcamp",
			intro: []string{
				"Stream your purchased Bandcamp collection (official Subsonic API, open beta).",
				"Generate dedicated credentials on bandcamp.com: Fan Settings → Subsonic.",
				"These are NOT your Bandcamp login. Docs: cliamp.stream → docs/bandcamp.md",
			},
			fields: []fieldSpec{
				{key: "user", label: "Subsonic username", required: true},
				{key: "password", label: "Subsonic password", required: true, secret: true},
			},
			validate: func(v map[string]string) error {
				// Bandcamp's ping answers ok even with bad credentials, so
				// validation uses an authenticated call. Fan Settings copies
				// credentials with a stray space, so trim before probing.
				bc := config.BandcampConfig{User: strings.TrimSpace(v["user"]), Password: strings.TrimSpace(v["password"])}
				// Validate against the same url override the saved block
				// keeps, not the default endpoint.
				if cfg, err := config.Load(); err == nil {
					bc.URL = cfg.Bandcamp.URL
				}
				return bandcamp.Validate(bc)
			},
			// The url override and the app-saved browse_sort are not owned,
			// so a save keeps them as written.
			owned: []string{"user", "password"},
			body: func(v map[string]string) []config.KeyValue {
				return []config.KeyValue{
					quotedKV("user", strings.TrimSpace(v["user"])),
					quotedKV("password", strings.TrimSpace(v["password"])),
				}
			},
		},
		{
			key:     "plex",
			name:    "Plex Media Server",
			section: "plex",
			intro: []string{
				"Stream from your Plex server using an X-Plex-Token.",
				"Find a token: https://support.plex.tv/articles/204059436",
			},
			fields: []fieldSpec{
				{key: "url", label: "Server URL", help: "e.g. http://192.168.1.10:32400", required: true},
				{key: "token", label: "X-Plex-Token", required: true, secret: true},
				{key: "libraries", label: "Music libraries (optional)", help: "comma-separated names to include; blank loads every music library"},
			},
			validate: func(v map[string]string) error {
				return plex.NewClient(v["url"], v["token"]).Ping()
			},
			owned: []string{"url", "token", "libraries"},
			body: func(v map[string]string) []config.KeyValue {
				kv := []config.KeyValue{
					quotedKV("url", v["url"]),
					quotedKV("token", v["token"]),
				}
				if libraries := setupStringList(v["libraries"]); libraries != "" {
					kv = append(kv, rawKV("libraries", libraries))
				}
				return kv
			},
		},
		{
			key:     "jellyfin",
			name:    "Jellyfin",
			section: "jellyfin",
			intro: []string{
				"Authenticate with an API token (Dashboard → API Keys)",
				"or with your username and password.",
			},
			picker: &pickerSpec{
				key:   keyJellyfinAuth,
				label: "Authentication",
				options: []pickerOption{
					{value: "token", label: "API token"},
					{value: "password", label: "Username + password"},
				},
			},
			fields: []fieldSpec{
				{key: "url", label: "Server URL", help: "e.g. https://jellyfin.example.com", required: true},
				{key: "token", label: "API token", required: true, secret: true,
					onlyIf: func(v map[string]string) bool { return v[keyJellyfinAuth] == "token" }},
				{key: "user", label: "Username", required: true,
					onlyIf: func(v map[string]string) bool { return v[keyJellyfinAuth] == "password" }},
				{key: "password", label: "Password", required: true, secret: true,
					onlyIf: func(v map[string]string) bool { return v[keyJellyfinAuth] == "password" }},
			},
			validate: func(v map[string]string) error {
				return jellyfin.NewClient(v["url"], v["token"], "", v["user"], v["password"]).Ping()
			},
			// user_id belongs to the account of the old credentials.
			owned: []string{"url", "token", "user", "password", "user_id"},
			body: func(v map[string]string) []config.KeyValue {
				kv := []config.KeyValue{quotedKV("url", v["url"])}
				if v[keyJellyfinAuth] == "token" {
					kv = append(kv, quotedKV("token", v["token"]))
				} else {
					kv = append(kv,
						quotedKV("user", v["user"]),
						quotedKV("password", v["password"]),
					)
				}
				return kv
			},
		},
		{
			key:     "emby",
			name:    "Emby",
			section: "emby",
			intro: []string{
				"Authenticate with an API key (Dashboard → API Keys)",
				"or with your username and password.",
			},
			picker: &pickerSpec{
				key:   keyEmbyAuth,
				label: "Authentication",
				options: []pickerOption{
					{value: "token", label: "API key"},
					{value: "password", label: "Username + password"},
				},
			},
			fields: []fieldSpec{
				{key: "url", label: "Server URL", help: "e.g. https://emby.example.com", required: true},
				{key: "token", label: "API key", required: true, secret: true,
					onlyIf: func(v map[string]string) bool { return v[keyEmbyAuth] == "token" }},
				{key: "user", label: "Username (optional)", help: "multi-user servers: picks your account from /Users",
					onlyIf: func(v map[string]string) bool { return v[keyEmbyAuth] == "token" }},
				{key: "user", label: "Username", required: true,
					onlyIf: func(v map[string]string) bool { return v[keyEmbyAuth] == "password" }},
				{key: "password", label: "Password", required: true, secret: true,
					onlyIf: func(v map[string]string) bool { return v[keyEmbyAuth] == "password" }},
			},
			validate: func(v map[string]string) error {
				if err := emby.NewClient(v["url"], v["token"], "", v["user"], v["password"]).Ping(); err != nil {
					return fmt.Errorf("emby: validation: %w", err)
				}
				return nil
			},
			// user_id belongs to the account of the old credentials.
			owned: []string{"url", "token", "user", "password", "user_id"},
			body: func(v map[string]string) []config.KeyValue {
				kv := []config.KeyValue{quotedKV("url", v["url"])}
				if v[keyEmbyAuth] == "token" {
					kv = append(kv, quotedKV("token", v["token"]))
					if v["user"] != "" {
						kv = append(kv, quotedKV("user", v["user"]))
					}
				} else {
					kv = append(kv,
						quotedKV("user", v["user"]),
						quotedKV("password", v["password"]),
					)
				}
				return kv
			},
		},
		{
			key:     "audiobookshelf",
			name:    "Audiobookshelf",
			section: "audiobookshelf",
			intro: []string{
				"Audiobooks and podcasts from your own Audiobookshelf server.",
				"Authenticate with an API key (Settings → Users → API Keys)",
				"or with your username and password.",
			},
			picker: &pickerSpec{
				key:   keyABSAuth,
				label: "Authentication",
				options: []pickerOption{
					{value: "token", label: "API key"},
					{value: "password", label: "Username + password"},
				},
			},
			fields: []fieldSpec{
				{key: "url", label: "Server URL", help: "e.g. https://abs.example.com", required: true},
				{key: "token", label: "API key", required: true, secret: true,
					onlyIf: func(v map[string]string) bool { return v[keyABSAuth] == "token" }},
				{key: "user", label: "Username", required: true,
					onlyIf: func(v map[string]string) bool { return v[keyABSAuth] == "password" }},
				{key: "password", label: "Password", required: true, secret: true,
					onlyIf: func(v map[string]string) bool { return v[keyABSAuth] == "password" }},
			},
			validate: func(v map[string]string) error {
				if err := audiobookshelf.NewClient(v["url"], v["token"], v["user"], v["password"], nil).Ping(); err != nil {
					return fmt.Errorf("audiobookshelf: validation: %w", err)
				}
				return nil
			},
			owned: []string{"url", "token", "user", "password"},
			body: func(v map[string]string) []config.KeyValue {
				kv := []config.KeyValue{quotedKV("url", v["url"])}
				if v[keyABSAuth] == "token" {
					kv = append(kv, quotedKV("token", v["token"]))
				} else {
					kv = append(kv,
						quotedKV("user", v["user"]),
						quotedKV("password", v["password"]),
					)
				}
				return kv
			},
		},
		{
			key:     "spotify",
			name:    "Spotify (Premium)",
			section: "spotify",
			intro: []string{
				"Requires a Spotify Premium account.",
				"",
				"Recommended: register your own Spotify Developer app at",
				"developer.spotify.com/dashboard (redirect URI",
				"http://127.0.0.1:19872/login). Your own client_id gives you a",
				"private Web API rate-limit quota for library, playlists, and search.",
				"Playback is authorized separately, so first use completes two",
				"steps in one browser tab. Development Mode caps /v1/search at",
				"10 results per request; cliamp pages automatically with offset.",
				"",
				"Alternative: cliamp ships a built-in client_id (the librespot",
				"keymaster) if you do not want to register an app. It's shared with",
				"every librespot- and spotify-player-based client, so you may see",
				"occasional 429 errors when the pool is busy.",
			},
			picker: &pickerSpec{
				key:   keySpotifyMode,
				label: "Client ID",
				options: []pickerOption{
					{value: "custom", label: "Use my own Spotify Developer app client_id"},
					{value: "default", label: "Use built-in shared client_id"},
				},
			},
			fields: []fieldSpec{
				{key: "client_id", label: "Client ID", required: true,
					help:   "from developer.spotify.com/dashboard; redirect URI http://127.0.0.1:19872/login",
					onlyIf: func(v map[string]string) bool { return v[keySpotifyMode] == "custom" }},
				{key: "bitrate", label: "Bitrate", help: "96, 160, or 320 kbps", defaultV: "320"},
			},
			extraValidate: func(v map[string]string) error {
				if v["bitrate"] == "" {
					return nil
				}
				if _, err := strconv.Atoi(v["bitrate"]); err != nil {
					return fmt.Errorf("bitrate must be a number")
				}
				return nil
			},
			// enabled is owned, so setup removes an old enabled = false.
			owned: []string{"enabled", "client_id", "bitrate"},
			body: func(v map[string]string) []config.KeyValue {
				br := v["bitrate"]
				if br == "" {
					br = "320"
				}
				var kv []config.KeyValue
				if v[keySpotifyMode] == "custom" && v["client_id"] != "" {
					kv = append(kv, quotedKV("client_id", v["client_id"]))
				}
				return append(kv, rawKV("bitrate", br))
			},
		},
		{
			key:     "qobuz",
			name:    "Qobuz",
			section: "qobuz",
			intro: []string{
				"Lossless streaming. Requires an active Qobuz subscription.",
				"",
				"No API credentials needed - cliamp obtains them automatically.",
				"After setup, launch cliamp, select Qobuz, and press Enter to",
				"sign in via OAuth in your browser. Hi-Res tiers require a plan",
				"that includes them.",
			},
			picker: &pickerSpec{
				key:   keyQobuzQuality,
				label: "Stream quality",
				options: []pickerOption{
					{value: "6", label: "FLAC 16-bit/44.1kHz (CD) - recommended"},
					{value: "7", label: "FLAC 24-bit up to 96kHz (Hi-Res)"},
					{value: "27", label: "FLAC 24-bit up to 192kHz (Hi-Res)"},
					{value: "5", label: "MP3 320kbps"},
				},
			},
			owned: []string{"enabled", "quality"},
			body: func(v map[string]string) []config.KeyValue {
				q := v[keyQobuzQuality]
				if q == "" {
					q = "6"
				}
				return []config.KeyValue{rawKV("enabled", "true"), rawKV("quality", q)}
			},
		},
		{
			key:     "tidal",
			name:    "Tidal",
			section: "tidal",
			intro: []string{
				"Lossless streaming. Requires a paid Tidal subscription",
				"(every paid plan includes lossless FLAC).",
				"",
				"No API credentials needed - cliamp uses built-in ones.",
				"After setup, launch cliamp, select Tidal, and press Enter to",
				"sign in: approve the link.tidal.com device code in a browser.",
			},
			picker: &pickerSpec{
				key:   keyTidalQuality,
				label: "Stream quality",
				options: []pickerOption{
					{value: "lossless", label: "FLAC (hi-res masters; AAC 320 otherwise) - recommended"},
					{value: "hires", label: "FLAC hi-res (same delivery as lossless)"},
					{value: "high", label: "AAC 320kbps"},
					{value: "low", label: "AAC 96kbps"},
				},
			},
			owned: []string{"enabled", "quality"},
			body: func(v map[string]string) []config.KeyValue {
				q := v[keyTidalQuality]
				if q == "" {
					q = "lossless"
				}
				return []config.KeyValue{rawKV("enabled", "true"), quotedKV("quality", q)}
			},
		},
		{
			key:     "netease",
			name:    "NetEase Cloud Music",
			section: "netease",
			intro: []string{
				"Reuses your browser session through yt-dlp cookies.",
				"Sign in at music.163.com first, then pick that browser here.",
			},
			picker: &pickerSpec{
				key:   keyNetEaseBrowser,
				label: "Browser session",
				options: []pickerOption{
					{value: "chrome", label: "Chrome"},
					{value: "safari", label: "Safari"},
					{value: "firefox", label: "Firefox"},
					{value: "brave", label: "Brave"},
					{value: "edge", label: "Edge"},
					{value: "chromium", label: "Chromium"},
					{value: "vivaldi", label: "Vivaldi"},
					{value: "custom", label: "Custom browser/profile"},
				},
			},
			fields: []fieldSpec{
				{key: "cookies_from", label: "Custom browser/profile", help: "e.g. chrome:Profile 1, firefox:default-release", required: true,
					onlyIf: func(v map[string]string) bool { return v[keyNetEaseBrowser] == "custom" }},
			},
			validate: func(v map[string]string) error {
				browser := netEaseCookiesFrom(v)
				if browser == "" {
					return fmt.Errorf("browser is required")
				}
				v["cookies_from"] = browser
				ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
				defer cancel()
				acc, err := netease.CheckLogin(ctx, browser)
				if err != nil {
					return fmt.Errorf("netease: validation: %w", err)
				}
				v["user_id"] = acc.UserID
				return nil
			},
			owned: []string{"enabled", "cookies_from", "user_id"},
			body: func(v map[string]string) []config.KeyValue {
				kv := []config.KeyValue{
					rawKV("enabled", "true"),
					quotedKV("cookies_from", netEaseCookiesFrom(v)),
				}
				if v["user_id"] != "" {
					kv = append(kv, quotedKV("user_id", v["user_id"]))
				}
				return kv
			},
		},
		{
			key:     "mixcloud",
			name:    "Mixcloud",
			section: "mixcloud",
			intro: []string{
				"Browse and play Mixcloud shows through its public API and yt-dlp.",
				"A username adds your public library, following stream, and creators.",
				"An optional developer access token adds /me and Listen Later.",
				"Choose browser cookies for subscriber-only or signed-in playback.",
				"Docs: cliamp.stream → docs/mixcloud.md",
			},
			picker: &pickerSpec{
				key:   keyMixcloudBrowser,
				label: "Playback session",
				options: []pickerOption{
					{value: "none", label: "Public shows only (no browser cookies)"},
					{value: "chrome", label: "Chrome"},
					{value: "safari", label: "Safari"},
					{value: "firefox", label: "Firefox"},
					{value: "brave", label: "Brave"},
					{value: "edge", label: "Edge"},
					{value: "chromium", label: "Chromium"},
					{value: "opera", label: "Opera"},
					{value: "vivaldi", label: "Vivaldi"},
					{value: "whale", label: "Whale"},
					{value: "custom", label: "Custom browser/profile"},
				},
			},
			fields: []fieldSpec{
				{key: "username", label: "Mixcloud username (optional)", help: "adds account views and followed creators"},
				{key: "access_token", label: "Developer API access token (optional)", help: "enables /me and Listen Later", secret: true},
				{key: "cookies_from", label: "Custom browser/profile", help: "e.g. chrome:Profile 1, firefox:default-release", required: true,
					onlyIf: func(v map[string]string) bool { return v[keyMixcloudBrowser] == "custom" }},
				{key: "styles", label: "Music styles (optional)", help: "comma-separated; blank uses cliamp defaults"},
				{key: "max_items", label: "Items per view", help: fmt.Sprintf("%d-%d", mixcloud.MinItems, mixcloud.MaxItemsLimit), defaultV: strconv.Itoa(mixcloud.DefaultMaxItems)},
				{key: "stream_creators", label: "Creators in Stream", help: fmt.Sprintf("%d-%d", mixcloud.MinItems, mixcloud.MaxStreamCreators), defaultV: strconv.Itoa(mixcloud.DefaultStreamCreators)},
			},
			extraValidate: func(v map[string]string) error {
				limits := []struct {
					key, label string
					max        int
				}{
					{key: "max_items", label: "items per view", max: mixcloud.MaxItemsLimit},
					{key: "stream_creators", label: "stream creators", max: mixcloud.MaxStreamCreators},
				}
				for _, field := range limits {
					n, err := strconv.Atoi(strings.TrimSpace(v[field.key]))
					if err != nil {
						return fmt.Errorf("%s must be a number", field.label)
					}
					if n < mixcloud.MinItems || n > field.max {
						return fmt.Errorf("%s is outside its supported range", field.label)
					}
				}
				return nil
			},
			owned: []string{"enabled", "username", "access_token", "cookies_from", "styles", "max_items", "stream_creators"},
			body: func(v map[string]string) []config.KeyValue {
				kv := []config.KeyValue{rawKV("enabled", "true")}
				if username := strings.TrimSpace(v["username"]); username != "" {
					kv = append(kv, quotedKV("username", username))
				}
				if token := strings.TrimSpace(v["access_token"]); token != "" {
					kv = append(kv, quotedKV("access_token", token))
				}
				if browser := mixcloudCookiesFrom(v); browser != "" {
					kv = append(kv, quotedKV("cookies_from", browser))
				}
				if styles := setupStringList(v["styles"]); styles != "" {
					kv = append(kv, rawKV("styles", styles))
				}
				maxItems := strings.TrimSpace(v["max_items"])
				if maxItems == "" {
					maxItems = strconv.Itoa(mixcloud.DefaultMaxItems)
				}
				streamCreators := strings.TrimSpace(v["stream_creators"])
				if streamCreators == "" {
					streamCreators = strconv.Itoa(mixcloud.DefaultStreamCreators)
				}
				return append(kv, rawKV("max_items", maxItems), rawKV("stream_creators", streamCreators))
			},
		},
		{
			key:     "ytmusic",
			name:    "YouTube Music",
			section: "ytmusic",
			intro: []string{
				"Browse playlists and liked music with browser cookies (zero OAuth setup),",
				"or provide your own Google Cloud OAuth client credentials.",
			},
			picker: &pickerSpec{
				key:   keyYTMusicMode,
				label: "Mode",
				options: []pickerOption{
					{value: "cookies", label: "Browser cookies (recommended — zero OAuth setup)"},
					{value: "custom", label: "Provide my own OAuth credentials"},
					{value: "off", label: "Disable YouTube Music"},
				},
			},
			fields: []fieldSpec{
				{key: "cookies_from", label: "Browser for cookies (optional with OAuth)", help: "e.g. chrome, firefox, chromium, brave+gnomekeyring; blank uses Chrome\nGNOME Keyring needs Python secretstorage",
					onlyIf: func(v map[string]string) bool { return v[keyYTMusicMode] != "off" }},
				{key: "client_id", label: "OAuth Client ID", required: true,
					onlyIf: func(v map[string]string) bool { return v[keyYTMusicMode] == "custom" }},
				{key: "client_secret", label: "OAuth Client Secret", secret: true, required: true,
					onlyIf: func(v map[string]string) bool { return v[keyYTMusicMode] == "custom" }},
			},
			owned: []string{"enabled", "client_id", "client_secret", "cookies_from"},
			body: func(v map[string]string) []config.KeyValue {
				switch v[keyYTMusicMode] {
				case "off":
					return []config.KeyValue{rawKV("enabled", "false")}
				case "custom":
					kv := []config.KeyValue{rawKV("enabled", "true")}
					if v["client_id"] != "" {
						kv = append(kv, quotedKV("client_id", v["client_id"]))
					}
					if v["client_secret"] != "" {
						kv = append(kv, quotedKV("client_secret", v["client_secret"]))
					}
					if v["cookies_from"] != "" {
						kv = append(kv, quotedKV("cookies_from", v["cookies_from"]))
					}
					return kv
				default:
					browser := strings.TrimSpace(v["cookies_from"])
					if browser == "" {
						browser = "chrome"
					}
					return []config.KeyValue{rawKV("enabled", "true"), quotedKV("cookies_from", browser)}
				}
			},
		},
		{
			key:     "telegram",
			name:    "Telegram",
			section: "telegram",
			intro: []string{
				"Play the audio of your own Telegram chats over MTProto.",
				"Create an application at https://my.telegram.org/apps and copy its api_id and api_hash.",
				"Press s after saving to sign in here; otherwise the first start of cliamp asks for the code.",
				"Leave Chats blank to show every chat, group and bot.",
			},
			fields: []fieldSpec{
				{key: "api_id", label: "API ID", help: "the numeric id from my.telegram.org/apps", required: true},
				{key: "api_hash", label: "API hash", help: "32 hexadecimal characters", required: true, secret: true},
				{key: "phone", label: "Phone", help: "international format, e.g. +5491100000000", required: true},
				{key: "password", label: "2FA password", help: "only when the account has two-step verification", secret: true},
				{key: "chats", label: "Chats", help: "optional: blank shows every chat, group and bot"},
				{key: keyPickChats, label: "Pick chats from the account", help: "enter opens the dialogs with checkboxes"},
				{key: "buffer_mb", label: "Buffer (MB)", help: "megabytes buffered before playback starts", defaultV: "1"},
			},
			extraValidate: func(v map[string]string) error {
				if id, err := strconv.Atoi(strings.TrimSpace(v["api_id"])); err != nil || id <= 0 {
					return errors.New("API ID must be the positive number from my.telegram.org/apps")
				}
				hash := strings.TrimSpace(v["api_hash"])
				if len(hash) != 32 {
					return errors.New("API hash must be 32 hexadecimal characters")
				}
				if _, err := hex.DecodeString(hash); err != nil {
					return errors.New("API hash must be 32 hexadecimal characters")
				}
				if phone := digitsOnly(v["phone"]); len(phone) < 7 || len(phone) > 15 {
					return errors.New("Phone must be in international format, e.g. +5491100000000")
				}
				for _, chat := range setupChats(v["chats"]) {
					if chatKey(chat) != "" {
						continue
					}
					if !validUsername(chat) {
						return fmt.Errorf("%q is not a Telegram username. Use @name or a t.me link, pick the chats with ctrl+p, or leave the list blank to show every chat, group and bot", chat)
					}
				}
				if b := strings.TrimSpace(v["buffer_mb"]); b != "" {
					if mb, err := strconv.Atoi(b); err != nil || mb < 1 || mb > 64 {
						return errors.New("Buffer must be a whole number of megabytes between 1 and 64")
					}
				}
				return nil
			},
			owned: []string{"api_id", "api_hash", "phone", "password", "chat", "chats", "buffer_mb"},
			body: func(v map[string]string) []config.KeyValue {
				kv := []config.KeyValue{
					rawKV("api_id", strings.TrimSpace(v["api_id"])),
					quotedKV("api_hash", strings.TrimSpace(v["api_hash"])),
					quotedKV("phone", strings.TrimSpace(v["phone"])),
				}
				if p := strings.TrimSpace(v["password"]); p != "" {
					kv = append(kv, quotedKV("password", p))
				}
				if chats := setupChats(v["chats"]); len(chats) > 0 {
					kv = append(kv, rawKV("chats", setupStringList(strings.Join(chats, ","))))
				}
				buffer := strings.TrimSpace(v["buffer_mb"])
				if buffer == "" {
					buffer = "1"
				}
				return append(kv, rawKV("buffer_mb", buffer))
			},
			signIn: func(v map[string]string) (string, func(context.Context) error) {
				cfg := telegramConfigFrom(v)
				return "sign in now", func(ctx context.Context) error {
					return telegram.SignIn(ctx, cfg)
				}
			},
			prefill: func() map[string]string {
				cfg, err := config.Load()
				if err != nil || !cfg.Telegram.IsSet() {
					return nil
				}
				mb := cfg.Telegram.BufferMB
				if mb <= 0 {
					mb = 1
				}
				return map[string]string{
					"api_id":    strconv.Itoa(cfg.Telegram.AppID),
					"api_hash":  cfg.Telegram.AppHash,
					"phone":     cfg.Telegram.Phone,
					"password":  cfg.Telegram.Password,
					"chats":     strings.Join(cfg.Telegram.Chats, ", "),
					"buffer_mb": strconv.Itoa(mb),
				}
			},
			summary: func(v map[string]string) string {
				switch n := len(setupChats(v["chats"])); n {
				case 0:
					return "every chat"
				case 1:
					return "1 chat"
				default:
					return fmt.Sprintf("%d chats", n)
				}
			},
			pickChats: func(v map[string]string) (chatPick, error) {
				p := telegram.NewFromConfig(telegramConfigFrom(v))
				if p == nil {
					return chatPick{}, errors.New("telegram is not configured yet")
				}
				dialogs, err := p.Dialogs()
				if err != nil {
					return chatPick{}, fmt.Errorf("%w\nSave the section and press s to sign in, or start cliamp once.", err)
				}
				pick := chatPick{options: make([]chatOption, 0, len(dialogs))}
				for _, dialog := range dialogs {
					pick.options = append(pick.options, chatOption{ID: dialog.ID, Name: dialog.Name, Section: dialog.Section})
				}
				if len(setupChats(v["chats"])) == 0 {
					return pick, nil
				}
				// The current list may hold usernames; resolve them so the
				// picker shows them checked.
				selected, err := p.Playlists()
				if err != nil {
					pick.warn = fmt.Errorf("could not resolve the current selection: %w", err)
					return pick, nil
				}
				for _, item := range selected {
					pick.selected = append(pick.selected, item.ID)
				}
				return pick, nil
			},
		},
	}
}

// setupChats splits the comma-separated chats field. It accepts "@name" and
// a t.me link, and drops the empty items a trailing comma leaves.
func setupChats(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		name := strings.TrimSpace(part)
		name = strings.TrimPrefix(name, "https://t.me/")
		name = strings.TrimPrefix(name, "t.me/")
		name = strings.TrimPrefix(name, "@")
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

// validUsername reports whether name looks like a public Telegram username.
func validUsername(name string) bool {
	if len(name) < 3 || len(name) > 32 {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

// digitsOnly keeps the digits of a phone number, ignoring the +, spaces,
// dashes and parentheses that people copy from an address book.
func digitsOnly(phone string) string {
	var b strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// telegramConfigFrom builds the [telegram] section the wizard writes, so
// setup can sign in right after saving it.
func telegramConfigFrom(v map[string]string) config.TelegramConfig {
	id, _ := strconv.Atoi(strings.TrimSpace(v["api_id"]))
	mb, _ := strconv.Atoi(strings.TrimSpace(v["buffer_mb"]))
	return config.TelegramConfig{
		AppID:    id,
		AppHash:  strings.TrimSpace(v["api_hash"]),
		Phone:    strings.TrimSpace(v["phone"]),
		Password: strings.TrimSpace(v["password"]),
		Chats:    setupChats(v["chats"]),
		BufferMB: mb,
	}
}

func netEaseCookiesFrom(v map[string]string) string {
	picked := strings.TrimSpace(v[keyNetEaseBrowser])
	if picked == "" || picked == "custom" {
		return strings.TrimSpace(v["cookies_from"])
	}
	return picked
}

func mixcloudCookiesFrom(v map[string]string) string {
	picked := strings.TrimSpace(v[keyMixcloudBrowser])
	if picked == "" || picked == "none" {
		return ""
	}
	if picked == "custom" {
		return strings.TrimSpace(v["cookies_from"])
	}
	return picked
}

// quotedKV returns a body key with value written as a TOML string.
func quotedKV(key, value string) config.KeyValue {
	return config.KeyValue{Key: key, Value: config.QuoteString(value)}
}

// rawKV returns a body key with value written as is, such as a number, a
// bool or a list.
func rawKV(key, value string) config.KeyValue {
	return config.KeyValue{Key: key, Value: value}
}

func setupStringList(value string) string {
	var quoted []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			quoted = append(quoted, config.QuoteString(part))
		}
	}
	if len(quoted) == 0 {
		return ""
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// ----- Bubbletea model ----------------------------------------------------

type stage int

const (
	stageMenu stage = iota
	stagePicker
	stageForm
	stageChats
	stageConfirm
	stageValidating
	stageResult
)

type setupModel struct {
	provs []providerSpec
	w, h  int

	stage      stage
	menuCursor int

	// active provider (index into provs); -1 means none
	pidx int

	// picker state (within a provider)
	pickerCursor int

	// form state
	values    map[string]string
	prefilled map[string]string // values a second run started from
	visible   []int             // indices into provs[pidx].fields that are currently visible
	fcursor   int

	// chat picker
	chatItems   []chatOption
	chatChecked map[int]bool
	chatCursor  int
	chatErr     error
	chatWarn    error
	chatLoading bool
	chatTouched bool              // the user toggled something in the checklist
	chatNote    string            // one-line feedback about the selection
	chatNames   map[string]string // peer key -> title, for the form summary

	// validating
	spinFrame int

	// result state
	resultErr     error
	resultWarning bool   // validation failed but config still valid
	resultText    string // overall message (e.g. "Saved [navidrome] section.")
	saveFailed    error  // io error writing config (rare; shown as error result)
	awaitingSave  bool   // true after a failed validate: waiting for y/n

	// sign-in offered after a successful save (Telegram)
	signInLabel     string
	signInAction    func(context.Context) error
	signInRequested bool

	// cached so we don't hit os.UserHomeDir per-frame
	cfgPath string
}

func newSetupModel() *setupModel {
	cfgPath, _ := config.Path()
	return &setupModel{
		provs:   providers(),
		stage:   stageMenu,
		pidx:    -1,
		values:  map[string]string{},
		cfgPath: cfgPath,
	}
}

func (m *setupModel) Init() tea.Cmd {
	return func() tea.Msg { return tea.RequestWindowSize() }
}

// ----- Messages -----------------------------------------------------------

// validateDoneMsg is the result of a probe. found holds each key that the
// probe added or changed in its copy of the form values, such as the NetEase
// user_id.
type validateDoneMsg struct {
	err   error
	found map[string]string
}

type spinTickMsg struct{}

func spinTickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return spinTickMsg{} })
}

func runValidateCmd(spec providerSpec, values map[string]string) tea.Cmd {
	return func() tea.Msg {
		if spec.validate == nil {
			return validateDoneMsg{}
		}
		before := maps.Clone(values)
		err := spec.validate(values)
		found := make(map[string]string)
		for k, v := range values {
			if old, ok := before[k]; !ok || old != v {
				found[k] = v
			}
		}
		return validateDoneMsg{err: err, found: found}
	}
}

type chatsLoadedMsg struct {
	options []chatOption
	checked map[int]bool
	err     error
	warn    error
}

func loadChatsCmd(spec providerSpec, values map[string]string) tea.Cmd {
	return func() tea.Msg {
		pick, err := spec.pickChats(values)
		if err != nil {
			return chatsLoadedMsg{err: err}
		}
		return chatsLoadedMsg{options: pick.options, checked: checkedChats(pick.options, pick.selected), warn: pick.warn}
	}
}

// checkedChats marks the options that the configured ids select. It matches
// by kind and number, so a stored access hash that went stale still matches.
func checkedChats(options []chatOption, selected []string) map[int]bool {
	wanted := make(map[string]struct{}, len(selected))
	for _, id := range selected {
		if key := chatKey(id); key != "" {
			wanted[key] = struct{}{}
		}
	}
	checked := make(map[int]bool, len(wanted))
	for i, option := range options {
		if key := chatKey(option.ID); key != "" {
			if _, ok := wanted[key]; ok {
				checked[i] = true
			}
		}
	}
	return checked
}

// chatKey reduces a peer id to kind:number, ignoring a stale access hash.
func chatKey(id string) string {
	parts := strings.Split(strings.TrimSpace(id), ":")
	if len(parts) < 2 {
		return ""
	}
	switch parts[0] {
	case "u", "ch":
		if len(parts) != 3 {
			return ""
		}
	case "c":
		if len(parts) != 2 {
			return ""
		}
	default:
		return ""
	}
	for _, part := range parts[1:] {
		if _, err := strconv.ParseInt(part, 10, 64); err != nil {
			return ""
		}
	}
	return parts[0] + ":" + parts[1]
}

// ----- Update -------------------------------------------------------------

func (m *setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.PasteMsg:
		return m.handlePaste(msg.Content)

	case spinTickMsg:
		if m.stage == stageValidating || (m.stage == stageChats && m.chatLoading) {
			m.spinFrame++
			return m, spinTickCmd()
		}
		return m, nil

	case chatsLoadedMsg:
		if m.stage != stageChats {
			return m, nil
		}
		m.chatLoading = false
		m.chatItems = msg.options
		m.chatChecked = msg.checked
		if m.chatChecked == nil {
			m.chatChecked = map[int]bool{}
		}
		m.chatErr, m.chatWarn = msg.err, msg.warn
		m.chatCursor = 0
		m.chatNames = make(map[string]string, len(msg.options))
		for _, option := range msg.options {
			if key := chatKey(option.ID); key != "" {
				m.chatNames[key] = option.Name
			}
		}
		return m, nil

	case validateDoneMsg:
		return m.onValidateDone(msg.err, msg.found)
	}
	return m, nil
}

// handlePaste appends bracketed-paste content into the active form field.
// Newlines and other control characters are stripped — the wizard's fields
// are all single-line, and pasted credentials often have a trailing newline
// from the source app.
func (m *setupModel) handlePaste(content string) (tea.Model, tea.Cmd) {
	if content == "" || m.stage != stageForm || len(m.visible) == 0 {
		return m, nil
	}
	clean := sanitizePaste(content)
	if clean == "" {
		return m, nil
	}
	field := m.provs[m.pidx].fields[m.visible[m.fcursor]]
	if field.key == keyPickChats {
		// A button, not a field.
		return m, nil
	}
	m.values[field.key] = typed(field, m.values[field.key], clean, m.prefilled)
	return m, nil
}

func sanitizePaste(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		// Drop newlines and other ASCII control chars; keep spaces and tabs (tabs as space).
		switch {
		case r == '\n' || r == '\r':
			continue
		case r == '\t':
			b.WriteRune(' ')
		case r < 0x20:
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (m *setupModel) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}

	switch m.stage {
	case stageMenu:
		return m.menuKey(msg)
	case stagePicker:
		return m.pickerKey(msg)
	case stageForm:
		return m.formKey(msg)
	case stageChats:
		return m.chatsKey(msg)
	case stageConfirm:
		return m.confirmKey(msg)
	case stageValidating:
		// Ignore input while probing.
		return m, nil
	case stageResult:
		return m.resultKey(msg)
	}
	return m, nil
}

func (m *setupModel) menuKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		return m, tea.Quit
	case "up", "k":
		if m.menuCursor > 0 {
			m.menuCursor--
		}
	case "down", "j":
		if m.menuCursor < len(m.provs)-1 {
			m.menuCursor++
		}
	case "enter":
		m.startProvider(m.menuCursor)
	}
	return m, nil
}

func (m *setupModel) startProvider(idx int) {
	m.pidx = idx
	m.values = map[string]string{}
	// A second run starts from the saved section, so changing one field does
	// not blank the rest.
	if spec := m.provs[idx]; spec.prefill != nil {
		maps.Copy(m.values, spec.prefill())
	}
	m.prefilled = maps.Clone(m.values)
	m.fcursor = 0
	m.pickerCursor = 0
	m.resultErr = nil
	m.resultText = ""
	m.resultWarning = false
	m.awaitingSave = false
	m.saveFailed = nil
	m.signInLabel, m.signInAction, m.signInRequested = "", nil, false
	m.chatItems, m.chatChecked, m.chatCursor, m.chatErr, m.chatWarn, m.chatLoading = nil, nil, 0, nil, nil, false
	m.chatTouched, m.chatNote = false, ""
	m.chatNames = nil

	if m.provs[idx].picker != nil {
		m.stage = stagePicker
	} else {
		m.refreshVisibleFields()
		m.stage = stageForm
	}
}

func (m *setupModel) pickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := m.provs[m.pidx].picker
	switch msg.String() {
	case "esc":
		m.stage = stageMenu
		return m, nil
	case "up", "k":
		if m.pickerCursor > 0 {
			m.pickerCursor--
		}
	case "down", "j":
		if m.pickerCursor < len(p.options)-1 {
			m.pickerCursor++
		}
	case "enter":
		m.values[p.key] = p.options[m.pickerCursor].value
		// Pickers can short-circuit with no fields (e.g. ytmusic "default" / "off").
		m.refreshVisibleFields()
		if len(m.visible) == 0 {
			return m.submitForm()
		}
		m.stage = stageForm
	}
	return m, nil
}

func (m *setupModel) formKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if len(m.visible) == 0 {
		// Defensive: shouldn't happen; submit anyway.
		return m.submitForm()
	}
	field := m.provs[m.pidx].fields[m.visible[m.fcursor]]
	cur := m.values[field.key]

	// The chats field can open a checklist of the account dialogs, so the
	// usual typo-prone usernames are optional. Ctrl+P is an alias; the visible
	// row is the reliable path, since a multiplexer can swallow the shortcut.
	if field.key == "chats" && m.provs[m.pidx].pickChats != nil && msg.String() == "ctrl+p" {
		return m.openChats()
	}
	if field.key == keyPickChats {
		switch {
		case msg.Code == tea.KeyEnter:
			return m.openChats()
		case msg.Code == tea.KeySpace, msg.Code == tea.KeyBackspace, len(msg.Text) > 0:
			// It is a button, not a field: nothing is typed into it.
			return m, nil
		}
	}

	switch msg.Code {
	case tea.KeyEscape:
		// Leaving with edits would drop them silently: ask first.
		if m.formDirty() {
			m.stage = stageConfirm
			return m, nil
		}
		m.stage = stageMenu
		return m, nil
	case tea.KeyUp:
		if m.fcursor > 0 {
			m.fcursor--
		}
		return m, nil
	case tea.KeyTab, tea.KeyDown:
		// Shift+Tab arrives with ModShift; treat it as "previous field".
		if msg.Mod&tea.ModShift != 0 {
			if m.fcursor > 0 {
				m.fcursor--
			}
			return m, nil
		}
		if m.fcursor < len(m.visible)-1 {
			m.fcursor++
		}
		return m, nil
	case tea.KeyEnter:
		// Enter on the last field submits; otherwise advance.
		if m.fcursor < len(m.visible)-1 {
			m.fcursor++
			return m, nil
		}
		return m.submitForm()
	case tea.KeyBackspace:
		if cur != "" {
			m.values[field.key] = removeLastRune(cur)
		}
		return m, nil
	case tea.KeySpace:
		m.values[field.key] = typed(field, cur, " ", m.prefilled)
		return m, nil
	}

	// Treat ctrl+s / ctrl+d as submit shortcuts.
	if s := msg.String(); s == "ctrl+s" || s == "ctrl+d" {
		return m.submitForm()
	}

	if len(msg.Text) > 0 {
		m.values[field.key] = typed(field, cur, msg.Text, m.prefilled)
	}
	return m, nil
}

// typed appends text to cur, except in a secret field that still holds the
// value it was prefilled with: the first keystroke replaces the secret
// instead of appending to it.
func typed(field fieldSpec, cur, text string, prefilled map[string]string) string {
	if field.secret {
		if before, ok := prefilled[field.key]; ok && cur == before {
			return text
		}
	}
	return cur + text
}

const chatPageSize = 12

// chatsKey drives the chat checklist: space toggles, a and n mark everything
// or nothing, and enter writes the picked ids into the chats field.
func (m *setupModel) chatsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.chatLoading {
		if msg.String() == "esc" {
			m.chatLoading = false
			m.stage = stageForm
		}
		return m, nil
	}
	switch msg.String() {
	case "esc", "q":
		m.stage = stageForm
	case "up", "k":
		if m.chatCursor > 0 {
			m.chatCursor--
		}
	case "down", "j":
		if m.chatCursor < len(m.chatItems)-1 {
			m.chatCursor++
		}
	case "pgup":
		m.chatCursor = max(0, m.chatCursor-chatPageSize)
	case "pgdown":
		if n := len(m.chatItems); n > 0 {
			m.chatCursor = min(n-1, m.chatCursor+chatPageSize)
		}
	case "home":
		m.chatCursor = 0
	case "end":
		if n := len(m.chatItems); n > 0 {
			m.chatCursor = n - 1
		}
	case " ", "space":
		if len(m.chatItems) > 0 {
			m.chatChecked[m.chatCursor] = !m.chatChecked[m.chatCursor]
			m.chatTouched = true
			m.chatNote = ""
		}
	case "a":
		for i := range m.chatItems {
			m.chatChecked[i] = true
		}
		m.chatTouched = true
		m.chatNote = ""
	case "n":
		m.chatChecked = map[int]bool{}
		m.chatTouched = true
		m.chatNote = ""
	case "enter":
		m.applyChats()
	}
	return m, nil
}

// applyChats writes the checked ids into the chats field. Picking every chat
// means no filter, so the field stays empty and chats created later appear
// too.
func (m *setupModel) applyChats() {
	if len(m.chatItems) == 0 {
		m.stage = stageForm
		return
	}
	if !m.chatTouched {
		// Enter on a row only moves the cursor. Staying here makes the
		// missing selection impossible to miss; clearing the filter needs
		// an explicit n.
		m.chatNote = "Nothing selected yet: space marks a chat, a marks all, n removes the filter."
		return
	}
	m.chatNote = ""
	selected := make([]string, 0, len(m.chatChecked))
	for i, option := range m.chatItems {
		if m.chatChecked[i] {
			selected = append(selected, option.ID)
		}
	}
	if len(selected) == len(m.chatItems) {
		m.values["chats"] = ""
	} else {
		m.values["chats"] = strings.Join(selected, ", ")
	}
	// Picking is the action the user came for: write the section right here
	// instead of waiting for a submit that may never come.
	if err := m.saveSection(); err != nil {
		m.chatNote = "Not saved: " + err.Error()
	} else {
		m.chatNote = "Selection saved."
		m.prefilled = maps.Clone(m.values)
	}
	// Leave the button so the next enter submits instead of reopening the
	// list.
	spec := m.provs[m.pidx]
	if m.fcursor < len(m.visible)-1 && spec.fields[m.visible[m.fcursor]].key == keyPickChats {
		m.fcursor++
	}
	m.stage = stageForm
}

// openChats starts the chat checklist.
func (m *setupModel) openChats() (tea.Model, tea.Cmd) {
	spec := m.provs[m.pidx]
	if spec.pickChats == nil {
		return m, nil
	}
	m.stage = stageChats
	m.chatLoading = true
	m.chatErr, m.chatWarn = nil, nil
	m.chatCursor = 0
	m.chatTouched = false
	return m, tea.Batch(spinTickCmd(), loadChatsCmd(spec, maps.Clone(m.values)))
}

// chatsSummary says how many chats the field will keep, so an empty selection
// is visible before saving.
func (m *setupModel) chatsSummary() string {
	note := m.chatNote
	summary := m.chatsSelection()
	if note == "" {
		return summary
	}
	if summary == "" {
		return note
	}
	return note + " " + summary
}

// chatsSelection describes the chats the field holds.
func (m *setupModel) chatsSelection() string {
	chats := setupChats(m.values["chats"])
	if len(chats) == 0 {
		return "No filter: every chat, group and bot."
	}
	if len(chats) == 1 {
		if name := m.chatNames[chatKey(chats[0])]; name != "" {
			return "1 chat selected: " + name + "."
		}
		return "1 chat selected."
	}
	names := make([]string, 0, len(chats))
	for _, chat := range chats {
		name := m.chatNames[chatKey(chat)]
		if name == "" {
			return fmt.Sprintf("%d chats selected.", len(chats))
		}
		names = append(names, name)
	}
	if len(names) <= 3 {
		return fmt.Sprintf("%d chats selected: %s.", len(names), strings.Join(names, ", "))
	}
	return fmt.Sprintf("%d chats selected.", len(names))
}

// saveSection writes the provider section from the current form values. It
// runs the cheap field checks first, so a half-filled first-time form does not
// land in the config.
func (m *setupModel) saveSection() error {
	spec := m.provs[m.pidx]
	if spec.extraValidate != nil {
		if err := spec.extraValidate(m.values); err != nil {
			return err
		}
	}
	return config.SaveSection(spec.section, spec.body(m.values), spec.owned)
}

// formDirty reports whether the form holds changes against the values it
// started from.
func (m *setupModel) formDirty() bool {
	return !maps.Equal(m.values, m.prefilled)
}

// confirmKey handles the save-on-exit question.
func (m *setupModel) confirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		return m.submitForm()
	case "n", "N":
		m.stage = stageMenu
		return m, nil
	case "esc":
		m.stage = stageForm
	}
	return m, nil
}

func (m *setupModel) submitForm() (tea.Model, tea.Cmd) {
	spec := m.provs[m.pidx]

	// Apply defaults for blank fields.
	for _, f := range spec.fields {
		if m.values[f.key] == "" && f.defaultV != "" {
			m.values[f.key] = f.defaultV
		}
	}

	// Field check (only for visible fields). config.Load reads a $NAME
	// value from the environment, so a reference to an unset or empty
	// variable loads as an empty value.
	for i, idx := range m.visible {
		f := spec.fields[idx]
		v := m.values[f.key]
		var err error
		if f.required && strings.TrimSpace(v) == "" {
			err = fmt.Errorf("%s is required", f.label)
		} else if name, ok := config.EnvRef(v); ok && os.Getenv(name) == "" {
			err = fmt.Errorf("%s names an unset or empty environment variable. cliamp reads a $NAME or ${NAME} value from the environment", f.label)
		}
		if err != nil {
			m.fcursor = i
			m.resultErr = err
			m.resultText = ""
			m.stage = stageResult
			return m, nil
		}
	}

	if spec.extraValidate != nil {
		if err := spec.extraValidate(m.values); err != nil {
			m.resultErr = err
			m.stage = stageResult
			return m, nil
		}
	}

	// Light URL sanity check before any network call. A $NAME value stays
	// as typed, and the check reads the variable.
	if u, ok := m.values["url"]; ok && u != "" {
		name, isRef := config.EnvRef(u)
		if isRef {
			u = os.Getenv(name)
		}
		clean := strings.TrimRight(u, "/")
		if !looksLikeHTTPURL(clean) {
			m.resultErr = fmt.Errorf("URL must start with http:// or https://")
			m.stage = stageResult
			return m, nil
		}
		if !isRef {
			m.values["url"] = clean
		}
	}

	if spec.validate == nil {
		// No probe; save immediately.
		return m, m.persistAndDone(false)
	}

	m.stage = stageValidating
	m.spinFrame = 0
	return m, tea.Batch(spinTickCmd(), runValidateCmd(spec, envResolved(m.values)))
}

// envResolved returns a copy of values in which each $NAME or ${NAME} value
// holds the value of that environment variable. The probe then checks the
// value that config.Load reads, and the save keeps the reference.
func envResolved(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for k, v := range values {
		if name, ok := config.EnvRef(v); ok {
			v = os.Getenv(name)
		}
		out[k] = v
	}
	return out
}

// onValidateDone takes the keys that the probe found into the form values,
// so the save writes them. A key that holds a $NAME or ${NAME} value keeps
// that reference.
func (m *setupModel) onValidateDone(err error, found map[string]string) (tea.Model, tea.Cmd) {
	for k, v := range found {
		if _, ok := config.EnvRef(m.values[k]); !ok {
			m.values[k] = v
		}
	}
	if err == nil {
		return m, m.persistAndDone(false)
	}
	m.resultErr = err
	m.awaitingSave = true
	m.stage = stageResult
	return m, nil
}

// persistAndDone writes the section and transitions to result. warn=true
// indicates the user opted to save despite a failed probe.
func (m *setupModel) persistAndDone(warn bool) tea.Cmd {
	spec := m.provs[m.pidx]
	if err := config.SaveSection(spec.section, spec.body(m.values), spec.owned); err != nil {
		m.saveFailed = err
		m.stage = stageResult
		m.awaitingSave = false
		return nil
	}
	m.stage = stageResult
	m.awaitingSave = false
	m.resultErr = nil
	m.resultWarning = warn
	m.resultText = fmt.Sprintf("Saved [%s] section.", spec.section)
	if spec.summary != nil {
		if note := spec.summary(m.values); note != "" {
			m.resultText = fmt.Sprintf("Saved [%s] section (%s).", spec.section, note)
		}
	}
	m.signInLabel, m.signInAction = "", nil
	if spec.signIn != nil && !warn {
		m.signInLabel, m.signInAction = spec.signIn(m.values)
	}
	return nil
}

func (m *setupModel) resultKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.awaitingSave {
		switch strings.ToLower(msg.String()) {
		case "q":
			return m, tea.Quit
		case "y":
			m.persistAndDone(true)
			return m, nil
		case "n", "esc":
			m.awaitingSave = false
			m.resultErr = nil
			m.stage = stageMenu
			return m, nil
		}
		return m, nil
	}

	if m.signInAction != nil && strings.ToLower(msg.String()) == "s" {
		m.signInRequested = true
		return m, tea.Quit
	}

	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "enter", "esc", " ":
		m.stage = stageMenu
		return m, nil
	}
	return m, nil
}

// refreshVisibleFields recomputes the visible-field index list based on
// the current values map, then resets the cursor to a sensible position.
func (m *setupModel) refreshVisibleFields() {
	spec := m.provs[m.pidx]
	m.visible = m.visible[:0]
	for i, f := range spec.fields {
		if f.onlyIf == nil || f.onlyIf(m.values) {
			m.visible = append(m.visible, i)
		}
	}
	if m.fcursor >= len(m.visible) {
		m.fcursor = 0
	}
}

// ----- Rendering ----------------------------------------------------------

var (
	// Color choices match the existing player palette; ANSI numbers give
	// good contrast against any terminal theme.
	titleStyle  = lipgloss.NewStyle().Foreground(lipgloss.ANSIColor(10)).Bold(true)
	hintStyle   = lipgloss.NewStyle().Foreground(lipgloss.ANSIColor(7))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.ANSIColor(8))
	accentStyle = lipgloss.NewStyle().Foreground(lipgloss.ANSIColor(11)).Bold(true)
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.ANSIColor(9)).Bold(true)
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.ANSIColor(10)).Bold(true)
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.ANSIColor(11)).Bold(true)
	cardStyle   = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.ANSIColor(8)).
			Padding(1, 2)
	activeFieldStyle = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder(), false, false, false, true).
				BorderForeground(lipgloss.ANSIColor(11)).
				PaddingLeft(1)
	inactiveFieldStyle = lipgloss.NewStyle().PaddingLeft(2)
)

const (
	maxCardWidth = 78
	logoLine1    = "  cliamp setup"
	logoLine2    = "  configure remote providers"
)

func (m *setupModel) View() tea.View {
	header := titleStyle.Render(logoLine1) + "\n" +
		hintStyle.Render(logoLine2) + "\n"

	var body string
	switch m.stage {
	case stageMenu:
		body = m.viewMenu()
	case stagePicker:
		body = m.viewPicker()
	case stageForm:
		body = m.viewForm()
	case stageChats:
		body = m.viewChats()
	case stageConfirm:
		body = m.viewConfirm()
	case stageValidating:
		body = m.viewValidating()
	case stageResult:
		body = m.viewResult()
	}

	footer := "\n" + m.viewFooter()
	view := tea.NewView(header + "\n" + body + footer)
	view.AltScreen = true
	return view
}

// card wraps body in the standard rounded-border card sized to the terminal.
func (m *setupModel) card(body string) string {
	return cardStyle.Width(min(maxCardWidth, m.viewWidth())).Render(body)
}

func (m *setupModel) viewMenu() string {
	var b strings.Builder
	b.WriteString(accentStyle.Render("Pick a provider to configure"))
	b.WriteString("\n\n")
	for i, p := range m.provs {
		marker := "  "
		nameRender := p.name
		if i == m.menuCursor {
			marker = accentStyle.Render("▸ ")
			nameRender = accentStyle.Render(p.name)
		}
		b.WriteString(marker)
		b.WriteString(nameRender)
		b.WriteString("\n")
		// Render the first intro line as a one-liner per item, faintly.
		if len(p.intro) > 0 {
			indent := "    "
			b.WriteString(indent)
			b.WriteString(hintStyle.Render(p.intro[0]))
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("config: " + m.cfgPath))
	return m.card(b.String())
}

func (m *setupModel) viewPicker() string {
	spec := m.provs[m.pidx]
	p := spec.picker
	var b strings.Builder
	b.WriteString(accentStyle.Render(spec.name))
	b.WriteString("\n\n")
	for _, line := range spec.intro {
		b.WriteString(hintStyle.Render(line))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(accentStyle.Render(p.label))
	b.WriteString("\n\n")
	for i, opt := range p.options {
		marker := "  "
		text := opt.label
		if i == m.pickerCursor {
			marker = accentStyle.Render("▸ ")
			text = accentStyle.Render(opt.label)
		}
		b.WriteString(marker)
		b.WriteString(text)
		b.WriteString("\n")
	}
	return m.card(b.String())
}

func (m *setupModel) viewForm() string {
	spec := m.provs[m.pidx]
	var b strings.Builder
	b.WriteString(accentStyle.Render(spec.name))
	b.WriteString("\n\n")
	for _, line := range spec.intro {
		b.WriteString(hintStyle.Render(line))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	for i, idx := range m.visible {
		f := spec.fields[idx]
		if f.key == keyPickChats {
			line := f.label
			if i == m.fcursor {
				line += "  " + hintStyle.Render("enter")
				if f.help != "" {
					line += "\n" + dimStyle.Render(f.help)
				}
			}
			if summary := m.chatsSummary(); summary != "" {
				line += "\n" + dimStyle.Render(summary)
			}
			if i == m.fcursor {
				b.WriteString(activeFieldStyle.Render(line))
			} else {
				b.WriteString(inactiveFieldStyle.Render(line))
			}
			b.WriteString("\n\n")
			continue
		}
		val := m.values[f.key]
		display := val
		if f.secret {
			display = strings.Repeat("•", utf8.RuneCountInString(val))
		}
		labelLine := f.label
		if f.required {
			labelLine += accentStyle.Render(" *")
		}
		valueLine := display
		if i == m.fcursor {
			// Active: show cursor caret at end of value.
			valueLine = display + accentStyle.Render("▎")
			block := labelLine + "\n" + valueLine
			if f.help != "" {
				block += "\n" + dimStyle.Render(f.help)
			}
			b.WriteString(activeFieldStyle.Render(block))
		} else {
			if valueLine == "" {
				valueLine = dimStyle.Render("(empty)")
			}
			block := labelLine + "\n" + valueLine
			b.WriteString(inactiveFieldStyle.Render(block))
		}
		b.WriteString("\n\n")
	}
	return m.card(strings.TrimRight(b.String(), "\n"))
}

func (m *setupModel) viewConfirm() string {
	spec := m.provs[m.pidx]
	var b strings.Builder
	b.WriteString(accentStyle.Render(spec.name))
	b.WriteString("\n\n")
	b.WriteString("Save the changes to [" + spec.section + "]?\n\n")
	b.WriteString(accentStyle.Render("y save") + "   " + "n discard" + "   " + "esc keep editing")
	return m.card(b.String())
}

func (m *setupModel) viewChats() string {
	var b strings.Builder
	b.WriteString(accentStyle.Render("Chats"))
	b.WriteString("\n\n")
	b.WriteString(hintStyle.Render("Space selects a chat. Enter saves the selection."))
	b.WriteString("\n\n")

	if m.chatLoading {
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		b.WriteString(accentStyle.Render(frames[m.spinFrame%len(frames)]) + " Listing the dialogs of the account…")
		b.WriteString("\n\n")
		b.WriteString(hintStyle.Render("Esc cancels."))
		return m.card(b.String())
	}

	if m.chatErr != nil {
		b.WriteString(errStyle.Render("✗ Could not list the dialogs"))
		b.WriteString("\n\n")
		b.WriteString(m.chatErr.Error())
		b.WriteString("\n\n")
		b.WriteString(hintStyle.Render("Esc goes back to the form, where the list can be typed by hand."))
		return m.card(b.String())
	}

	if m.chatNote != "" {
		b.WriteString(warnStyle.Render("⚠ " + m.chatNote))
		b.WriteString("\n\n")
	}

	if m.chatWarn != nil {
		b.WriteString(warnStyle.Render("⚠ " + m.chatWarn.Error()))
		b.WriteString("\n\n")
	}

	if len(m.chatItems) == 0 {
		b.WriteString("The account has no dialogs.")
		b.WriteString("\n\n")
		b.WriteString(hintStyle.Render("Esc goes back to the form."))
		return m.card(b.String())
	}

	start := 0
	if m.chatCursor >= chatPageSize {
		start = m.chatCursor - chatPageSize + 1
	}
	end := min(start+chatPageSize, len(m.chatItems))
	checked := 0
	for i := range m.chatItems {
		if m.chatChecked[i] {
			checked++
		}
	}
	for i := start; i < end; i++ {
		option := m.chatItems[i]
		box := "[ ]"
		if m.chatChecked[i] {
			box = "[x]"
		}
		line := box + " " + option.Name
		if option.Section != "" {
			line += "  " + dimStyle.Render(option.Section)
		}
		if i == m.chatCursor {
			b.WriteString(accentStyle.Render("▸ ") + line)
		} else {
			b.WriteString("  " + line)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(fmt.Sprintf("%d of %d selected", checked, len(m.chatItems))))
	if start > 0 || end < len(m.chatItems) {
		b.WriteString(dimStyle.Render(fmt.Sprintf("   showing %d-%d", start+1, end)))
	}
	b.WriteString("\n\n")
	b.WriteString(hintStyle.Render("Picking every chat means no filter: chats created later appear too."))
	return m.card(b.String())
}

func (m *setupModel) viewValidating() string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	frame := frames[m.spinFrame%len(frames)]
	body := accentStyle.Render(frame) + " " +
		"Verifying connection to " + m.provs[m.pidx].name + "…"
	return cardStyle.Width(min(maxCardWidth, m.viewWidth())).Render(body)
}

func (m *setupModel) viewResult() string {
	spec := m.provs[m.pidx]
	var b strings.Builder
	b.WriteString(accentStyle.Render(spec.name))
	b.WriteString("\n\n")

	if m.saveFailed != nil {
		b.WriteString(errStyle.Render("✗ Failed to write config"))
		b.WriteString("\n\n")
		b.WriteString(m.saveFailed.Error())
		b.WriteString("\n\n")
		b.WriteString(hintStyle.Render("Press Enter or Esc to return to the menu, or q to quit."))
		return m.card(b.String())
	}

	if m.awaitingSave {
		b.WriteString(errStyle.Render("✗ Validation failed"))
		b.WriteString("\n\n")
		b.WriteString(m.resultErr.Error())
		b.WriteString("\n\n")
		b.WriteString(hintStyle.Render("The config will still load on next launch — useful when the server is offline now."))
		b.WriteString("\n\n")
		b.WriteString(accentStyle.Render("Save anyway?  ") + "[y/N]  q quit")
		return m.card(b.String())
	}

	if m.resultErr != nil {
		b.WriteString(errStyle.Render("✗ "))
		b.WriteString(m.resultErr.Error())
		b.WriteString("\n\n")
		b.WriteString(hintStyle.Render("Press Enter or Esc to return to the menu, or q to quit."))
		return m.card(b.String())
	}

	if m.resultWarning {
		b.WriteString(warnStyle.Render("⚠ Saved without verification"))
	} else {
		b.WriteString(okStyle.Render("✓ " + m.resultText))
	}
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render(m.cfgPath))
	b.WriteString("\n\n")
	if m.signInAction != nil {
		b.WriteString(hintStyle.Render(fmt.Sprintf("Press s to %s, Enter or Esc to configure another provider, or q to quit.", m.signInLabel)))
		return m.card(b.String())
	}
	b.WriteString(hintStyle.Render("Press Enter or Esc to configure another provider, or q to quit."))
	return m.card(b.String())
}

func (m *setupModel) viewFooter() string {
	var keys string
	switch m.stage {
	case stageMenu:
		keys = "↑/↓ pick   enter select   q quit"
	case stagePicker:
		keys = "↑/↓ pick   enter confirm   esc back"
	case stageForm:
		keys = "↑/↓ field   enter submit   esc back"
		if spec := m.provs[m.pidx]; spec.pickChats != nil && len(m.visible) > 0 {
			switch spec.fields[m.visible[m.fcursor]].key {
			case keyPickChats:
				keys = "enter pick chats   ↑/↓ field   esc back"
			case "chats":
				keys = "↑/↓ field   ctrl+p pick chats   enter submit   esc back"
			}
		}
	case stageChats:
		if m.chatLoading {
			keys = "esc cancel"
		} else {
			keys = "↑/↓ move   space toggle   a all   n none   enter apply   esc back"
		}
	case stageConfirm:
		keys = "y save   n discard   esc keep editing"
	case stageValidating:
		keys = "ctrl+c cancel"
	case stageResult:
		switch {
		case m.awaitingSave:
			keys = "y save anyway   n cancel  q quit"
		case m.signInAction != nil:
			keys = "s " + m.signInLabel + "   enter/esc continue   q quit"
		default:
			keys = "enter/esc continue   q quit"
		}
	}
	return dimStyle.Render(keys)
}

func (m *setupModel) viewWidth() int {
	if m.w <= 4 {
		return maxCardWidth
	}
	return m.w - 2
}

// ----- Helpers ------------------------------------------------------------

func looksLikeHTTPURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// removeLastRune trims the final UTF-8 rune from s.
func removeLastRune(s string) string {
	if len(s) > 0 {
		_, size := utf8.DecodeLastRuneInString(s)
		return s[:len(s)-size]
	}
	return s
}
