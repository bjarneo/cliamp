package yandex

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestBuildStreamURL(t *testing.T) {
	tests := []struct {
		name string
		info downloadInfo
		full fullDownloadInfo
		want string
	}{
		{
			name: "signed mp3 url",
			info: downloadInfo{Codec: "mp3"},
			full: fullDownloadInfo{
				Host: "s130.music.yandex.ru",
				Path: "/download-info/123.mp3",
				Ts:   "1675244728",
				S:    "abc",
			},
			want: "https://s130.music.yandex.ru/get-mp3/ab06833a3f77afc4aa1357b420586d2d/1675244728/download-info/123.mp3",
		},
		{
			name: "signed aac url",
			info: downloadInfo{Codec: "aac"},
			full: fullDownloadInfo{
				Host: "s1.music.yandex.ru",
				Path: "/download-info/456.aac",
				Ts:   "1675244728",
				S:    "xyz",
			},
			want: "https://s1.music.yandex.ru/get-aac/" + md5Hex("XGRlBW9FXlekgbPrRHuSiA"+"download-info/456.aac"+"xyz") + "/1675244728/download-info/456.aac",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildStreamURL(tc.info, tc.full); got != tc.want {
				t.Errorf("buildStreamURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBestDownloadInfo(t *testing.T) {
	tests := []struct {
		name    string
		infos   []downloadInfo
		wantOK  bool
		wantBit int
	}{
		{
			name:   "empty list",
			infos:  nil,
			wantOK: false,
		},
		{
			name: "prefers highest bitrate mp3",
			infos: []downloadInfo{
				{Codec: "mp3", BitrateInKbps: 64, DownloadInfoURL: "https://dl/64"},
				{Codec: "mp3", BitrateInKbps: 320, DownloadInfoURL: "https://dl/320"},
				{Codec: "mp3", BitrateInKbps: 192, DownloadInfoURL: "https://dl/192"},
			},
			wantOK:  true,
			wantBit: 320,
		},
		{
			name: "prefers non-preview over preview",
			infos: []downloadInfo{
				{Codec: "mp3", BitrateInKbps: 320, Preview: true, DownloadInfoURL: "https://dl/preview"},
				{Codec: "mp3", BitrateInKbps: 128, DownloadInfoURL: "https://dl/full"},
			},
			wantOK:  true,
			wantBit: 128,
		},
		{
			name: "falls back to aac",
			infos: []downloadInfo{
				{Codec: "aac", BitrateInKbps: 192, DownloadInfoURL: "https://dl/aac"},
			},
			wantOK:  true,
			wantBit: 192,
		},
		{
			name: "skips entries without url",
			infos: []downloadInfo{
				{Codec: "mp3", BitrateInKbps: 320},
				{Codec: "aac", BitrateInKbps: 64, DownloadInfoURL: "https://dl/aac"},
			},
			wantOK:  true,
			wantBit: 64,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := bestDownloadInfo(tc.infos)
			if ok != tc.wantOK {
				t.Fatalf("bestDownloadInfo() ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if got.BitrateInKbps != tc.wantBit {
				t.Errorf("bitrate = %d, want %d", got.BitrateInKbps, tc.wantBit)
			}
			if got.DownloadInfoURL == "" {
				t.Error("selected info has empty DownloadInfoURL")
			}
		})
	}
}

func TestPlainID(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"12345:67890", "12345"},
		{"12345", "12345"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := plainID(tc.in); got != tc.want {
			t.Errorf("plainID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTrackConversion(t *testing.T) {
	p := New("token")
	remote := []track{
		{
			ID:         "1001",
			Title:      "Song",
			Version:    "Remix",
			Available:  true,
			DurationMs: 215500,
			Artists:    []artist{{ID: 1, Name: "Alpha"}, {Name: "Beta"}},
			Albums:     []album{{ID: 10, Title: "Album One", Year: 2020}},
		},
		{ID: "", Title: "no id"},
		{ID: "1002", Error: &apiError{Name: "not-found"}},
		{ID: "1003", Error: &apiError{Name: "track-not-available"}},
	}
	tracks := p.toPlaylistTracks(remote)
	if len(tracks) != 1 {
		t.Fatalf("got %d tracks, want 1", len(tracks))
	}
	got := tracks[0]
	if got.Path != TrackURIPrefix+"1001" {
		t.Errorf("Path = %q, want %q", got.Path, TrackURIPrefix+"1001")
	}
	if got.Title != "Song (Remix)" {
		t.Errorf("Title = %q, want %q", got.Title, "Song (Remix)")
	}
	if got.Artist != "Alpha, Beta" {
		t.Errorf("Artist = %q, want %q", got.Artist, "Alpha, Beta")
	}
	if got.Album != "Album One" {
		t.Errorf("Album = %q, want %q", got.Album, "Album One")
	}
	if got.Year != 2020 {
		t.Errorf("Year = %d, want 2020", got.Year)
	}
	if got.DurationSecs != 216 {
		t.Errorf("DurationSecs = %d, want 216", got.DurationSecs)
	}
	if got.Meta(provider.MetaYandexID) != "1001" {
		t.Errorf("Meta = %q, want %q", got.Meta(provider.MetaYandexID), "1001")
	}
	if !got.Stream {
		t.Error("URI track should be marked Stream")
	}
	if got.Unplayable {
		t.Error("URI track should not be marked Unplayable")
	}
}

func TestTrackKeys(t *testing.T) {
	ts := []track{
		{ID: "100", Albums: []album{{ID: 10}}},
		{ID: "200"},
		{ID: ""},
	}
	want := []string{"100:10", "200"}
	got := trackKeys(ts)
	if len(got) != len(want) {
		t.Fatalf("trackKeys() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("trackKeys()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResolveSourceRejects(t *testing.T) {
	p := New("token")
	if _, err := p.ResolveSource("other://1"); err == nil {
		t.Error("ResolveSource should reject foreign URIs")
	}
	if _, err := p.ResolveSource(TrackURIPrefix); err == nil {
		t.Error("ResolveSource should reject empty track ids")
	}
	if _, err := p.ResolveSource(TrackURIPrefix + "1/2"); err == nil {
		t.Error("ResolveSource should reject ids with path characters")
	}
}

func TestCanReportPlayback(t *testing.T) {
	p := New("token")
	yes := playlist.Track{ProviderMeta: map[string]string{provider.MetaYandexID: "42"}}
	no := playlist.Track{Title: "local"}
	if !p.CanReportPlayback(yes) {
		t.Error("CanReportPlayback(yandex track) = false, want true")
	}
	if p.CanReportPlayback(no) {
		t.Error("CanReportPlayback(foreign track) = true, want false")
	}
}

func TestTrackTitle(t *testing.T) {
	tests := []struct {
		name string
		in   track
		want string
	}{
		{"plain", track{Title: "Song"}, "Song"},
		{"with version", track{Title: "Song", Version: "Live"}, "Song (Live)"},
		{"version only", track{Version: "Live"}, " (Live)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := trackTitle(tc.in); got != tc.want {
				t.Errorf("trackTitle() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMillisToSeconds(t *testing.T) {
	tests := []struct {
		in   int
		want int
	}{
		{0, 0},
		{-5, 0},
		{1, 1},
		{999, 1},
		{1000, 1},
		{1001, 2},
	}
	for _, tc := range tests {
		if got := millisToSeconds(tc.in); got != tc.want {
			t.Errorf("millisToSeconds(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestConfigIsSet(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want bool
	}{
		{"disabled", Config{Token: "tok"}, false},
		{"enabled without token", Config{Enabled: true}, false},
		{"enabled with blank token", Config{Enabled: true, Token: "   "}, false},
		{"enabled with token", Config{Enabled: true, Token: "tok"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.IsSet(); got != tc.want {
				t.Errorf("IsSet() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReportPlaybackGuards(t *testing.T) {
	p := New("token")
	if err := p.report(playlist.Track{Title: "foreign"}, 10, rotorTrackStarted); err != nil {
		t.Errorf("report(foreign track) = %v, want nil", err)
	}
	yes := playlist.Track{ProviderMeta: map[string]string{provider.MetaYandexID: "42"}}
	if err := p.report(yes, 10, rotorTrackFinished); err != nil {
		t.Errorf("report before account load = %v, want nil", err)
	}
}

func TestAPIErrorUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		data     string
		wantName string
	}{
		{"string error", `"track-not-available"`, "track-not-available"},
		{"object error", `{"name":"not-found","message":"gone"}`, "not-found"},
		{"null", `null`, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var e apiError
			if err := json.Unmarshal([]byte(tc.data), &e); err != nil {
				t.Fatalf("Unmarshal(%s) error = %v", tc.data, err)
			}
			if e.Name != tc.wantName {
				t.Errorf("Name = %q, want %q", e.Name, tc.wantName)
			}
		})
	}
}
