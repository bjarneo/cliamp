// Package deeplink parses cliamp:// URIs into the small set of actions the
// protocol handler is allowed to perform.
//
// A cliamp:// URI can be delivered by any web page with a single click, so
// every value reaching Parse is untrusted. Two rules keep that manageable:
//
//   - Parse returns a closed struct. There is no field that can carry an IPC
//     operation name, a command-line flag or a filesystem path, so a caller
//     cannot accidentally turn URI text into any of those.
//   - Parse performs no I/O. It is a pure function over a string, which is
//     what makes the whole surface testable in a table.
//
// Callers map the returned Action onto an explicit allowlist of IPC
// operations. They must never forward Action fields as operation names.
package deeplink

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// Verb is the action a URI requests. The set is closed: a URI that does not
// name one of these is rejected rather than passed through.
type Verb uint8

const (
	// Play starts the target immediately, replacing what is playing.
	Play Verb = iota + 1
	// Queue appends the target without interrupting playback.
	Queue
)

// String returns the URI spelling of the verb.
func (v Verb) String() string {
	switch v {
	case Play:
		return "play"
	case Queue:
		return "queue"
	}
	return "unknown"
}

// Target identifies which of Action's mutually exclusive payloads is set.
type Target uint8

const (
	// TargetURL is a plain http/https media or playlist URL.
	TargetURL Target = iota + 1
	// TargetAlbum is a provider album ID.
	TargetAlbum
	// TargetPlaylist is a provider playlist ID.
	TargetPlaylist
	// TargetSearch is a provider search query.
	TargetSearch
)

// Action is the validated result of parsing a cliamp:// URI. Exactly one
// target field is populated, indicated by Target.
type Action struct {
	Verb   Verb
	Target Target

	// URL is set when Target is TargetURL. It is always http or https.
	URL string

	// Provider is the provider key (e.g. "navidrome"), set for every target
	// except TargetURL. It is a bare lowercase identifier, never a path.
	Provider string

	// Album, Playlist and Query hold the provider-scoped payload matching
	// Target. At most one is non-empty.
	Album    string
	Playlist string
	Query    string
}

// ErrNotDeepLink reports a URI that is not in the cliamp scheme at all, so
// callers can tell "not for us" apart from "malformed".
var ErrNotDeepLink = errors.New("not a cliamp:// URI")

// Scheme is the URI scheme cliamp registers with the desktop environment.
const Scheme = "cliamp"

const (
	maxURILen      = 4096
	maxURLLen      = 2048
	maxIdentLen    = 256
	maxProviderLen = 32
)

// Parse converts a cliamp:// URI into an Action, rejecting anything outside
// the documented grammar:
//
//	cliamp://play?url=https://example.com/stream.mp3
//	cliamp://play?provider=navidrome&album=a1b2c3
//	cliamp://play?provider=navidrome&playlist=a1b2c3
//	cliamp://queue?provider=ytmusic&q=aphex+twin
//
// There are two verbs and no more. "play" makes the target the thing that is
// playing now; "queue" adds it without interrupting. A third verb meaning
// "load without playing" was dropped because every operation it could map to
// already behaves like one of these two, and a verb whose behavior is
// indistinguishable from another invites callers to expect a difference.
//
// Local file targets are deliberately absent: a link that names a local path
// lets the sending page probe the filesystem through decode errors, and the
// CLI already covers that case for anyone who wants it.
func Parse(raw string) (Action, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Action{}, ErrNotDeepLink
	}
	if len(raw) > maxURILen {
		return Action{}, fmt.Errorf("cliamp URI is too long (%d bytes, limit %d)", len(raw), maxURILen)
	}

	u, err := url.Parse(raw)
	if err != nil {
		return Action{}, fmt.Errorf("invalid cliamp URI: %w", err)
	}
	if !strings.EqualFold(u.Scheme, Scheme) {
		return Action{}, ErrNotDeepLink
	}

	verb, err := parseVerb(u.Host)
	if err != nil {
		return Action{}, err
	}
	// The verb is the whole authority component. A trailing path means the
	// sender is using a grammar we do not implement, and silently ignoring it
	// would run an action they did not intend.
	if p := strings.Trim(u.Path, "/"); p != "" {
		return Action{}, fmt.Errorf("cliamp://%s takes no path segment, got %q", verb, p)
	}

	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return Action{}, fmt.Errorf("invalid cliamp URI query: %w", err)
	}
	if err := rejectUnknownParams(query); err != nil {
		return Action{}, err
	}

	action := Action{Verb: verb}
	if err := applyTarget(&action, query); err != nil {
		return Action{}, err
	}
	return action, nil
}

func parseVerb(host string) (Verb, error) {
	switch strings.ToLower(host) {
	case "play":
		return Play, nil
	case "queue":
		return Queue, nil
	case "":
		return 0, errors.New("cliamp URI is missing an action (expected cliamp://play or cliamp://queue)")
	default:
		return 0, fmt.Errorf("unknown cliamp action %q (expected play or queue)", host)
	}
}

// knownParams is an allowlist. Rejecting unknown keys means a URI written
// against a future grammar fails loudly instead of doing something close to,
// but not the same as, what the sender meant.
var knownParams = map[string]bool{
	"url":      true,
	"provider": true,
	"album":    true,
	"playlist": true,
	"q":        true,
}

func rejectUnknownParams(query url.Values) error {
	for key := range query {
		if !knownParams[key] {
			return fmt.Errorf("unknown cliamp URI parameter %q", key)
		}
		if len(query[key]) > 1 {
			return fmt.Errorf("cliamp URI parameter %q is repeated", key)
		}
	}
	return nil
}

// applyTarget resolves the mutually exclusive target parameters, requiring
// exactly one and validating whichever was supplied.
func applyTarget(action *Action, query url.Values) error {
	rawURL := strings.TrimSpace(query.Get("url"))
	provider := strings.TrimSpace(query.Get("provider"))
	album := strings.TrimSpace(query.Get("album"))
	playlist := strings.TrimSpace(query.Get("playlist"))
	search := strings.TrimSpace(query.Get("q"))

	var targets []string
	if rawURL != "" {
		targets = append(targets, "url")
	}
	if album != "" {
		targets = append(targets, "album")
	}
	if playlist != "" {
		targets = append(targets, "playlist")
	}
	if search != "" {
		targets = append(targets, "q")
	}
	switch len(targets) {
	case 0:
		return errors.New("cliamp URI names no target (expected url, album, playlist or q)")
	case 1:
	default:
		return fmt.Errorf("cliamp URI names %d targets (%s); expected exactly one", len(targets), strings.Join(targets, ", "))
	}

	if rawURL != "" {
		if provider != "" {
			return errors.New("cliamp URI cannot combine url with provider")
		}
		clean, err := validateURL(rawURL)
		if err != nil {
			return err
		}
		action.Target, action.URL = TargetURL, clean
		return nil
	}

	if err := validateProvider(provider); err != nil {
		return err
	}
	action.Provider = provider

	switch {
	case album != "":
		if err := validateIdent("album", album); err != nil {
			return err
		}
		action.Target, action.Album = TargetAlbum, album
	case playlist != "":
		if err := validateIdent("playlist", playlist); err != nil {
			return err
		}
		action.Target, action.Playlist = TargetPlaylist, playlist
	default:
		if err := validateIdent("q", search); err != nil {
			return err
		}
		action.Target, action.Query = TargetSearch, search
	}
	return nil
}

// validateURL admits only http and https. Everything else that cliamp can
// play reaches an external process: ssh:// runs the ssh binary against a
// caller-named host, and the yt-dlp path treats its final argument as a
// positional that a flag-shaped string could escape. A web page must not
// choose either.
//
// Private and loopback addresses are deliberately allowed. Streaming from a
// Navidrome or Jellyfin server on the LAN is the common case, and blocking it
// would break the feature to prevent a page from learning timing facts it can
// mostly obtain by other means.
func validateURL(raw string) (string, error) {
	if len(raw) > maxURLLen {
		return "", fmt.Errorf("url is too long (%d bytes, limit %d)", len(raw), maxURLLen)
	}
	if err := rejectUnprintable("url", raw); err != nil {
		return "", err
	}
	// A leading "-" cannot survive url.Parse as a scheme, but reject it up
	// front so the reason is obvious to whoever reads the error.
	if strings.HasPrefix(raw, "-") {
		return "", fmt.Errorf("url %q looks like a command-line flag", raw)
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid url: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	case "":
		return "", fmt.Errorf("url %q has no scheme (expected http or https)", raw)
	default:
		return "", fmt.Errorf("url scheme %q is not allowed in a cliamp:// link (expected http or https)", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("url %q has no host", raw)
	}
	if u.User != nil {
		return "", errors.New("url must not carry credentials")
	}

	// Normalize the scheme to lowercase. url.Parse lowercases u.Scheme, but
	// the checks downstream compare a literal "https://" prefix against the
	// raw string, so returning "HTTPS://host/a.mp3" unchanged would be
	// accepted here and then treated as a local file path by resolve. Layers
	// that disagree about what a value is are how a validated input turns
	// back into an unvalidated one.
	if scheme, rest, ok := strings.Cut(raw, ":"); ok && scheme != strings.ToLower(scheme) {
		raw = strings.ToLower(scheme) + ":" + rest
	}
	return raw, nil
}

// dangerousTrackSchemes name the transports a link must not reach through a
// provider's answer.
//
// "ssh" is the one that matters: player.openSource routes an ssh:// path to
// exec.Command("ssh", ...) against the host named in it, so whoever chose the
// path chooses a host the user connects to. The rest only ever fail to
// decode, but a provider has no legitimate reason to return one.
var dangerousTrackSchemes = map[string]bool{
	"ssh":        true,
	"file":       true,
	"data":       true,
	"javascript": true,
}

// AllowsTrackPath reports whether a track a provider resolved on behalf of a
// link may be played.
//
// This is deliberately looser than the http-only rule applied to a url=
// target. A provider answers with its own native paths, and some are not URLs
// at all: Spotify returns "spotify:track:...", which player.pipeline hands to
// a registered streamer before openSource is ever consulted. Refusing those
// would break a legitimate search link for no gain, because the provider
// built the path rather than forwarding a stranger's.
//
// What it refuses is a path that reaches the ssh client or the local
// filesystem. A path with no scheme is opened with os.Open, which would let a
// link probe for files through decode behavior.
func AllowsTrackPath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || strings.HasPrefix(path, "-") {
		return false
	}
	if err := rejectUnprintable("track path", path); err != nil {
		return false
	}
	u, err := url.Parse(path)
	if err != nil {
		return false
	}
	// A one-character scheme is a Windows drive letter ("C:\music\a.mp3"),
	// which is a local path rather than a transport.
	if len(u.Scheme) < 2 {
		return false
	}
	return !dangerousTrackSchemes[strings.ToLower(u.Scheme)]
}

// validateProvider requires a bare lowercase identifier. Provider keys are
// looked up in a runtime map, so the charset restriction is what stops a
// value shaped like a path or an option from ever being used as one.
func validateProvider(provider string) error {
	if provider == "" {
		return errors.New("cliamp URI is missing the provider parameter")
	}
	if len(provider) > maxProviderLen {
		return fmt.Errorf("provider name is too long (%d bytes, limit %d)", len(provider), maxProviderLen)
	}
	for i, r := range provider {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9', r == '-', r == '_':
			if i == 0 {
				return fmt.Errorf("provider %q must start with a letter", provider)
			}
		default:
			return fmt.Errorf("provider %q contains disallowed characters (expected lowercase letters, digits, - and _)", provider)
		}
	}
	return nil
}

// validateIdent covers provider-scoped IDs and search text. These are opaque
// to cliamp and are forwarded to a provider API, so the checks are about
// keeping them out of argument and control-character territory rather than
// about their shape.
func validateIdent(name, value string) error {
	if len(value) > maxIdentLen {
		return fmt.Errorf("%s is too long (%d bytes, limit %d)", name, len(value), maxIdentLen)
	}
	if err := rejectUnprintable(name, value); err != nil {
		return err
	}
	if strings.HasPrefix(value, "-") {
		return fmt.Errorf("%s %q looks like a command-line flag", name, value)
	}
	return nil
}

// rejectUnprintable blocks control characters, which have no legitimate place
// in any of these values and are how a crafted URI would try to break out of
// a log line, a terminal or a downstream parser.
func rejectUnprintable(name, value string) error {
	for _, r := range value {
		if r == unicode.ReplacementChar {
			return fmt.Errorf("%s contains invalid UTF-8", name)
		}
		if unicode.IsControl(r) {
			return fmt.Errorf("%s contains a control character", name)
		}
	}
	return nil
}
