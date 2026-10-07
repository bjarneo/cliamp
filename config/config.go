// Package config handles loading user configuration from ~/.config/cliamp/config.toml.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/fileutil"
)

// maxVisRows caps the configurable visualizer height. The layout shrinks the
// value further when the terminal cannot spare the rows.
const maxVisRows = 40

// Path returns the path of config.toml in the cliamp config directory.
func Path() (string, error) {
	dir, err := appdir.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}

// parseString unquotes a TOML string value and, if the result is exactly
// $NAME or ${NAME}, replaces it with the value of that environment variable
// (or "" when unset). Mixed values containing other characters are left
// untouched, so literal '$' in passwords is preserved.
func parseString(s string) string {
	s = unquote(s)
	if name, ok := EnvRef(s); ok {
		return os.Getenv(name)
	}
	return s
}

// EnvRef reports whether the unquoted value s is exactly $NAME or ${NAME}.
// Load reads such a value from the environment variable name, so the text
// itself cannot be stored in config.toml.
func EnvRef(s string) (name string, ok bool) {
	if len(s) < 2 || s[0] != '$' {
		return "", false
	}
	name = s[1:]
	if name[0] == '{' {
		if name[len(name)-1] != '}' {
			return "", false
		}
		name = name[1 : len(name)-1]
	}
	if !isEnvName(name) {
		return "", false
	}
	return name, true
}

// unquote removes one pair of matching quotes from s. Inside double quotes it
// decodes \\ and \", the escapes QuoteString writes, and keeps every other
// backslash as typed, so "D:\new" stays a Windows path. Single quotes are
// literal. A # comment after the closing quote is dropped, with or without
// whitespace before it. A value that does not start with a quote is returned
// unchanged, # included.
func unquote(s string) string {
	if len(s) < 2 || (s[0] != '"' && s[0] != '\'') {
		return s
	}
	q := s[0]
	var b strings.Builder
	closed := false
	for i := 1; i < len(s); i++ {
		c := s[i]
		if q == '"' && c == '\\' && i+1 < len(s) && (s[i+1] == '\\' || s[i+1] == '"') {
			i++
			b.WriteByte(s[i])
			continue
		}
		if c == q {
			if afterClose(s[i+1:]) {
				return b.String()
			}
			closed = true
			break
		}
		b.WriteByte(c)
	}
	// The closing quote is missing, as in "Tokyo Night or 'abc". Strip the
	// opening quote and any stray quote at the end, and keep the rest as
	// typed.
	if !closed {
		return strings.TrimRight(s[1:], `"'`)
	}
	// Text follows the closing quote. Strip the outer pair and keep the rest
	// as typed.
	if s[len(s)-1] == q {
		return s[1 : len(s)-1]
	}
	return s
}

// isComment reports whether rest, the text after a value, is empty or a
// # comment that whitespace separates from the value.
func isComment(rest string) bool {
	trimmed := strings.TrimLeft(rest, " \t")
	if trimmed == "" {
		return true
	}
	return len(trimmed) < len(rest) && trimmed[0] == '#'
}

// afterClose reports whether rest, the text after a closing quote or
// bracket, is empty or a # comment. The value cannot hold that #, so the
// comment needs no whitespace before it.
func afterClose(rest string) bool {
	rest = strings.TrimLeft(rest, " \t")
	return rest == "" || rest[0] == '#'
}

var quoteEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`)

// QuoteString returns s as a double-quoted TOML string that Load reads back
// unchanged. It escapes only \ and ". s must be a single line. The one
// exception is a $NAME or ${NAME} value: Load reads it from the environment.
// Use EnvRef to find such a value.
func QuoteString(s string) string {
	return `"` + quoteEscaper.Replace(s) + `"`
}

func isEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_':
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// parseBool reads a bool value. It accepts true and false in any letter case,
// and the other strconv.ParseBool forms such as 1 and 0. ok is false for any
// other value, so the caller keeps the current setting.
func parseBool(val string) (v, ok bool) {
	v, err := strconv.ParseBool(strings.ToLower(scalar(val)))
	return v, err == nil
}

// parseInt reads an integer value. ok is false for any other value.
func parseInt(val string) (int, bool) {
	v, err := strconv.Atoi(scalar(val))
	return v, err == nil
}

// parseFloat reads a finite number value. ok is false for any other value,
// also for NaN and an infinity, which clamp cannot constrain.
func parseFloat(val string) (float64, bool) {
	v, err := strconv.ParseFloat(scalar(val), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// scalar returns the number or bool token at the start of val without its
// trailing # comment. Only the number and bool parsers call it, so a # in an
// unquoted string value is never cut.
func scalar(val string) string {
	if i := strings.IndexAny(val, " \t"); i >= 0 && isComment(val[i:]) {
		return val[:i]
	}
	return val
}

// Provider sections follow one of three enable rules. Each provider struct
// names its rule, and its IsSet method applies it.
//
//   - Credentials: the provider registers when the section holds the
//     credentials that IsSet needs. The section header alone does nothing.
//   - Section: the section header alone registers the provider. Load sets
//     Enabled when it reads the header, and enabled = false sets Disabled.
//   - Opt-in: the provider registers only when the section sets
//     enabled = true.
//
// Radio and podcasts are always on, so their sections only tune them.

// NavidromeConfig holds credentials for a Navidrome/Subsonic server.
// All three fields must be non-empty for a client to be constructed.
// Enable rule: credentials.
type NavidromeConfig struct {
	URL              string // e.g. "https://music.example.com"
	User             string
	Password         string
	Format           string // requested stream format; empty lets the server decide, "raw" requests the original
	BrowseSort       string // album browse sort order, e.g. "alphabeticalByName"
	ScrobbleDisabled bool   // true only when "scrobble = false" is explicitly set
}

// IsSet reports whether all three Navidrome credentials are present.
func (n NavidromeConfig) IsSet() bool {
	return n.URL != "" && n.User != "" && n.Password != ""
}

// set applies one key of the [navidrome] section.
func (n *NavidromeConfig) set(key, val string) {
	switch key {
	case "url":
		n.URL = parseString(val)
	case "user":
		n.User = parseString(val)
	case "password":
		n.Password = parseString(val)
	case "browse_sort":
		n.BrowseSort = parseString(val)
	case "format":
		n.Format = parseString(val)
	case "scrobble":
		// Opt-out: only mark disabled when the value is explicitly false.
		if v, ok := parseBool(val); ok {
			n.ScrobbleDisabled = !v
		}
	}
}

// LyrionConfig holds settings for a Lyrion Music Server (LMS) instance.
// User and Password are optional — they are only needed when the server has
// password protection enabled.
// Enable rule: credentials, where the URL alone is enough.
type LyrionConfig struct {
	URL      string // e.g. "http://nas.local:9000"
	User     string
	Password string
	// ShowUnplayable includes tracks and playlists contributed by LMS server
	// plugins, which the server cannot stream to cliamp. Hidden by default.
	ShowUnplayable bool
}

// IsSet reports whether a Lyrion server URL is configured. Credentials are
// optional, so the URL alone is enough to construct a client.
func (l LyrionConfig) IsSet() bool {
	return l.URL != ""
}

// set applies one key of the [lyrion] section.
func (l *LyrionConfig) set(key, val string) {
	switch key {
	case "url":
		l.URL = parseString(val)
	case "user":
		l.User = parseString(val)
	case "password":
		l.Password = parseString(val)
	case "show_unplayable":
		if v, ok := parseBool(val); ok {
			l.ShowUnplayable = v
		}
	}
}

// SpotifyConfig holds settings for the Spotify provider. Requires a Spotify
// Premium account. If client_id is empty, a built-in fallback (the librespot
// keymaster ID) is used so search and catalog endpoints work even for users
// who never registered their own developer app — see Spotify's Nov 27, 2024
// dev-mode quota restriction.
// Enable rule: section.
type SpotifyConfig struct {
	Disabled bool   // true only when user explicitly sets enabled = false
	Enabled  bool   // true when [spotify] section exists (even without client_id)
	ClientID string // Spotify Developer app client ID (overrides built-in fallback)
	Bitrate  int    // preferred Spotify stream bitrate in kbps
}

// IsSet reports whether the Spotify provider should be shown. Section presence
// is enough — a built-in fallback client_id is used when none is configured.
func (s SpotifyConfig) IsSet() bool {
	return !s.Disabled && s.Enabled
}

// ResolveClientID returns the user's configured client_id, or fallbackID when
// none is set.
func (s SpotifyConfig) ResolveClientID(fallbackID string) string {
	if s.ClientID != "" {
		return s.ClientID
	}
	return fallbackID
}

// set applies one key of the [spotify] section.
func (s *SpotifyConfig) set(key, val string) {
	switch key {
	case "enabled":
		if v, ok := parseBool(val); ok {
			s.Disabled = !v
		}
	case "client_id":
		s.ClientID = parseString(val)
	case "bitrate":
		if v, ok := parseInt(val); ok {
			s.Bitrate = v
		}
	}
}

// QobuzConfig holds settings for the Qobuz provider. Requires a paid Qobuz
// subscription (Studio/Sublime). The app_id, signing secrets and OAuth private
// key are scraped automatically from the Qobuz web player, so no developer
// credentials are needed. Sign-in is an interactive OAuth browser flow.
// Enable rule: section.
type QobuzConfig struct {
	Disabled bool // true only when user explicitly sets enabled = false
	Enabled  bool // true when [qobuz] section exists
	Quality  int  // preferred stream format_id: 5 (MP3 320), 6 (FLAC CD), 7 (Hi-Res <=96kHz), 27 (Hi-Res <=192kHz)
}

// IsSet reports whether the Qobuz provider should be shown. Section presence
// is enough; credentials are scraped from the Qobuz web player automatically.
func (q QobuzConfig) IsSet() bool {
	return !q.Disabled && q.Enabled
}

// set applies one key of the [qobuz] section.
func (q *QobuzConfig) set(key, val string) {
	switch key {
	case "enabled":
		if v, ok := parseBool(val); ok {
			q.Disabled = !v
		}
	case "quality":
		if v, ok := parseInt(val); ok {
			q.Quality = v
		}
	}
}

// TidalConfig holds settings for the Tidal provider. Requires a paid Tidal
// subscription (all paid plans include lossless FLAC). Sign-in is an OAuth
// device flow (link.tidal.com). Built-in fallback client credentials are used
// when none are configured; Tidal revokes leaked client IDs periodically, so
// client_id/client_secret can be overridden without waiting for a release.
// Enable rule: section.
type TidalConfig struct {
	Disabled     bool   // true only when user explicitly sets enabled = false
	Enabled      bool   // true when [tidal] section exists
	ClientID     string // OAuth client ID (overrides built-in fallback)
	ClientSecret string // OAuth client secret (overrides built-in fallback)
	Quality      string // preferred quality: "low", "high", "lossless", "hires"
}

// IsSet reports whether the Tidal provider should be shown. Section presence
// is enough — built-in fallback client credentials are used when none are set.
func (t TidalConfig) IsSet() bool {
	return !t.Disabled && t.Enabled
}

// set applies one key of the [tidal] section.
func (t *TidalConfig) set(key, val string) {
	switch key {
	case "enabled":
		if v, ok := parseBool(val); ok {
			t.Disabled = !v
		}
	case "client_id":
		t.ClientID = parseString(val)
	case "client_secret":
		t.ClientSecret = parseString(val)
	case "quality":
		t.Quality = parseString(val)
	}
}

// YouTubeMusicConfig holds settings for the YouTube Music provider.
// cliamp ships no OAuth client, so sign-in needs client_id and client_secret
// from the user. cookies_from alone enables the cookie mode.
// Enable rule: section. The [yt] and [youtube] headers count as [ytmusic].
type YouTubeMusicConfig struct {
	Disabled       bool   // true only when user explicitly sets enabled = false
	Enabled        bool   // true when [ytmusic] section exists (even without credentials)
	ClientID       string // Google Cloud OAuth2 client ID
	ClientSecret   string // Google Cloud OAuth2 client secret
	CookiesFrom    string // browser name for yt-dlp --cookies-from-browser (e.g. "chrome", "firefox")
	ExpandPlaylist *bool  // nil = default (true), controls whether list= URLs expand the full playlist
}

// IsSet reports whether the YouTube providers should be enabled: the
// [ytmusic] section exists or cookies_from is set, and enabled is not false.
func (y YouTubeMusicConfig) IsSet() bool {
	return !y.Disabled && (y.Enabled || strings.TrimSpace(y.CookiesFrom) != "")
}

// set applies one key of the [ytmusic] section.
func (y *YouTubeMusicConfig) set(key, val string) {
	switch key {
	case "enabled":
		if v, ok := parseBool(val); ok {
			y.Disabled = !v
		}
	case "client_id":
		y.ClientID = parseString(val)
	case "client_secret":
		y.ClientSecret = parseString(val)
	case "cookies_from":
		y.CookiesFrom = strings.TrimSpace(parseString(val))
	case "expand_playlist":
		if v, ok := parseBool(val); ok {
			y.ExpandPlaylist = &v
		}
	}
}

// RadioConfig holds settings for the built-in Radio provider. Radio is always
// enabled, so this block only tunes it.
type RadioConfig struct {
	// Country is the listener's home country as an ISO 3166-1 alpha-2 code.
	// It puts a "near you" row at the top of the radio pane, offers that
	// country's regions in the country browser, and is the first stop of the
	// catalog country filter. Unset means "detect from the system timezone,
	// then the locale"; set it to "none" to turn detection off.
	Country string
}

// set applies one key of the [radio] section.
func (r *RadioConfig) set(key, val string) {
	switch key {
	case "country":
		r.Country = strings.TrimSpace(parseString(val))
	}
}

// PodcastConfig tunes the always-available public podcast directory.
type PodcastConfig struct {
	Country string // two-letter country code for Apple charts (default "us")
}

// set applies one key of the [podcast] section.
func (p *PodcastConfig) set(key, val string) {
	if key == "country" {
		p.Country = strings.TrimSpace(parseString(val))
	}
}

// SoundCloudConfig holds settings for the SoundCloud provider.
// SoundCloud is opt-in: requires enabled = true in [soundcloud] before the
// provider registers. Setting User exposes that profile's Tracks/Likes/Reposts
// in the browse view. Setting CookiesFrom (browser name) lets yt-dlp use the
// user's signed-in session for subscriber-gated tracks.
// Enable rule: opt-in.
type SoundCloudConfig struct {
	Enabled     bool   // true only when user explicitly sets enabled = true
	User        string // SoundCloud username for browse (optional)
	CookiesFrom string // browser name for yt-dlp --cookies-from-browser (optional)
}

// IsSet reports whether the SoundCloud provider should be shown.
func (s SoundCloudConfig) IsSet() bool { return s.Enabled }

// set applies one key of the [soundcloud] section.
func (s *SoundCloudConfig) set(key, val string) {
	switch key {
	case "enabled":
		if v, ok := parseBool(val); ok {
			s.Enabled = v
		}
	case "user":
		s.User = parseString(val)
	case "cookies_from":
		s.CookiesFrom = strings.TrimSpace(parseString(val))
	}
}

// MixcloudConfig holds settings for the Mixcloud provider. Public discovery
// works with only enabled=true. Username adds public account views; an access
// token adds /me and Listen Later; browser cookies are used only by yt-dlp for
// playback that needs the listener's signed-in Mixcloud session.
// Enable rule: opt-in.
type MixcloudConfig struct {
	Enabled        bool
	Username       string
	AccessToken    string
	CookiesFrom    string
	Styles         []string
	StylesSet      bool // distinguishes omitted styles (defaults) from an explicit empty list
	MaxItems       int
	StreamCreators int
}

// IsSet reports whether the Mixcloud provider should be shown.
func (m MixcloudConfig) IsSet() bool { return m.Enabled }

// set applies one key of the [mixcloud] section.
func (m *MixcloudConfig) set(key, val string) {
	switch key {
	case "enabled":
		if v, ok := parseBool(val); ok {
			m.Enabled = v
		}
	case "username":
		m.Username = strings.TrimSpace(parseString(val))
	case "access_token":
		m.AccessToken = strings.TrimSpace(parseString(val))
	case "cookies_from":
		m.CookiesFrom = strings.TrimSpace(parseString(val))
	case "styles":
		m.Styles = parseStringSlice(val)
		m.StylesSet = true
	case "max_items":
		if v, ok := parseInt(val); ok {
			m.MaxItems = v
		}
	case "stream_creators":
		if v, ok := parseInt(val); ok {
			m.StreamCreators = v
		}
	}
}

// NetEaseConfig holds settings for the NetEase Cloud Music provider.
// The provider is opt-in and can reuse an existing browser session through
// yt-dlp's --cookies-from-browser support.
// Enable rule: opt-in.
type NetEaseConfig struct {
	Enabled     bool   // true only when user explicitly sets enabled = true
	CookiesFrom string // browser name for account APIs and playback (e.g. "chrome")
	UserID      string // optional account user id; setup can discover this from cookies
}

// IsSet reports whether the NetEase provider should be shown.
func (n NetEaseConfig) IsSet() bool { return n.Enabled }

// set applies one key of the [netease] section.
func (n *NetEaseConfig) set(key, val string) {
	switch key {
	case "enabled":
		if v, ok := parseBool(val); ok {
			n.Enabled = v
		}
	case "cookies_from":
		n.CookiesFrom = strings.TrimSpace(parseString(val))
	case "user_id":
		n.UserID = parseString(val)
	}
}

// YandexConfig holds settings for the Yandex Music provider.
// The provider is opt-in and authenticates with a personal OAuth token
// obtained from https://oauth.yandex.ru/authorize?response_type=token&client_id=23cabbbdc6cd418abb4b39c32c41195d
// Enable rule: opt-in, and Token must also be set.
type YandexConfig struct {
	Enabled bool   // true only when user explicitly sets enabled = true
	Token   string // personal OAuth token
}

// IsSet reports whether the Yandex provider should be shown.
func (y YandexConfig) IsSet() bool { return y.Enabled && strings.TrimSpace(y.Token) != "" }

// set applies one key of the [yandex] section.
func (y *YandexConfig) set(key, val string) {
	switch key {
	case "enabled":
		if v, ok := parseBool(val); ok {
			y.Enabled = v
		}
	case "token":
		y.Token = parseString(val)
	}
}

// PlexConfig holds credentials for a Plex Media Server.
// Both URL and Token must be non-empty for a client to be constructed.
// Enable rule: credentials.
type PlexConfig struct {
	URL       string   // e.g. "http://192.168.1.10:32400"
	Token     string   // X-Plex-Token
	Libraries []string // optional: restrict to these music library names
}

// IsSet reports whether both Plex credentials are present.
func (p PlexConfig) IsSet() bool {
	return p.URL != "" && p.Token != ""
}

// set applies one key of the [plex] section.
func (p *PlexConfig) set(key, val string) {
	switch key {
	case "url":
		p.URL = parseString(val)
	case "token":
		p.Token = parseString(val)
	case "libraries":
		p.Libraries = parseStringSlice(val)
	}
}

// JellyfinConfig holds credentials for a Jellyfin server.
// URL is required. Authenticate either with Token, or with User+Password.
// UserID is optional and can be discovered lazily.
// Enable rule: credentials.
type JellyfinConfig struct {
	URL      string // e.g. "https://jellyfin.example.com"
	Token    string // API access token
	User     string // optional username for password-based login
	Password string // optional password for password-based login
	UserID   string // optional user id to skip discovery via /Users/Me
}

// IsSet reports whether the Jellyfin provider is configured.
func (j JellyfinConfig) IsSet() bool {
	return j.URL != "" && (j.Token != "" || (j.User != "" && j.Password != ""))
}

// set applies one key of the [jellyfin] section.
func (j *JellyfinConfig) set(key, val string) {
	switch key {
	case "url":
		j.URL = parseString(val)
	case "token":
		j.Token = parseString(val)
	case "user":
		j.User = parseString(val)
	case "password":
		j.Password = parseString(val)
	case "user_id":
		j.UserID = parseString(val)
	}
}

// EmbyConfig holds credentials for an Emby server.
// URL is required. Authenticate either with Token, or with User+Password.
// UserID is optional and can be discovered lazily.
// Enable rule: credentials.
type EmbyConfig struct {
	URL      string // e.g. "https://emby.example.com"
	Token    string // API access token
	User     string // optional username for password-based login
	Password string // optional password for password-based login
	UserID   string // optional user id to skip discovery via /Users/Me
}

// IsSet reports whether the Emby provider is configured.
func (e EmbyConfig) IsSet() bool {
	return e.URL != "" && (e.Token != "" || (e.User != "" && e.Password != ""))
}

// set applies one key of the [emby] section.
func (e *EmbyConfig) set(key, val string) {
	switch key {
	case "url":
		e.URL = parseString(val)
	case "token":
		e.Token = parseString(val)
	case "user":
		e.User = parseString(val)
	case "password":
		e.Password = parseString(val)
	case "user_id":
		e.UserID = parseString(val)
	}
}

// AudiobookshelfConfig holds credentials for an Audiobookshelf server.
// URL is required. Authenticate either with Token, or with User+Password.
// Enable rule: credentials.
type AudiobookshelfConfig struct {
	URL       string   // e.g. "https://abs.example.com"
	Token     string   // API key or login token
	User      string   // optional username for password-based login
	Password  string   // optional password for password-based login
	Libraries []string // optional: restrict to these library names
}

// IsSet reports whether the Audiobookshelf provider is configured.
func (a AudiobookshelfConfig) IsSet() bool {
	return a.URL != "" && (a.Token != "" || (a.User != "" && a.Password != ""))
}

// set applies one key of the [audiobookshelf] section.
func (a *AudiobookshelfConfig) set(key, val string) {
	switch key {
	case "url":
		a.URL = parseString(val)
	case "token":
		a.Token = parseString(val)
	case "user":
		a.User = parseString(val)
	case "password":
		a.Password = parseString(val)
	case "libraries":
		a.Libraries = parseStringSlice(val)
	}
}

// DownloadsConfig selects the directory for saved audio. Empty uses ~/Music/cliamp.
type DownloadsConfig struct {
	Directory string
}

// set applies one key of the [downloads] section.
func (d *DownloadsConfig) set(key, val string) {
	if key == "directory" {
		d.Directory = parseString(val)
	}
}

// Config holds user preferences loaded from the config file.
type Config struct {
	Downloads        DownloadsConfig
	Volume           float64     // dB, clamped at runtime to [VolumeMin, +6]
	VolumeMin        float64     // dB floor, range [-90, 0]; default -50
	VisVolumeLinked  bool        // when true, visualizer bar height follows volume; default true
	EQ               [10]float64 // per-band gain in dB, range [-12, +12]
	EQPreset         string      // preset name, or "" for custom
	Repeat           string      // "off", "all", or "one"
	Shuffle          bool
	Mono             bool
	ReplayGain       string                       // loudness normalisation: "off" (default), "track" or "album"
	ReplayGainPreamp float64                      // dB added to ReplayGain adjustments, range [-15, +15]
	Speed            float64                      // playback speed ratio: 0.25–2.0 (default 1.0)
	AutoPlay         bool                         // start playback automatically on launch (radio streams, CLI tracks)
	SeekStepLarge    int                          // seconds for Shift+Left/Right seek jumps
	Provider         string                       // default provider key, a value that --provider accepts (default "cliamp")
	Theme            string                       // theme name, or "" for ANSI default
	Visualizer       string                       // visualizer mode name, or "" for default (Bars)
	VisRows          int                          // visualizer height in rows at the full layout tier, or 0 for the built-in default
	SampleRate       int                          // output sample rate: 22050, 44100, 48000, 96000, 192000
	BufferMs         int                          // speaker buffer in milliseconds (50-5000)
	ResampleQuality  int                          // beep resample quality factor (1–4)
	BitDepth         int                          // PCM bit depth for FFmpeg output: 16 or 32
	Simplified       bool                         // simplified playback view: track summary and time strip
	HideHelpBar      bool                         // hide the key-binding hint bar above the status line
	HideSettingsPane bool                         // close the settings pane beside the playlist
	ShowMetadata     bool                         // expand highlighted-track metadata below settings (default false)
	Expanded         bool                         // start with the playlist expanded (the Ctrl+X state)
	PaddingH         int                          // horizontal padding for the UI frame (default 3)
	PaddingV         int                          // vertical padding for the UI frame (default 1)
	AudioDevice      string                       // preferred audio output device name (empty = system default)
	Playlist         string                       // local TOML playlist name to load on startup
	InitialDirectory string                       // initial directory for the file browser
	LyricsOffsetMs   int                          // lyric timestamp adjustment in ms (-10000..10000), applied to all sources
	Navidrome        NavidromeConfig              // optional Navidrome/Subsonic server credentials
	Lyrion           LyrionConfig                 // optional Lyrion Music Server (LMS) instance
	Spotify          SpotifyConfig                // optional Spotify provider (requires Premium)
	Qobuz            QobuzConfig                  // optional Qobuz provider (requires subscription)
	Tidal            TidalConfig                  // optional Tidal provider (requires subscription)
	YouTubeMusic     YouTubeMusicConfig           // optional YouTube Music provider
	Plex             PlexConfig                   // optional Plex Media Server credentials
	Jellyfin         JellyfinConfig               // optional Jellyfin server credentials
	Emby             EmbyConfig                   // optional Emby server credentials
	Audiobookshelf   AudiobookshelfConfig         // optional Audiobookshelf server credentials
	Radio            RadioConfig                  // built-in Radio provider settings
	Podcast          PodcastConfig                // built-in podcast directory settings
	SoundCloud       SoundCloudConfig             // SoundCloud provider (opt-in via enabled = true)
	Mixcloud         MixcloudConfig               // Mixcloud provider (opt-in via enabled = true)
	NetEase          NetEaseConfig                // NetEase Cloud Music provider (opt-in via enabled = true)
	Yandex           YandexConfig                 // Yandex Music provider (opt-in via enabled = true)
	Plugins          map[string]map[string]string // per-plugin config from [plugins.*] sections
	LogLevel         string                       // log level: debug, info, warn, error (default "info")
	LowPower         bool                         // reduce CPU by lowering UI cadence and disabling visualization
}

// defaultConfig returns a Config with sensible defaults.
// SampleRate defaults to 0, which means "auto-detect from the system's default
// output device" (see player.DeviceSampleRate). This ensures USB audio devices
// that require a specific rate (commonly 48 kHz) work out of the box.
func defaultConfig() Config {
	return Config{
		VolumeMin:       -50,
		VisVolumeLinked: true,
		ReplayGain:      "off",
		Repeat:          "off",
		AutoPlay:        false,
		Speed:           1.0,
		SeekStepLarge:   30,
		SampleRate:      0,
		BufferMs:        250,
		ResampleQuality: 4,
		BitDepth:        16,
		PaddingH:        3,
		PaddingV:        1,
		Spotify:         SpotifyConfig{Bitrate: 320},
		Qobuz:           QobuzConfig{Quality: 6},
		LogLevel:        "info",
	}
}

// Load reads the config file from ~/.config/cliamp/config.toml.
// Returns defaults if the file does not exist.
func Load() (Config, error) {
	cfg := defaultConfig()

	path, err := Path()
	if err != nil {
		return cfg, nil
	}

	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cfg, nil
		}
		return cfg, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	section := "" // current [section] header, empty = top-level
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Section header: [navidrome], [plex], [plugins.lastfm], etc.
		if name, ok := sectionHeader(line); ok {
			section = canonicalSection(name)
			// Mark providers as enabled when their section exists.
			switch section {
			case "ytmusic":
				cfg.YouTubeMusic.Enabled = true
			case "spotify":
				cfg.Spotify.Enabled = true
			case "qobuz":
				cfg.Qobuz.Enabled = true
			case "tidal":
				cfg.Tidal.Enabled = true
			}
			// Initialize plugin sub-maps for [plugins] and [plugins.*] sections.
			if name, ok := pluginSection(section); ok {
				if cfg.Plugins == nil {
					cfg.Plugins = make(map[string]map[string]string)
				}
				if _, ok := cfg.Plugins[name]; !ok {
					cfg.Plugins[name] = make(map[string]string)
				}
			}
			continue
		}

		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)

		switch section {
		case "downloads":
			cfg.Downloads.set(key, val)
		case "navidrome":
			cfg.Navidrome.set(key, val)
		case "lyrion":
			cfg.Lyrion.set(key, val)
		case "spotify":
			cfg.Spotify.set(key, val)
		case "qobuz":
			cfg.Qobuz.set(key, val)
		case "tidal":
			cfg.Tidal.set(key, val)
		case "ytmusic":
			cfg.YouTubeMusic.set(key, val)
		case "plex":
			cfg.Plex.set(key, val)
		case "radio":
			cfg.Radio.set(key, val)
		case "podcast":
			cfg.Podcast.set(key, val)
		case "soundcloud":
			cfg.SoundCloud.set(key, val)
		case "mixcloud":
			cfg.Mixcloud.set(key, val)
		case "netease":
			cfg.NetEase.set(key, val)
		case "yandex":
			cfg.Yandex.set(key, val)
		case "jellyfin":
			cfg.Jellyfin.set(key, val)
		case "emby":
			cfg.Emby.set(key, val)
		case "audiobookshelf":
			cfg.Audiobookshelf.set(key, val)
		default:
			if name, ok := pluginSection(section); ok {
				if m, ok := cfg.Plugins[name]; ok {
					m[key] = pluginValue(name, key, val)
				}
			} else {
				cfg.setTopLevel(key, val)
			}
		}
	}

	cfg.clamp()
	return cfg, scanner.Err()
}

// sectionHeader returns the name inside a [name] header line. A # comment
// may follow the closing bracket.
func sectionHeader(line string) (string, bool) {
	if !strings.HasPrefix(line, "[") {
		return "", false
	}
	if end := strings.IndexByte(line, ']'); end > 0 && afterClose(line[end+1:]) {
		return line[1:end], true
	}
	if strings.HasSuffix(line, "]") {
		return line[1 : len(line)-1], true
	}
	return "", false
}

// canonicalSection returns the name that Load uses for a section header. It
// ignores letter case, and [yt], [youtube] and [ytmusic] all configure the
// same YouTube providers.
func canonicalSection(name string) string {
	name = strings.ToLower(name)
	switch name {
	case "yt", "youtube":
		return "ytmusic"
	}
	return name
}

// pluginSection returns the plugin name of a [plugins] or [plugins.<name>]
// section. The top-level [plugins] section has the empty name.
func pluginSection(section string) (string, bool) {
	if section == "plugins" {
		return "", true
	}
	return strings.CutPrefix(section, "plugins.")
}

// pluginValue reads a value of the [plugins] section, or of the
// [plugins.<name>] section when plugin is not empty. Plugins get each value
// as a string. cliamp reads three keys itself, so these follow the value
// rules: enabled of a plugin is a bool, and disabled and allowed_binaries of
// [plugins] are lists. Load stores the bool as true or false, and a list in
// square brackets as names that commas separate.
func pluginValue(plugin, key, val string) string {
	switch {
	case plugin != "" && key == "enabled":
		if v, ok := parseBool(val); ok {
			return strconv.FormatBool(v)
		}
	case plugin == "" && (key == "disabled" || key == "allowed_binaries"):
		if strings.HasPrefix(val, "[") {
			return strings.Join(parseStringSlice(val), ",")
		}
	}
	return parseString(val)
}

// setTopLevel applies one top-level key. Keys under an unknown section also
// land here.
func (c *Config) setTopLevel(key, val string) {
	switch key {
	case "volume":
		if v, ok := parseFloat(val); ok {
			c.Volume = v
		}
	case "volume_min":
		if v, ok := parseFloat(val); ok {
			c.VolumeMin = v
		}
	case "vis_volume_linked":
		if v, ok := parseBool(val); ok {
			c.VisVolumeLinked = v
		}
	case "repeat":
		val = parseString(val)
		switch strings.ToLower(val) {
		case "all", "one", "off":
			c.Repeat = strings.ToLower(val)
		}
	case "shuffle":
		if v, ok := parseBool(val); ok {
			c.Shuffle = v
		}
	case "mono":
		if v, ok := parseBool(val); ok {
			c.Mono = v
		}
	case "replaygain":
		switch v := strings.ToLower(parseString(val)); v {
		case "off", "track", "album":
			c.ReplayGain = v
		}
	case "replaygain_preamp":
		if v, ok := parseFloat(val); ok {
			c.ReplayGainPreamp = v
		}
	case "auto_play":
		if v, ok := parseBool(val); ok {
			c.AutoPlay = v
		}
	case "seek_large_step_sec":
		if v, ok := parseInt(val); ok {
			c.SeekStepLarge = v
		}
	case "lyrics_offset_ms":
		if v, ok := parseInt(val); ok {
			c.LyricsOffsetMs = v
		}
	case "eq":
		c.EQ = parseEQ(val)
	case "eq_preset":
		c.EQPreset = parseString(val)
	case "theme":
		c.Theme = parseString(val)
	case "provider":
		c.Provider = strings.ToLower(parseString(val))
	case "visualizer":
		c.Visualizer = parseString(val)
	case "vis_rows":
		if v, ok := parseInt(val); ok {
			c.VisRows = v
		}
	case "sample_rate":
		if v, ok := parseInt(val); ok {
			c.SampleRate = v
		}
	case "buffer_ms":
		if v, ok := parseInt(val); ok {
			c.BufferMs = v
		}
	case "resample_quality":
		if v, ok := parseInt(val); ok {
			c.ResampleQuality = v
		}
	case "bit_depth":
		if v, ok := parseInt(val); ok {
			c.BitDepth = v
		}
	case "speed":
		if v, ok := parseFloat(val); ok {
			c.Speed = v
		}
	case "simplified":
		if v, ok := parseBool(val); ok {
			c.Simplified = v
		}
	case "hide_help_bar":
		if v, ok := parseBool(val); ok {
			c.HideHelpBar = v
		}
	case "hide_settings_pane":
		if v, ok := parseBool(val); ok {
			c.HideSettingsPane = v
		}
	case "show_metadata":
		if v, ok := parseBool(val); ok {
			c.ShowMetadata = v
		}
	case "expanded":
		if v, ok := parseBool(val); ok {
			c.Expanded = v
		}
	case "audio_device":
		c.AudioDevice = parseString(val)
	case "initial_directory":
		c.InitialDirectory = parseString(val)
	case "padding_horizontal":
		if v, ok := parseInt(val); ok {
			c.PaddingH = v
		}
	case "padding_vertical":
		if v, ok := parseInt(val); ok {
			c.PaddingV = v
		}
	case "log_level":
		lvl := strings.ToLower(parseString(val))
		switch lvl {
		case "debug", "info", "warn", "warning", "error":
			c.LogLevel = lvl
		}
	case "low_power":
		if v, ok := parseBool(val); ok {
			c.LowPower = v
		}
	}
}

// save updates only the given key in the existing config file, preserving
// all other content, comments, and formatting. If the key doesn't exist,
// it is appended. If no config file exists, one is created with just that key.
// The file always ends with a newline. value is the TOML text of the value.
// The typed savers SaveString, SaveBool, SaveFloat and SaveFloats format it.
func save(key, value string) error {
	if strings.ContainsAny(key+value, "\r\n") {
		return fmt.Errorf("save %s: a key or value holds a line break", key)
	}
	line := fmt.Sprintf("%s = %s", key, value)
	return update(func(data string) string { return editTopLevel(data, key, line) })
}

// saveMu serializes the config saves of this process. The lock file in
// update serializes them across cliamp processes.
var saveMu sync.Mutex

// update reads config.toml, passes its text to edit and writes the result.
// A missing file reads as empty text. A lock file next to config.toml covers
// the read, the edit and the write, so a save from another cliamp process,
// such as cliamp setup next to the TUI, cannot get lost in between.
func update(edit func(data string) string) error {
	path, err := Path()
	if err != nil {
		return fmt.Errorf("resolve config path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	saveMu.Lock()
	defer saveMu.Unlock()
	unlock, err := fileutil.LockFile(path + ".lock")
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()

	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read config: %w", err)
	}
	if err := fileutil.WriteFileAtomic(path, []byte(edit(string(data))), 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// editTopLevel returns data with line written as the top-level key. save
// describes the rules.
func editTopLevel(data, key, line string) string {
	// Scan existing lines and replace the matching key in-place,
	// but only in the top-level scope (before any [section] header).
	// Load uses the last line of a duplicate key, so replace every line.
	lines := strings.Split(data, "\n")
	found := false
	lastKey, header := -1, -1 // indexes of the last top-level key and the first header
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Stop searching once we hit a section header — the key
		// belongs in the top-level scope only.
		if _, ok := sectionHeader(trimmed); ok {
			header = i
			break
		}
		k, _, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		lastKey = i
		if strings.TrimSpace(k) == key {
			lines[i] = line
			found = true
		}
	}
	switch {
	case found:
	case header >= 0 && lastKey >= 0:
		// Keep top-level keys together.
		lines = slices.Insert(lines, lastKey+1, line)
	case header >= 0:
		// The comment and blank lines above the first header describe the
		// section, so the key goes above them.
		at := header
		for at > 0 {
			if t := strings.TrimSpace(lines[at-1]); t != "" && !strings.HasPrefix(t, "#") {
				break
			}
			at--
		}
		lines = slices.Insert(lines, at, line)
	default:
		// A final newline leaves an empty last element. The key goes
		// before it, so it does not land after a blank line.
		end := len(lines)
		if lines[end-1] == "" {
			end--
		}
		lines = slices.Insert(lines, end, line)
	}
	if lines[len(lines)-1] != "" {
		lines = append(lines, "") // end the file with one newline
	}
	return strings.Join(lines, "\n")
}

// SaveNavidromeSort persists the given album browse sort type to the
// [navidrome] section of the config file. It rewrites the browse_sort key
// in-place, or appends it after the [navidrome] section if not present.
// If no [navidrome] section exists, one is appended along with the key.
func SaveNavidromeSort(sortType string) error {
	return SaveSection("navidrome", []KeyValue{{"browse_sort", QuoteString(sortType)}}, nil)
}

// SaveRadioCountry persists the listener's home country in the [radio] section
// so the choice survives a restart. Pass "" to record that detection should be
// turned off.
func SaveRadioCountry(code string) error {
	if code == "" {
		code = "none"
	}
	return SaveSection("radio", []KeyValue{{"country", QuoteString(code)}}, nil)
}

// SaveMixcloudStyles persists the selected discovery styles in the [mixcloud]
// section without disturbing other provider settings or comments.
func SaveMixcloudStyles(styles []string) error {
	quoted := make([]string, 0, len(styles))
	for _, style := range styles {
		quoted = append(quoted, QuoteString(style))
	}
	return SaveSection("mixcloud", []KeyValue{{"styles", "[" + strings.Join(quoted, ", ") + "]"}}, nil)
}

// KeyValue is one key line for SaveSection. Value is the TOML text of the
// value, such as QuoteString(s), a number, a bool or a list.
type KeyValue struct {
	Key, Value string
}

// SaveSection writes kv into the [section] block of the config file and
// keeps every other line. It replaces a key in place and adds a missing key
// after the last key of the section. It removes each key in owned that kv
// does not hold, and it keeps all other keys and all comments. Headers match
// the way Load reads them, so section "ytmusic" also edits [youtube] or [yt].
// A missing section is added at the end, and a missing file is created.
func SaveSection(section string, kv []KeyValue, owned []string) error {
	for _, e := range kv {
		if strings.ContainsAny(e.Key+e.Value, "\r\n") {
			return fmt.Errorf("save [%s] %s: a key or value holds a line break", section, e.Key)
		}
	}
	return update(func(data string) string { return editSection(data, section, kv, owned) })
}

// editSection returns data with kv written into [section]. SaveSection
// describes the rules.
func editSection(data, section string, kv []KeyValue, owned []string) string {
	values := make(map[string]string, len(kv))
	for _, e := range kv {
		values[e.Key] = e.Value
	}
	written := make(map[string]bool, len(kv))
	target := canonicalSection(section)

	var lines []string
	if data != "" {
		lines = strings.Split(data, "\n")
	}
	out := make([]string, 0, len(lines)+len(kv)+3)
	inSection := false
	block := 0     // matching blocks seen so far
	insertAt := -1 // missing keys go before out[insertAt]
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if name, ok := sectionHeader(trimmed); ok {
			inSection = canonicalSection(name) == target
			out = append(out, l)
			if inSection {
				block++
				if block == 1 {
					insertAt = len(out)
				}
			}
			continue
		}
		if inSection && !strings.HasPrefix(trimmed, "#") {
			if before, _, ok := strings.Cut(l, "="); ok {
				key := strings.TrimSpace(before)
				if v, ok := values[key]; ok {
					if !strings.HasSuffix(before, " ") && !strings.HasSuffix(before, "\t") {
						before += " "
					}
					l = before + "= " + v
					written[key] = true
				} else if slices.Contains(owned, key) {
					continue
				}
				out = append(out, l)
				if block == 1 {
					insertAt = len(out)
				}
				continue
			}
		}
		out = append(out, l)
	}

	var missing []string
	for _, e := range kv {
		if !written[e.Key] {
			written[e.Key] = true
			missing = append(missing, e.Key+" = "+values[e.Key])
		}
	}
	switch {
	case len(missing) == 0:
	case insertAt >= 0:
		out = slices.Insert(out, insertAt, missing...)
	default:
		for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			out = out[:len(out)-1]
		}
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, "["+section+"]")
		out = append(out, missing...)
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

// PlayerConfig is the subset of player controls needed to apply config.
type PlayerConfig interface {
	SetVolumeMin(db float64)
	SetVolume(db float64)
	SetSpeed(ratio float64)
	SetEQBand(band int, dB float64)
	ToggleMono()
}

// PlaylistConfig is the subset of playlist controls needed to apply config.
type PlaylistConfig interface {
	CycleRepeat()
	ToggleShuffle()
}

// ApplyPlayer applies audio-engine settings from the config.
func (c Config) ApplyPlayer(p PlayerConfig) {
	p.SetVolumeMin(c.VolumeMin)
	p.SetVolume(c.Volume)
	if c.Speed != 0 && c.Speed != 1.0 {
		p.SetSpeed(c.Speed)
	}
	if c.EQPreset == "" || c.EQPreset == "Custom" {
		for i, gain := range c.EQ {
			p.SetEQBand(i, gain)
		}
	}
	if c.Mono {
		p.ToggleMono()
	}
}

// ApplyPlaylist applies playlist-state settings from the config.
func (c Config) ApplyPlaylist(pl PlaylistConfig) {
	switch c.Repeat {
	case "all":
		pl.CycleRepeat() // off -> all
	case "one":
		pl.CycleRepeat() // off -> all
		pl.CycleRepeat() // all -> one
	}
	if c.Shuffle {
		pl.ToggleShuffle()
	}
}

// SeekStepLargeDuration returns the configured Shift+Left/Right seek jump.
func (c Config) SeekStepLargeDuration() time.Duration {
	return time.Duration(c.SeekStepLarge) * time.Second
}

// clamp constrains all Config fields to their valid ranges.
func (c *Config) clamp() {
	c.VolumeMin = max(min(c.VolumeMin, 0), -90)
	c.Volume = max(min(c.Volume, 6), c.VolumeMin)
	if c.Speed < 0.25 || c.Speed > 2.0 {
		c.Speed = 1.0
	}
	c.SeekStepLarge = max(min(c.SeekStepLarge, 600), 6)
	c.LyricsOffsetMs = max(min(c.LyricsOffsetMs, 10000), -10000)
	c.ReplayGainPreamp = max(min(c.ReplayGainPreamp, 15), -15)
	c.SampleRate = clampSampleRate(c.SampleRate)
	c.BufferMs = max(min(c.BufferMs, 5000), 50)
	c.ResampleQuality = max(min(c.ResampleQuality, 4), 1)
	c.BitDepth = clampBitDepth(c.BitDepth)
	c.Spotify.Bitrate = clampSpotifyBitrate(c.Spotify.Bitrate)
	c.PaddingH = max(min(c.PaddingH, 10), 0)
	c.PaddingV = max(min(c.PaddingV, 5), 0)
	if c.VisRows != 0 {
		c.VisRows = max(min(c.VisRows, maxVisRows), 1)
	}
	if c.LowPower {
		c.Visualizer = "none"
	}
}

// nearestAllowed returns the value in allowed closest to v.
// allowed must be non-empty.
func nearestAllowed(v int, allowed []int) int {
	best := allowed[0]
	bestDist := abs(v - best)
	for _, a := range allowed[1:] {
		if d := abs(v - a); d < bestDist {
			best = a
			bestDist = d
		}
	}
	return best
}

// clampSampleRate returns the nearest valid sample rate from the allowed set.
// A value of 0 is preserved as-is to signal "auto-detect" to the player.
func clampSampleRate(v int) int {
	if v == 0 {
		return 0 // auto-detect
	}
	return nearestAllowed(v, []int{22050, 44100, 48000, 96000, 192000})
}

// clampBitDepth returns the nearest valid bit depth (16 or 32).
func clampBitDepth(v int) int {
	if v >= 24 {
		return 32
	}
	return 16
}

func clampSpotifyBitrate(v int) int {
	if v <= 0 {
		return 320
	}
	return nearestAllowed(v, []int{96, 160, 320})
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// listItems returns the items of a list value without the square brackets
// and without a # comment after the closing bracket. The list ends at the
// first ] outside quotes. A value that does not start with [ is returned
// unchanged.
func listItems(val string) string {
	if !strings.HasPrefix(val, "[") {
		return val
	}
	if end := listEnd(val); end > 0 && afterClose(val[end+1:]) {
		return val[1:end]
	}
	return strings.Trim(val, "[]")
}

// listEnd returns the index of the first ] outside quotes in val, or -1. A
// backslash inside double quotes hides the next byte, as in unquote.
func listEnd(val string) int {
	var quote byte
	for i := 1; i < len(val); i++ {
		c := val[i]
		switch {
		case quote == '"' && c == '\\':
			i++
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == ']':
			return i
		}
	}
	return -1
}

// splitItems splits the items of a list on each comma outside quotes. An
// item that starts with a quote ends its quoted text at the matching quote.
// A quote inside an unquoted item is text, as in Jazz's. A backslash inside
// double quotes hides the next byte, as in unquote.
func splitItems(items string) []string {
	var parts []string
	start, atStart := 0, true
	for i := 0; i < len(items); i++ {
		switch c := items[i]; {
		case c == ',':
			parts = append(parts, items[start:i])
			start, atStart = i+1, true
		case c == ' ' || c == '\t':
		case atStart && (c == '"' || c == '\''):
			for i++; i < len(items) && items[i] != c; i++ {
				if c == '"' && items[i] == '\\' {
					i++
				}
			}
			atStart = false
		default:
			atStart = false
		}
	}
	return append(parts, items[start:])
}

// parseStringSlice parses a comma-separated list of strings, optionally
// wrapped in square brackets (e.g. `["Music", "Jazz"]` or `Music, Jazz`).
// Each element is trimmed and unquoted like a single string value.
func parseStringSlice(val string) []string {
	val = listItems(val)
	parts := splitItems(val)
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = unquote(strings.TrimSpace(p))
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// parseEQ parses a TOML-style array like [0, 1.5, -2, ...] into 10 bands.
func parseEQ(val string) [10]float64 {
	var bands [10]float64
	val = listItems(val)
	parts := strings.Split(val, ",")
	for i, p := range parts {
		if i >= 10 {
			break
		}
		// parseFloat drops a comment after the last band of a list without
		// brackets, and it skips a NaN or infinite band.
		if v, ok := parseFloat(strings.TrimSpace(p)); ok {
			bands[i] = max(min(v, 12), -12)
		}
	}
	return bands
}
