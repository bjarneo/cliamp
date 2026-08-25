package main

import (
	"context"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/bjarneo/cliamp/external/lyrion"
)

// A Lyrion track is a finite file, so it must route to the buffered download
// pipeline alongside the other provider endpoints.
func TestIsBufferedProviderURLIncludesLyrion(t *testing.T) {
	// The matcher sees the URL a source resolver produced, not the track path.
	for _, c := range []*lyrion.Client{
		lyrion.New("http://nas.local:9000", "", ""),
		lyrion.New("http://nas.local:9000", "bob", "pw"),
	} {
		u, _, err := c.ResolveSource(lyrion.TrackURIPrefix + "77")
		if err != nil {
			t.Fatalf("ResolveSource: %v", err)
		}
		if !isBufferedProviderURL(u) {
			t.Errorf("isBufferedProviderURL(%q) = false, want true", u)
		}
	}
}

// A lyrion:// track path is resolved before the matcher ever sees it, so the
// URI form itself must not be mistaken for a directly fetchable stream URL.
// (That track paths carry no credentials is asserted in the lyrion package.)
func TestLyrionTrackURIIsNotABufferedURL(t *testing.T) {
	if isBufferedProviderURL(lyrion.TrackURIPrefix + "77") {
		t.Error("a lyrion:// URI should not match the buffered URL matcher; it is resolved first")
	}
}

func TestIsBufferedProviderURLRejectsLiveStreams(t *testing.T) {
	for _, u := range []string{
		"https://stream.example.com/live.mp3",
		"http://nas.local:9000/music/current/cover.jpg",
	} {
		if isBufferedProviderURL(u) {
			t.Errorf("isBufferedProviderURL(%q) = true, want false", u)
		}
	}
}

// The --provider flag validates against an explicit allowlist, so a new
// provider must be added there as well as registered in run(). Missing it
// makes `--provider lyrion` and `default_provider = "lyrion"` fail even though
// the provider itself works.
func TestProviderFlagAcceptsLyrion(t *testing.T) {
	app := buildApp()
	var flagErr error
	var got string
	app.Action = func(_ context.Context, c *cli.Command) error {
		ov, err := overridesFromFlags(c)
		flagErr = err
		if err == nil && ov.Provider != nil {
			got = *ov.Provider
		}
		return nil
	}
	if err := app.Run(context.Background(), []string{"cliamp", "--provider", "lyrion"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if flagErr != nil {
		t.Fatalf("--provider lyrion rejected: %v", flagErr)
	}
	if got != "lyrion" {
		t.Errorf("resolved provider = %q, want lyrion", got)
	}
}
