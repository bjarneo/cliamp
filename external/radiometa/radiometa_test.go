package radiometa

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/httpclient"
)

func TestResolverMatching(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		wantOK    bool
		wantEvery time.Duration
	}{
		{"nts relay", "http://stream-relay-geo.ntslive.net/stream", true, 30 * time.Second},
		{"nts relay stream2 uppercase", "HTTPS://STREAM-RELAY-GEO.NTSLIVE.NET/STREAM2?client=test", true, 30 * time.Second},
		{"nts radiomast", "https://streams.radiomast.io/nts1", true, 30 * time.Second},
		{"nts radiomast ch2", "https://streams.radiomast.io/nts2", true, 30 * time.Second},
		{"fip aac", "http://icecast.radiofrance.fr/fip-hifi.aac", true, 15 * time.Second},
		{"fip jazz", "http://icecast.radiofrance.fr/fipjazz-hifi.aac", true, 15 * time.Second},
		{"fip hls sub-channel", "https://stream.radiofrance.fr/fipjazz/fipjazz_hifi.m3u8", true, 15 * time.Second},
		{"nts path on unrelated host", "https://example.com/nts1", false, 0},
		{"nts spoofed host suffix", "https://notntslive.net/stream", false, 0},
		{"nts radiomast non-exact segment", "https://streams.radiomast.io/nts10", false, 0},
		{"nts host in query string", "https://example.com/live?ref=ntslive.net/stream", false, 0},
		{"fip spoofed host suffix", "https://notradiofrance.fr/fip-hifi.aac", false, 0},
		{"fip host in path", "https://example.com/radiofrance.fr/fip-hifi.aac", false, 0},
		{"fip keyword only in query", "http://icecast.radiofrance.fr/franceinfo-hifi.aac?ref=fip", false, 0},
		{"fip keyword only in host", "https://fip.radiofrance.fr/franceinter-hifi.aac", false, 0},
		{"fip non-exact segment", "http://icecast.radiofrance.fr/fipfake-hifi.aac", false, 0},
		{"kexp aac", "https://kexp.streamguys1.com/kexp160.aac", false, 0},
		{"local file", "/home/user/song.mp3", false, 0},
		{"local nts path", "/music/nts1", false, 0},
		{"random http", "https://example.com/stream.mp3", false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fetch, every, ok := Resolver(tt.url)
			if ok != tt.wantOK {
				t.Fatalf("Resolver(%q) ok = %v, want %v", tt.url, ok, tt.wantOK)
			}
			if ok && fetch == nil {
				t.Errorf("Resolver(%q) returned ok but nil fetch", tt.url)
			}
			if every != tt.wantEvery {
				t.Errorf("Resolver(%q) interval = %v, want %v", tt.url, every, tt.wantEvery)
			}
		})
	}
}

func TestNTSChannel(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"http://stream-relay-geo.ntslive.net/stream", "1"},
		{"http://stream-relay-geo.ntslive.net/stream2", "2"},
		{"https://streams.radiomast.io/nts1", "1"},
		{"https://streams.radiomast.io/nts2", "2"},
	}
	for _, tt := range tests {
		if got := ntsChannel(tt.url); got != tt.want {
			t.Errorf("ntsChannel(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestFIPStationID(t *testing.T) {
	tests := []struct {
		url  string
		want int
	}{
		{"http://icecast.radiofrance.fr/fip-hifi.aac", 7},
		{"http://icecast.radiofrance.fr/fip-midfi.mp3", 7},
		{"http://icecast.radiofrance.fr/fipjazz-hifi.aac", 65},
		{"http://icecast.radiofrance.fr/fiprock-hifi.aac", 64},
		{"http://icecast.radiofrance.fr/fipgroove-hifi.aac", 66},
		{"http://icecast.radiofrance.fr/fipreggae-hifi.aac", 71},
		{"http://icecast.radiofrance.fr/fipelectro-hifi.aac", 74},
		{"http://icecast.radiofrance.fr/fipmetal-hifi.aac", 77},
		{"http://icecast.radiofrance.fr/fipnouveautes-hifi.aac", 70},
		{"http://icecast.radiofrance.fr/fipworld-hifi.aac", 69},
	}
	for _, tt := range tests {
		if got := fipStationID(tt.url); got != tt.want {
			t.Errorf("fipStationID(%q) = %d, want %d", tt.url, got, tt.want)
		}
	}
}

func TestParseNTS(t *testing.T) {
	const payload = `{"results":[
		{"channel_name":"1","now":{"broadcast_title":"CHARISSE C","embeds":{"details":{"name":"Charisse C"}}}},
		{"channel_name":"2","now":{"broadcast_title":"OLIVIA O.","embeds":{"details":{"name":""}}}}
	]}`
	var live ntsLive
	if err := json.Unmarshal([]byte(payload), &live); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got := parseNTS(&live, "1"); got != "Charisse C" {
		t.Errorf("channel 1 = %q, want %q (prefer details name)", got, "Charisse C")
	}
	if got := parseNTS(&live, "2"); got != "OLIVIA O." {
		t.Errorf("channel 2 = %q, want %q (fall back to broadcast_title)", got, "OLIVIA O.")
	}
	if got := parseNTS(&live, "3"); got != "" {
		t.Errorf("unknown channel = %q, want empty", got)
	}
}

func TestParseFIP(t *testing.T) {
	const payload = `{"steps":{
		"a":{"title":"Past Song","authors":"Old Artist","start":100,"end":200},
		"b":{"title":"Obecanje","authors":"Boban Markovic Orkestar","start":200,"end":300},
		"c":{"title":"Future Song","authors":"Next Artist","start":300,"end":400}
	}}`
	var live fipLive
	if err := json.Unmarshal([]byte(payload), &live); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got := parseFIP(&live, 250); got != "Boban Markovic Orkestar - Obecanje" {
		t.Errorf("now=250 = %q, want in-window step b", got)
	}
	if got := parseFIP(&live, 350); got != "Next Artist - Future Song" {
		t.Errorf("now=350 = %q, want in-window step c", got)
	}
	// now=500 is past every window: fall back to the most recent started step (c).
	if got := parseFIP(&live, 500); got != "Next Artist - Future Song" {
		t.Errorf("now=500 = %q, want most-recent-started fallback", got)
	}
	// now=50 is before any step: nothing has started, so empty.
	if got := parseFIP(&live, 50); got != "" {
		t.Errorf("now=50 = %q, want empty (nothing started)", got)
	}
}

func TestParseFIPOverlappingStepsDeterministic(t *testing.T) {
	tests := []struct {
		name  string
		steps map[string]fipStep
		now   int64
		want  string
	}{
		{
			name: "latest Start wins among overlapping active windows",
			steps: map[string]fipStep{
				"show":  {Title: "Evening Show", Authors: "Host", Start: 100, End: 500},
				"mid":   {Title: "Middle Track", Authors: "Artist A", Start: 150, End: 400},
				"track": {Title: "Current Track", Authors: "Artist B", Start: 200, End: 300},
			},
			now:  250,
			want: "Artist B - Current Track",
		},
		{
			name: "earlier End breaks equal Start ties deterministically",
			steps: map[string]fipStep{
				"wide":   {Title: "Wide Show", Authors: "Host", Start: 200, End: 500},
				"narrow": {Title: "Exact Track", Authors: "Artist C", Start: 200, End: 300},
			},
			now:  250,
			want: "Artist C - Exact Track",
		},
		{
			name: "step starting at zero is selected",
			steps: map[string]fipStep{
				"zero": {Title: "Intro", Authors: "Artist Z", Start: 0, End: 10},
			},
			now:  0,
			want: "Artist Z - Intro",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			live := &fipLive{Steps: tt.steps}
			for range 64 {
				if got := parseFIP(live, tt.now); got != tt.want {
					t.Fatalf("parseFIP(now=%d) = %q, want %q", tt.now, got, tt.want)
				}
			}
		})
	}
}

func TestFormatTrack(t *testing.T) {
	tests := []struct {
		artist, title, want string
	}{
		{"Daft Punk", "Aerodynamic", "Daft Punk - Aerodynamic"},
		{"", "Just A Title", "Just A Title"},
		{"Just An Artist", "", "Just An Artist"},
		{"  Spaced  ", "  Out  ", "Spaced - Out"},
		{"", "", ""},
	}
	for _, tt := range tests {
		if got := formatTrack(tt.artist, tt.title); got != tt.want {
			t.Errorf("formatTrack(%q, %q) = %q, want %q", tt.artist, tt.title, got, tt.want)
		}
	}
}

func TestGetJSON(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		want    string
		wantErr bool
	}{
		{name: "decodes a 200 body", status: http.StatusOK, body: `{"title":"Song"}`, want: "Song"},
		{name: "fails on a non-200 status", status: http.StatusServiceUnavailable, body: `{}`, wantErr: true},
		{name: "fails on a body over the limit", status: http.StatusOK, body: `{"title":"` + strings.Repeat("x", maxMetaBody) + `"}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if ua := r.UserAgent(); ua != httpclient.UserAgent {
					t.Errorf("User-Agent = %q, want %q", ua, httpclient.UserAgent)
				}
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			var got struct {
				Title string `json:"title"`
			}
			err := getJSON(context.Background(), srv.URL, &got)
			if (err != nil) != tt.wantErr {
				t.Fatalf("getJSON() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.status == http.StatusOK && tt.wantErr && !errors.Is(err, httpclient.ErrTooLarge) {
				t.Errorf("getJSON() error = %v, want httpclient.ErrTooLarge", err)
			}
			if got.Title != tt.want {
				t.Errorf("Title = %q, want %q", got.Title, tt.want)
			}
		})
	}
}
