// Package navidrome adapts the shared Subsonic client (internal/subsonicapi)
// to a Navidrome server and exposes it as a playlist provider.
package navidrome

import (
	"os"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/internal/subsonicapi"
	"github.com/bjarneo/cliamp/playlist"
)

// NavidromeClient aliases the shared Subsonic client so the provider layer
// reads naturally and external callers keep using navidrome.NavidromeClient.
type NavidromeClient = subsonicapi.Client

// IsSubsonicStreamURL reports whether path is a Subsonic stream or download
// endpoint. Used by the player to select the buffered download pipeline.
func IsSubsonicStreamURL(path string) bool { return playlist.IsSubsonicStreamURL(path) }

// New creates a NavidromeClient with the given server credentials. Unlike
// NewFromConfig it never returns nil: an empty credential simply fails at
// request time, as it always has.
func New(serverURL, user, password string) *NavidromeClient {
	return subsonicapi.NewNavidromeClient(subsonicapi.Config{
		BaseURL:  serverURL,
		User:     user,
		Password: password,
		SaveSort: config.SaveNavidromeSort,
	})
}

// NewFromEnv creates a NavidromeClient from NAVIDROME_URL, NAVIDROME_USER,
// and NAVIDROME_PASS environment variables, retaining non-credential settings
// from cfg. Returns nil if any environment credentials are unset.
func NewFromEnv(cfg config.NavidromeConfig) *NavidromeClient {
	cfg.URL = os.Getenv("NAVIDROME_URL")
	cfg.User = os.Getenv("NAVIDROME_USER")
	cfg.Password = os.Getenv("NAVIDROME_PASS")
	return NewFromConfig(cfg)
}

// NewFromConfig creates a NavidromeClient from a config.NavidromeConfig value.
// Returns nil if any of the required fields (URL, User, Password) are empty.
func NewFromConfig(cfg config.NavidromeConfig) *NavidromeClient {
	if !cfg.IsSet() {
		return nil
	}
	return subsonicapi.NewNavidromeClient(subsonicapi.Config{
		BaseURL:          cfg.URL,
		User:             cfg.User,
		Password:         cfg.Password,
		BrowseSort:       cfg.BrowseSort,
		StreamFormat:     cfg.Format,
		ScrobbleDisabled: cfg.ScrobbleDisabled,
		SaveSort:         config.SaveNavidromeSort,
	})
}
