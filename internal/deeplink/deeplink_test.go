package deeplink

import (
	"errors"
	"strings"
	"testing"
)

func TestParseValid(t *testing.T) {
	tests := []struct {
		name string
		uri  string
		want Action
	}{
		{
			name: "play url",
			uri:  "cliamp://play?url=https://example.com/stream.mp3",
			want: Action{Verb: Play, Target: TargetURL, URL: "https://example.com/stream.mp3"},
		},
		{
			name: "plain http is allowed",
			uri:  "cliamp://play?url=http://example.com/stream.mp3",
			want: Action{Verb: Play, Target: TargetURL, URL: "http://example.com/stream.mp3"},
		},
		{
			name: "lan address is allowed",
			uri:  "cliamp://play?url=http://192.168.1.10:4533/rest/stream",
			want: Action{Verb: Play, Target: TargetURL, URL: "http://192.168.1.10:4533/rest/stream"},
		},
		{
			name: "loopback is allowed",
			uri:  "cliamp://queue?url=http://localhost:4533/stream.mp3",
			want: Action{Verb: Queue, Target: TargetURL, URL: "http://localhost:4533/stream.mp3"},
		},
		{
			name: "queue url",
			uri:  "cliamp://queue?url=https://example.com/a.mp3",
			want: Action{Verb: Queue, Target: TargetURL, URL: "https://example.com/a.mp3"},
		},
		{
			name: "provider album",
			uri:  "cliamp://play?provider=navidrome&album=a1b2c3",
			want: Action{Verb: Play, Target: TargetAlbum, Provider: "navidrome", Album: "a1b2c3"},
		},
		{
			name: "provider playlist",
			uri:  "cliamp://queue?provider=jellyfin&playlist=xyz789",
			want: Action{Verb: Queue, Target: TargetPlaylist, Provider: "jellyfin", Playlist: "xyz789"},
		},
		{
			name: "provider search with encoded space",
			uri:  "cliamp://play?provider=ytmusic&q=aphex%20twin",
			want: Action{Verb: Play, Target: TargetSearch, Provider: "ytmusic", Query: "aphex twin"},
		},
		{
			name: "provider search with plus space",
			uri:  "cliamp://play?provider=ytmusic&q=aphex+twin",
			want: Action{Verb: Play, Target: TargetSearch, Provider: "ytmusic", Query: "aphex twin"},
		},
		{
			name: "scheme is case insensitive",
			uri:  "CLIAMP://play?url=https://example.com/a.mp3",
			want: Action{Verb: Play, Target: TargetURL, URL: "https://example.com/a.mp3"},
		},
		{
			name: "verb is case insensitive",
			uri:  "cliamp://PLAY?url=https://example.com/a.mp3",
			want: Action{Verb: Play, Target: TargetURL, URL: "https://example.com/a.mp3"},
		},
		{
			name: "trailing slash after verb is accepted",
			uri:  "cliamp://play/?url=https://example.com/a.mp3",
			want: Action{Verb: Play, Target: TargetURL, URL: "https://example.com/a.mp3"},
		},
		{
			name: "query preserves url path and query",
			uri:  "cliamp://play?url=https%3A%2F%2Fex.com%2Fs.mp3%3Ft%3D30",
			want: Action{Verb: Play, Target: TargetURL, URL: "https://ex.com/s.mp3?t=30"},
		},
		{
			name: "provider name may contain digits and dashes",
			uri:  "cliamp://play?provider=sub-sonic2&album=x",
			want: Action{Verb: Play, Target: TargetAlbum, Provider: "sub-sonic2", Album: "x"},
		},
		{
			// url.Parse lowercases the scheme it reports, but resolve compares
			// a literal "https://" prefix. Parse must hand back a string both
			// layers agree on.
			name: "uppercase scheme is normalized",
			uri:  "cliamp://play?url=HTTPS%3A%2F%2Fexample.com%2Fa.mp3",
			want: Action{Verb: Play, Target: TargetURL, URL: "https://example.com/a.mp3"},
		},
		{
			name: "mixed case scheme is normalized",
			uri:  "cliamp://play?url=HtTpS%3A%2F%2Fexample.com%2Fa.mp3",
			want: Action{Verb: Play, Target: TargetURL, URL: "https://example.com/a.mp3"},
		},
		{
			// Only the scheme is lowercased; a path is case-sensitive.
			name: "path case is preserved",
			uri:  "cliamp://play?url=HTTPS%3A%2F%2Fexample.com%2FMixedCase.mp3",
			want: Action{Verb: Play, Target: TargetURL, URL: "https://example.com/MixedCase.mp3"},
		},
		{
			name: "surrounding whitespace is tolerated",
			uri:  "  cliamp://play?url=https://example.com/a.mp3  ",
			want: Action{Verb: Play, Target: TargetURL, URL: "https://example.com/a.mp3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.uri)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tt.uri, err)
			}
			if got != tt.want {
				t.Errorf("Parse(%q)\n got %+v\nwant %+v", tt.uri, got, tt.want)
			}
		})
	}
}

// TestParseRejects is the security surface of the package. Every case here is
// something a hostile web page could put behind a link.
func TestParseRejects(t *testing.T) {
	tests := []struct {
		name string
		uri  string
		// wantContains is a fragment of the expected error, so a rejection for
		// the wrong reason fails the test rather than passing quietly.
		wantContains string
	}{
		// Scheme gating.
		{"empty", "", "not a cliamp"},
		{"blank", "   ", "not a cliamp"},
		{"http scheme", "http://example.com", "not a cliamp"},
		{"spotify scheme", "spotify:track:abc", "not a cliamp"},
		{"scheme prefix only", "cliampx://play?url=https://e.com/a.mp3", "not a cliamp"},

		// Verb gating.
		{"missing verb", "cliamp://?url=https://e.com/a.mp3", "missing an action"},
		{"unknown verb", "cliamp://destroy?url=https://e.com/a.mp3", "unknown cliamp action"},
		{"dropped open verb", "cliamp://open?url=https://e.com/a.mp3", "unknown cliamp action"},
		{"plugin verb is not reachable", "cliamp://plugin.call?q=x", "unknown cliamp action"},
		{"save verb is not reachable", "cliamp://save?url=https://e.com/a.mp3", "unknown cliamp action"},
		{"path segment after verb", "cliamp://play/extra?url=https://e.com/a.mp3", "takes no path segment"},

		// Target arity.
		{"no target", "cliamp://play", "names no target"},
		{"empty query", "cliamp://play?", "names no target"},
		{"two targets", "cliamp://play?url=https://e.com/a.mp3&q=x", "expected exactly one"},
		{"album and playlist", "cliamp://play?provider=nd&album=a&playlist=b", "expected exactly one"},
		{"url with provider", "cliamp://play?url=https://e.com/a.mp3&provider=nd", "cannot combine"},
		{"provider target without provider", "cliamp://play?album=abc", "missing the provider"},
		{"repeated param", "cliamp://play?url=https://a.com/x.mp3&url=https://b.com/y.mp3", "is repeated"},

		// Unknown parameters must not be ignored.
		{"unknown param", "cliamp://play?url=https://e.com/a.mp3&exec=whoami", "unknown cliamp URI parameter"},
		{"file param is not supported", "cliamp://play?file=/etc/passwd", "unknown cliamp URI parameter"},

		// URL scheme allowlist. These are the paths to an external process.
		{"ssh url", "cliamp://play?url=ssh://host/etc/passwd", "not allowed"},
		{"file url", "cliamp://play?url=file:///etc/passwd", "not allowed"},
		{"ftp url", "cliamp://play?url=ftp://e.com/a.mp3", "not allowed"},
		{"data url", "cliamp://play?url=data:audio/mp3,AAAA", "not allowed"},
		{"semicolon in query", "cliamp://play?url=data:audio/mp3;base64,AAAA", "invalid cliamp URI query"},
		{"javascript url", "cliamp://play?url=javascript:alert(1)", "not allowed"},
		{"nested cliamp url", "cliamp://play?url=cliamp://play", "not allowed"},
		{"schemeless url", "cliamp://play?url=example.com/a.mp3", "no scheme"},
		{"url without host", "cliamp://play?url=https:///a.mp3", "no host"},
		{"url with credentials", "cliamp://play?url=https://user:pw@e.com/a.mp3", "credentials"},

		// Argument injection into yt-dlp / ssh.
		{"exec flag as url", "cliamp://play?url=--exec%3Dtouch%20%2Ftmp%2Fpwned", "command-line flag"},
		{"short flag as url", "cliamp://play?url=-o%2Ftmp%2Fout", "command-line flag"},
		{"config flag as url", "cliamp://play?url=--config-location%3D%2Ftmp%2Fevil", "command-line flag"},
		{"flag as search query", "cliamp://play?provider=yt&q=--exec%3Dwhoami", "command-line flag"},
		{"flag as album id", "cliamp://play?provider=nd&album=--exec%3Dwhoami", "command-line flag"},

		// Provider charset.
		{"provider with slash", "cliamp://play?provider=..%2F..%2Fetc&album=x", "disallowed characters"},
		{"provider with flag shape", "cliamp://play?provider=-nd&album=x", "must start with a letter"},
		{"provider uppercase", "cliamp://play?provider=Navidrome&album=x", "disallowed characters"},
		{"provider starting with digit", "cliamp://play?provider=1nd&album=x", "must start with a letter"},

		// Control characters.
		{"newline in query", "cliamp://play?provider=yt&q=a%0Ab", "control character"},
		{"null in album", "cliamp://play?provider=nd&album=a%00b", "control character"},
		{"escape in url", "cliamp://play?url=https://e.com/%1B%5B31m", "control character"},

		// Malformed input.
		{"bad percent encoding in query", "cliamp://play?url=%zz", "invalid cliamp URI query"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.uri)
			if err == nil {
				t.Fatalf("Parse(%q) succeeded with %+v, want error", tt.uri, got)
			}
			if !strings.Contains(err.Error(), tt.wantContains) {
				t.Errorf("Parse(%q) error = %q, want it to contain %q", tt.uri, err, tt.wantContains)
			}
			if got != (Action{}) {
				t.Errorf("Parse(%q) returned %+v alongside an error, want the zero Action", tt.uri, got)
			}
		})
	}
}

// TestParseNotDeepLinkIsDistinguishable confirms callers can tell "this URI
// belongs to another handler" from "this cliamp URI is malformed".
func TestParseNotDeepLinkIsDistinguishable(t *testing.T) {
	if _, err := Parse("https://example.com"); !errors.Is(err, ErrNotDeepLink) {
		t.Errorf("Parse(https URI) error = %v, want ErrNotDeepLink", err)
	}
	if _, err := Parse("cliamp://play"); errors.Is(err, ErrNotDeepLink) {
		t.Error("a malformed cliamp URI must not report ErrNotDeepLink")
	}
}

func TestParseLengthLimits(t *testing.T) {
	long := strings.Repeat("a", maxURILen+1)
	if _, err := Parse("cliamp://play?url=https://e.com/" + long); err == nil {
		t.Error("over-long URI should be rejected")
	}

	longURL := "https://e.com/" + strings.Repeat("a", maxURLLen)
	if _, err := Parse("cliamp://play?url=" + longURL); err == nil {
		t.Error("over-long url should be rejected")
	}

	longQuery := strings.Repeat("a", maxIdentLen+1)
	if _, err := Parse("cliamp://play?provider=yt&q=" + longQuery); err == nil {
		t.Error("over-long query should be rejected")
	}

	longProvider := strings.Repeat("a", maxProviderLen+1)
	if _, err := Parse("cliamp://play?provider=" + longProvider + "&album=x"); err == nil {
		t.Error("over-long provider should be rejected")
	}
}

func TestVerbString(t *testing.T) {
	tests := []struct {
		verb Verb
		want string
	}{
		{Play, "play"},
		{Queue, "queue"},
		{Verb(0), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.verb.String(); got != tt.want {
			t.Errorf("Verb(%d).String() = %q, want %q", tt.verb, got, tt.want)
		}
	}
}

// TestParseRoundTripsVerbs checks every verb reaches the parser, so adding a
// verb without a test is visible.
func TestParseRoundTripsVerbs(t *testing.T) {
	for _, verb := range []Verb{Play, Queue} {
		uri := "cliamp://" + verb.String() + "?url=https://example.com/a.mp3"
		got, err := Parse(uri)
		if err != nil {
			t.Fatalf("Parse(%q) returned error: %v", uri, err)
		}
		if got.Verb != verb {
			t.Errorf("Parse(%q).Verb = %v, want %v", uri, got.Verb, verb)
		}
	}
}

// TestAllowsTrackPath covers the second gate, applied to a path a provider
// resolved rather than one the URI carried. The radio directory is public, so
// a search result is not automatically safe to hand to playback.
func TestAllowsTrackPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"https stream", "https://stream.example/live.mp3", true},
		{"http stream", "http://stream.example/live.mp3", true},
		{"lan stream", "http://192.168.1.10:4533/rest/stream", true},
		{"surrounding whitespace", "  https://stream.example/live.mp3  ", true},
		{"uppercase scheme", "HTTPS://stream.example/live.mp3", true},
		// Provider-native URIs reach a registered streamer in
		// player.pipeline before openSource is consulted, so refusing them
		// would break a legitimate search link.
		{"spotify uri", "spotify:track:abc123", true},

		// player.openSource routes this to exec.Command("ssh", ...) against
		// the host in the path. This is the case the gate exists for.
		{"ssh transport", "ssh://attacker.example/x", false},
		{"ssh uppercase", "SSH://attacker.example/x", false},
		// Opened with os.Open.
		{"absolute local path", "/etc/shadow", false},
		{"relative local path", "music/a.mp3", false},
		{"windows drive path", `C:\music\a.mp3`, false},
		{"file url", "file:///etc/passwd", false},
		{"data url", "data:audio/mp3,AAAA", false},
		{"javascript url", "javascript:alert(1)", false},
		{"flag shaped", "--exec=whoami", false},
		{"empty", "", false},
		{"control character", "https://e.com/a\x00b", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AllowsTrackPath(tt.path); got != tt.want {
				t.Errorf("AllowsTrackPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// TestParsedURLsAreAcceptedDownstream is the invariant that ties the two
// layers together: anything Parse accepts as a URL target must also satisfy
// the http/https prefix test that resolve and playback apply to a track path.
// A disagreement here means a value validated as a URL gets handled as a
// local path further in.
func TestParsedURLsAreAcceptedDownstream(t *testing.T) {
	uris := []string{
		"cliamp://play?url=https://example.com/a.mp3",
		"cliamp://play?url=HTTPS%3A%2F%2Fexample.com%2Fa.mp3",
		"cliamp://play?url=HtTp%3A%2F%2Fexample.com%2Fa.mp3",
		"cliamp://queue?url=http://localhost:4533/s.mp3",
	}
	for _, uri := range uris {
		t.Run(uri, func(t *testing.T) {
			action, err := Parse(uri)
			if err != nil {
				t.Fatalf("Parse(%q): %v", uri, err)
			}
			if !strings.HasPrefix(action.URL, "http://") && !strings.HasPrefix(action.URL, "https://") {
				t.Errorf("Parse(%q).URL = %q, which downstream prefix checks reject", uri, action.URL)
			}
			if !AllowsTrackPath(action.URL) {
				t.Errorf("Parse(%q).URL = %q, which AllowsTrackPath rejects", uri, action.URL)
			}
			if _, err := validateURL(action.URL); err != nil {
				t.Errorf("Parse(%q).URL = %q, which validateURL rejects: %v", uri, action.URL, err)
			}
		})
	}
}
