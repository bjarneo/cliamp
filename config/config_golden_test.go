package config

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// loadConfigText writes data as config.toml in a temporary HOME and loads it.
func loadConfigText(t *testing.T, data string) Config {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(os.Getenv("HOME"), ".config", "cliamp", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return cfg
}

func readExampleConfig(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "config.toml.example"))
	if err != nil {
		t.Fatalf("read config.toml.example: %v", err)
	}
	// A Windows checkout can turn the line ends into CRLF.
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

// exampleSetting matches a commented key or section header in the example.
var exampleSetting = regexp.MustCompile(`(?m)^# (\[[a-z]+\]|[a-z_]+ *=.*)$`)

// uncommentExample turns every commented key and section header of the
// example into a live line, the way a user turns a setting on.
func uncommentExample(s string) string {
	return exampleSetting.ReplaceAllString(s, "$1")
}

// goldenSections sets every key of every provider section. The unquoted
// values that contain # must reach the Config whole.
const goldenSections = `
[navidrome]
url = "https://music.example.com"
user = alice
password = pa#ss word
browse_sort = alphabeticalByArtist
format = "raw"
scrobble = false

[lyrion]
url = "http://nas.local:9000"
user = 'bob'
password = "lyr#ion"
show_unplayable = true

[spotify]
client_id = "spotify-client"
bitrate = 160

[qobuz]
quality = 27

[tidal]
client_id = "tidal-id"
client_secret = "tidal-secret"
quality = "hires"

[youtube]
cookies_from = " firefox "
client_id = "yt-id"
client_secret = "${CLIAMP_GOLDEN_YT_SECRET}"
expand_playlist = false

[plex]
url = "http://plex.local:32400"
token = plex#token
libraries = ["Music", "Jazz"]

[jellyfin]
url = https://jelly.example.com/web/#/home
token = "jelly-token"
user = "jelly"
password = "jelly-pass"
user_id = "jelly-uid"

[emby]
url = "https://emby.example.com"
token = "emby-token"
user = "emby"
password = "emby#pass"
user_id = "emby-uid"

[audiobookshelf]
url = "https://abs.example.com"
token = "abs-token"
user = "listener"
password = "abs-pass"
libraries = Audiobooks, Podcasts

[radio]
country = " NO "

[podcast]
country = "no"

[soundcloud]
enabled = true
user = "sc-user"
cookies_from = "chrome"

[mixcloud]
enabled = true
username = "mc-user"
access_token = mc#token
cookies_from = "brave"
styles = ["ambient", "deep-house"]
max_items = 50

[netease]
enabled = true
cookies_from = "chrome:Profile 1"
user_id = "42"

[yandex]
enabled = true
token = "y0_token"

[plugins]
disabled = webhook, discord-rpc

[plugins.lastfm]
api_key = abc#123
callback = https://example.com/cb#frag
note = "quoted # value"
`

// goldenTopLevel sets every top-level key to a value other than its default.
const goldenTopLevel = `
volume = -12.5
volume_min = -60
vis_volume_linked = false
vis_rows = 12
repeat = 'ALL'
shuffle = true
mono = true
auto_play = true
seek_large_step_sec = 45
lyrics_offset_ms = -250
eq = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]
eq_preset = "Custom"
theme = "Tokyo Night"
provider = "Jellyfin"
visualizer = "Wave"
sample_rate = 48000
buffer_ms = 500
resample_quality = 2
bit_depth = 32
speed = 1.25
simplified = true
hide_help_bar = true
hide_settings_pane = true
show_metadata = true
expanded = true
audio_device = "USB DAC #2"
initial_directory = ~/Music#Mixes
padding_horizontal = 5
padding_vertical = 2
log_level = "DEBUG"
low_power = false
`

// goldenListComments puts a # comment after each list key. A # inside a
// quoted item stays in the item. A comment after an unquoted last item stays
// part of that item, as for any unquoted string.
const goldenListComments = `
eq = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]   # bass up

[plex]
url = "http://plex.local:32400"
token = "plex-token"
libraries = ["Music", "Jazz # Blues"] # two libraries

[mixcloud]
enabled = true
styles = ['ambient', "deep-house"]	# tab before the comment

[audiobookshelf]
url = "https://abs.example.com"
token = "abs-token"
libraries = Audiobooks, Podcasts # unquoted
`

// TestLoadGolden loads whole config files and compares every Config field,
// so a parser change cannot move a value without a test failure.
func TestLoadGolden(t *testing.T) {
	t.Setenv("CLIAMP_GOLDEN_YT_SECRET", "yt-secret")
	t.Setenv("MIXCLOUD_ACCESS_TOKEN", "mc-env-token")
	example := readExampleConfig(t)
	f, tr := false, true

	exampleWant := defaultConfig()
	exampleWant.EQPreset = "Flat"

	sectionsWant := exampleWant
	sectionsWant.Navidrome = NavidromeConfig{
		URL:              "https://music.example.com",
		User:             "alice",
		Password:         "pa#ss word",
		Format:           "raw",
		BrowseSort:       "alphabeticalByArtist",
		ScrobbleDisabled: true,
	}
	sectionsWant.Lyrion = LyrionConfig{URL: "http://nas.local:9000", User: "bob", Password: "lyr#ion", ShowUnplayable: true}
	sectionsWant.Spotify = SpotifyConfig{Enabled: true, ClientID: "spotify-client", Bitrate: 160}
	sectionsWant.Qobuz = QobuzConfig{Enabled: true, Quality: 27}
	sectionsWant.Tidal = TidalConfig{Enabled: true, ClientID: "tidal-id", ClientSecret: "tidal-secret", Quality: "hires"}
	sectionsWant.YouTubeMusic = YouTubeMusicConfig{
		Enabled:        true,
		ClientID:       "yt-id",
		ClientSecret:   "yt-secret",
		CookiesFrom:    "firefox",
		ExpandPlaylist: &f,
	}
	sectionsWant.Plex = PlexConfig{URL: "http://plex.local:32400", Token: "plex#token", Libraries: []string{"Music", "Jazz"}}
	sectionsWant.Jellyfin = JellyfinConfig{
		URL:      "https://jelly.example.com/web/#/home",
		Token:    "jelly-token",
		User:     "jelly",
		Password: "jelly-pass",
		UserID:   "jelly-uid",
	}
	sectionsWant.Emby = EmbyConfig{
		URL:      "https://emby.example.com",
		Token:    "emby-token",
		User:     "emby",
		Password: "emby#pass",
		UserID:   "emby-uid",
	}
	sectionsWant.Audiobookshelf = AudiobookshelfConfig{
		URL:       "https://abs.example.com",
		Token:     "abs-token",
		User:      "listener",
		Password:  "abs-pass",
		Libraries: []string{"Audiobooks", "Podcasts"},
	}
	sectionsWant.Radio = RadioConfig{Country: "NO"}
	sectionsWant.Podcast = PodcastConfig{Country: "no"}
	sectionsWant.SoundCloud = SoundCloudConfig{Enabled: true, User: "sc-user", CookiesFrom: "chrome"}
	sectionsWant.Mixcloud = MixcloudConfig{
		Enabled:     true,
		Username:    "mc-user",
		AccessToken: "mc#token",
		CookiesFrom: "brave",
		Styles:      []string{"ambient", "deep-house"},
		StylesSet:   true,
		MaxItems:    50,
	}
	sectionsWant.NetEase = NetEaseConfig{Enabled: true, CookiesFrom: "chrome:Profile 1", UserID: "42"}
	sectionsWant.Yandex = YandexConfig{Enabled: true, Token: "y0_token"}
	sectionsWant.Plugins = map[string]map[string]string{
		"": {"disabled": "webhook, discord-rpc"},
		"lastfm": {
			"api_key":  "abc#123",
			"callback": "https://example.com/cb#frag",
			"note":     "quoted # value",
		},
	}

	topWant := defaultConfig()
	topWant.Volume = -12.5
	topWant.VolumeMin = -60
	topWant.VisVolumeLinked = false
	topWant.VisRows = 12
	topWant.Repeat = "all"
	topWant.Shuffle = true
	topWant.Mono = true
	topWant.AutoPlay = true
	topWant.SeekStepLarge = 45
	topWant.LyricsOffsetMs = -250
	topWant.EQ = [10]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	topWant.EQPreset = "Custom"
	topWant.Theme = "Tokyo Night"
	topWant.Provider = "jellyfin"
	topWant.Visualizer = "Wave"
	topWant.SampleRate = 48000
	topWant.BufferMs = 500
	topWant.ResampleQuality = 2
	topWant.BitDepth = 32
	topWant.Speed = 1.25
	topWant.Simplified = true
	topWant.HideHelpBar = true
	topWant.HideSettingsPane = true
	topWant.ShowMetadata = true
	topWant.Expanded = true
	topWant.AudioDevice = "USB DAC #2"
	topWant.InitialDirectory = "~/Music#Mixes"
	topWant.PaddingH = 5
	topWant.PaddingV = 2
	topWant.LogLevel = "debug"

	// Every setting of the example turned on. The example puts # comments
	// after sample_rate, the other audio numbers and 3 cookies_from strings.
	allWant := exampleWant
	allWant.VisRows = 7
	allWant.InitialDirectory = "~/Music"
	allWant.AudioDevice = "alsa_output.usb-FiiO_K5_Pro-00.analog-stereo"
	allWant.Provider = "cliamp"
	allWant.Simplified = true
	allWant.HideHelpBar = true
	allWant.HideSettingsPane = true
	allWant.Expanded = true
	allWant.Theme = "Tokyo Night"
	allWant.Visualizer = "Bars"
	allWant.Podcast = PodcastConfig{Country: "no"}
	allWant.Spotify = SpotifyConfig{Enabled: true, ClientID: "your-spotify-app-client-id", Bitrate: 320}
	allWant.Qobuz = QobuzConfig{Enabled: true, Quality: 6}
	allWant.Tidal = TidalConfig{Enabled: true, Quality: "lossless"}
	allWant.Navidrome = NavidromeConfig{
		URL:        "https://music.example.com",
		User:       "alice",
		Password:   "secret",
		Format:     "raw",
		BrowseSort: "alphabeticalByName",
	}
	allWant.Lyrion = LyrionConfig{URL: "http://nas.local:9000", User: "alice", Password: "secret", ShowUnplayable: true}
	allWant.SoundCloud = SoundCloudConfig{Enabled: true, User: "yourname", CookiesFrom: "firefox"}
	allWant.Mixcloud = MixcloudConfig{
		Enabled:     true,
		Username:    "yourname",
		AccessToken: "mc-env-token",
		CookiesFrom: "firefox",
		Styles:      []string{"ambient", "deep-house", "house", "jazz", "techno"},
		StylesSet:   true,
		MaxItems:    100,
	}
	allWant.NetEase = NetEaseConfig{Enabled: true, CookiesFrom: "chrome", UserID: "optional-account-user-id"}
	allWant.Yandex = YandexConfig{Enabled: true, Token: "y0_YourPersonalOAuthToken"}
	allWant.Plex = PlexConfig{URL: "http://192.168.1.10:32400", Token: "xxxxxxxxxxxxxxxxxxxx", Libraries: []string{"Music", "Jazz"}}
	allWant.Jellyfin = JellyfinConfig{
		URL:      "https://jellyfin.example.com",
		Token:    "optional-api-token",
		User:     "alice",
		Password: "secret",
		UserID:   "optional-user-id",
	}
	allWant.YouTubeMusic = YouTubeMusicConfig{
		Enabled:        true,
		ClientID:       "your-google-oauth-client-id",
		ClientSecret:   "your-google-oauth-client-secret",
		CookiesFrom:    "chrome",
		ExpandPlaylist: &tr,
	}
	allWant.Emby = EmbyConfig{
		URL:      "https://emby.example.com",
		Token:    "optional-api-key",
		User:     "alice",
		Password: "secret",
		UserID:   "optional-user-id",
	}
	allWant.Audiobookshelf = AudiobookshelfConfig{
		URL:       "https://abs.example.com",
		Token:     "your-api-key",
		User:      "listener",
		Password:  "secret",
		Libraries: []string{"Audiobooks", "Podcasts"},
	}

	listsWant := defaultConfig()
	listsWant.EQ = [10]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	listsWant.Plex = PlexConfig{URL: "http://plex.local:32400", Token: "plex-token", Libraries: []string{"Music", "Jazz # Blues"}}
	listsWant.Mixcloud = MixcloudConfig{Enabled: true, Styles: []string{"ambient", "deep-house"}, StylesSet: true}
	listsWant.Audiobookshelf = AudiobookshelfConfig{
		URL:       "https://abs.example.com",
		Token:     "abs-token",
		Libraries: []string{"Audiobooks", "Podcasts # unquoted"},
	}

	tests := []struct {
		name string
		data string
		want Config
	}{
		{"example as shipped", example, exampleWant},
		{"example with every setting turned on", uncommentExample(example), allWant},
		{"example plus every provider section", example + goldenSections, sectionsWant},
		{"every top-level key", goldenTopLevel, topWant},
		{"comments after lists", goldenListComments, listsWant},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := loadConfigText(t, tt.data)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Load() mismatch\n got: %+v\nwant: %+v", got, tt.want)
			}
		})
	}
}
