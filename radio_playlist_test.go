package main

import (
	"testing"

	"github.com/bjarneo/cliamp/external/radio"
	"github.com/bjarneo/cliamp/resolve"
)

// TestArgsBuiltinRadioURLGoesToPending pins the assumption the default startup
// playlist rests on: the cliamp radio station list is a remote M3U, so it is
// deferred to the pending path rather than fetched inline. That is what keeps
// launch from blocking on the network, and what makes the startup playlist
// expand through the same parser the "cliamp radio" provider entry uses --
// same channels, same order, same titles.
func TestArgsBuiltinRadioURLGoesToPending(t *testing.T) {
	got, err := resolve.Args([]string{radio.BuiltinURL})
	if err != nil {
		t.Fatalf("Args(%q) error = %v", radio.BuiltinURL, err)
	}
	if len(got.Tracks) != 0 {
		t.Errorf("Tracks = %v, want none resolved inline", got.Tracks)
	}
	if len(got.Pending) != 1 || got.Pending[0] != radio.BuiltinURL {
		t.Errorf("Pending = %v, want [%s]", got.Pending, radio.BuiltinURL)
	}
}
