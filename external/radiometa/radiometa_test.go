package radiometa

import (
	"encoding/json"
	"testing"
	"time"
)

func TestResolverMatching(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		wantOK    bool
		wantEvery time.Duration
	}{
		{"nts relay", "http://stream-relay-geo.ntslive.net/stream", true, 30 * time.Second},
		{"nts radiomast", "https://streams.radiomast.io/nts1", true, 30 * time.Second},
		{"fip aac", "http://icecast.radiofrance.fr/fip-hifi.aac", true, 15 * time.Second},
		{"fip jazz", "http://icecast.radiofrance.fr/fipjazz-hifi.aac", true, 15 * time.Second},
		{"kexp aac", "https://kexp.streamguys1.com/kexp160.aac", false, 0},
		{"local file", "/home/user/song.mp3", false, 0},
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
