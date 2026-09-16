package subsonicapi

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/bjarneo/cliamp/provider"
)

// dialect captures the handful of behaviors that differ between the Subsonic
// servers cliamp talks to (Navidrome, Bandcamp). Everything else in Client is
// shared. Mirrors the internal/embyapi dialect pattern.
type dialect interface {
	name() string    // provider display name shown in the UI
	metaKey() string // playlist.Track ProviderMeta key
	// apiVersion is the v= protocol parameter. Navidrome is frozen at the
	// historical "1.0.0" for bit-identical wire behavior; stricter servers
	// get the version matching the features actually used.
	apiVersion() string
	// endpointSuffix is appended to every endpoint name. Bandcamp's beta
	// router registers some endpoints (createPlaylist, updatePlaylist) only
	// under their ".view" form, while accepting ".view" everywhere — so its
	// dialect uses ".view" uniformly (verified live 2026-08-25). Navidrome
	// keeps its historical bare names.
	endpointSuffix() string
	// scrobbleSupported gates PlaybackReporter: when false the provider
	// never claims tracks for now-playing/scrobble reporting.
	scrobbleSupported() bool
	// singleSongPlaylistAdd works around servers that honor only the last
	// songIdToAdd in a multi-value updatePlaylist call (Bandcamp's beta,
	// verified live 2026-08-25): batch adds become one call per song.
	singleSongPlaylistAdd() bool
	defaultSort() string
	albumSortTypes() []provider.SortType
	// sortListExhaustive reports whether albumSortTypes covers every sort the
	// server accepts. When true a configured browse_sort outside that list is
	// replaced by defaultSort, because the server would answer an unknown
	// type with an empty album list and no error. Navidrome says false: it
	// takes the full Subsonic set (random, highest, ...) even though the
	// picker offers a subset, and a hand-edited config must keep working.
	sortListExhaustive() bool
	// httpError maps a non-200 API response to the error shown to the user.
	httpError(endpoint string, status int, statusText string) error
	// missingEnvelope maps a valid-JSON response that lacks the
	// subsonic-response envelope. nil means proceed (the historical
	// Navidrome behavior); a non-nil error marks the endpoint unsupported
	// and is memoized until Refresh. Memoizing by endpoint name is safe on
	// the beta: bad parameters (e.g. an unknown getAlbumList2 sort type)
	// answer with a proper ok envelope, not the fall-through (verified
	// live 2026-08-25), and the dialect condemns only Bandcamp's own
	// fall-through signature, so a transient proxy/LB JSON body cannot
	// poison the memo.
	missingEnvelope(endpoint string, body []byte) error
}

// navidromeDialect preserves the exact wire behavior the Navidrome provider
// has always had.
type navidromeDialect struct{}

func (navidromeDialect) name() string                { return "Navidrome" }
func (navidromeDialect) metaKey() string             { return provider.MetaNavidromeID }
func (navidromeDialect) apiVersion() string          { return "1.0.0" }
func (navidromeDialect) endpointSuffix() string      { return "" }
func (navidromeDialect) scrobbleSupported() bool     { return true }
func (navidromeDialect) singleSongPlaylistAdd() bool { return false }
func (navidromeDialect) defaultSort() string         { return SortAlphabeticalByName }
func (navidromeDialect) sortListExhaustive() bool    { return false }

func (navidromeDialect) albumSortTypes() []provider.SortType {
	return []provider.SortType{
		{ID: SortAlphabeticalByName, Label: "Alphabetical by Name"},
		{ID: SortAlphabeticalByArtist, Label: "Alphabetical by Artist"},
		{ID: SortNewest, Label: "Newest"},
		{ID: SortRecent, Label: "Recently Played"},
		{ID: SortFrequent, Label: "Most Played"},
		{ID: SortStarred, Label: "Starred"},
		{ID: SortByYear, Label: "By Year"},
		{ID: SortByGenre, Label: "By Genre"},
	}
}

func (navidromeDialect) httpError(endpoint string, _ int, statusText string) error {
	return fmt.Errorf("navidrome: %s: http status %s", endpoint, statusText)
}

func (navidromeDialect) missingEnvelope(string, []byte) error { return nil }

// NewNavidromeClient returns a Client speaking the Navidrome dialect.
func NewNavidromeClient(cfg Config) *Client {
	return newClient(navidromeDialect{}, cfg)
}

// bandcampDialect speaks to Bandcamp's official Subsonic API open beta
// (https://bandcamp.com/api/subsonic, July 2026). Quirks verified against the
// live endpoint with real credentials, 2026-08-25: bad credentials surface as
// a bare HTTP 500 (never Subsonic error 40); unimplemented endpoints fall
// through to a generic JSON error without the subsonic-response envelope;
// ping answers ok without validating credentials; some endpoints
// (createPlaylist, updatePlaylist) are routed only under their .view form
// while .view works everywhere; multi-value songIdToAdd keeps only the last
// value; streams are 128 kbps MP3 via a bcbits.com redirect and format=raw
// is accepted but ignored. The beta is a moving target; revisit these knobs
// as it evolves.
type bandcampDialect struct{}

func (bandcampDialect) name() string                { return "Bandcamp" }
func (bandcampDialect) metaKey() string             { return provider.MetaBandcampID }
func (bandcampDialect) apiVersion() string          { return "1.16.1" }
func (bandcampDialect) endpointSuffix() string      { return ".view" }
func (bandcampDialect) scrobbleSupported() bool     { return false }
func (bandcampDialect) singleSongPlaylistAdd() bool { return true }
func (bandcampDialect) defaultSort() string         { return SortNewest }
func (bandcampDialect) sortListExhaustive() bool    { return true }

func (bandcampDialect) albumSortTypes() []provider.SortType {
	// Conservative subset until the beta's supported getAlbumList2 types are
	// probed; the default shows recent purchases first.
	return []provider.SortType{
		{ID: SortNewest, Label: "Newest"},
		{ID: SortAlphabeticalByName, Label: "Alphabetical by Name"},
		{ID: SortAlphabeticalByArtist, Label: "Alphabetical by Artist"},
	}
}

func (bandcampDialect) httpError(endpoint string, status int, statusText string) error {
	// Wrong credentials surface as a bare 500 on the beta — but so does a
	// server-side fault, and the two are indistinguishable from one
	// response. On the auth probe (the call whose whole job is to test the
	// credentials) the likeliest cause is worth naming in the message, but
	// it stays an ordinary error: classifying a guess as ErrBadCredentials
	// would let one failing endpoint blank a working pane and tell the user
	// to regenerate credentials that are fine.
	if status == http.StatusInternalServerError && endpoint == authProbeEndpoint {
		return fmt.Errorf("bandcamp: %s: http status %s — this usually means wrong Subsonic credentials; regenerate them in Bandcamp Fan Settings and update [bandcamp] in config.toml", endpoint, statusText)
	}
	return fmt.Errorf("bandcamp: %s: http status %s", endpoint, statusText)
}

func (bandcampDialect) missingEnvelope(endpoint string, body []byte) error {
	// Only Bandcamp's generic fall-through ({"error":true,...}) marks an
	// endpoint unimplemented; any other envelope-less JSON is treated as a
	// transient oddity and passed through un-memoized.
	var generic struct {
		Error bool `json:"error"`
	}
	if json.Unmarshal(body, &generic) != nil || !generic.Error {
		return nil
	}
	return fmt.Errorf("bandcamp: Bandcamp's Subsonic API (beta) doesn't support %s yet — refresh to retry later", endpoint)
}

// NewBandcampClient returns a Client speaking the Bandcamp dialect.
func NewBandcampClient(cfg Config) *Client {
	return newClient(bandcampDialect{}, cfg)
}
