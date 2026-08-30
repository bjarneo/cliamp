package cmd

import (
	"testing"

	"github.com/bjarneo/cliamp/internal/deeplink"
)

// TestSchemeMatches keeps the registered scheme and the parsed scheme in step.
// They live in different packages, and a mismatch would register a handler for
// links that `cliamp open` then refuses.
func TestSchemeMatches(t *testing.T) {
	if SchemeName != deeplink.Scheme {
		t.Errorf("SchemeName = %q, deeplink.Scheme = %q; they must match", SchemeName, deeplink.Scheme)
	}
}
