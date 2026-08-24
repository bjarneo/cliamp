package cmd

import (
	"strings"
	"testing"
)

func lyrionSpec(t *testing.T) providerSpec {
	t.Helper()
	for _, p := range providers() {
		if p.section == "lyrion" {
			return p
		}
	}
	t.Fatal("no lyrion provider in the setup wizard")
	return providerSpec{}
}

func TestLyrionSetupWritesSection(t *testing.T) {
	p := lyrionSpec(t)
	body := p.body(map[string]string{
		"url":      "http://nas.local:9000",
		"user":     "bob",
		"password": "pw",
	})
	for _, want := range []string{`url      = "http://nas.local:9000"`, `user     = "bob"`, `password = "pw"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\ngot:\n%s", want, body)
		}
	}
}

// Only the URL is required: LMS servers without password protection are the
// common case, and the wizard must not force credentials on them.
func TestLyrionSetupRequiresOnlyURL(t *testing.T) {
	p := lyrionSpec(t)
	required := map[string]bool{}
	for _, f := range p.fields {
		required[f.key] = f.required
	}
	// Assert presence as well as optionality: a missing key also reads as
	// "not required", which would let a dropped field pass silently.
	for _, key := range []string{"url", "user", "password"} {
		if _, ok := required[key]; !ok {
			t.Errorf("no %q field in the Lyrion setup form", key)
		}
	}
	if !required["url"] {
		t.Error("url field should be required")
	}
	if required["user"] || required["password"] {
		t.Error("credentials should be optional for an unprotected server")
	}
}

func TestLyrionSetupHidesPassword(t *testing.T) {
	p := lyrionSpec(t)
	for _, f := range p.fields {
		if f.key == "password" && !f.secret {
			t.Error("password field should be marked secret")
		}
	}
}
