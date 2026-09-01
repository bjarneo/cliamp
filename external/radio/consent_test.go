package radio

import (
	"errors"
	"testing"
)

// The provider must not work out anybody's location until it is told to. A
// fresh install has no answer recorded, so New must leave home unset no matter
// what the host's timezone says.
func TestNewDetectsNoLocationUntilAsked(t *testing.T) {
	t.Setenv("TZ", "Europe/Oslo")

	for _, tc := range []struct {
		name       string
		country    string
		wantHome   string
		wantAsking bool
	}{
		{name: "not asked yet", country: "", wantHome: "", wantAsking: true},
		{name: "declined", country: CountryDeclined, wantHome: "", wantAsking: false},
		{name: "declined, any case", country: "None", wantHome: "", wantAsking: false},
		{name: "answered with a country", country: "NO", wantHome: "NO", wantAsking: false},
		{name: "answered lowercase", country: "no", wantHome: "NO", wantAsking: false},
		{name: "unusable code is not an answer", country: "XX", wantHome: "", wantAsking: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			p := New(Options{Country: tc.country})

			if got := p.HomeCountry().Code; got != tc.wantHome {
				t.Errorf("HomeCountry() = %q, want %q", got, tc.wantHome)
			}
			if got := p.NeedsLocationConsent(); got != tc.wantAsking {
				t.Errorf("NeedsLocationConsent() = %v, want %v", got, tc.wantAsking)
			}
		})
	}
}

func TestSetLocationConsentAllowed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TZ", "Europe/Oslo")

	var saved string
	p := New(Options{SaveCountry: func(code string) error { saved = code; return nil }})

	place, err := p.SetLocationConsent(true)
	if err != nil {
		t.Fatalf("SetLocationConsent: %v", err)
	}
	if place != "Norway" {
		t.Errorf("place = %q, want Norway", place)
	}
	if p.HomeCountry().Code != "NO" {
		t.Errorf("HomeCountry() = %+v, want NO", p.HomeCountry())
	}
	if saved != "NO" {
		t.Errorf("persisted %q, want NO", saved)
	}
	if p.NeedsLocationConsent() {
		t.Error("still asking after an answer")
	}
}

func TestSetLocationConsentRefused(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TZ", "Europe/Oslo")

	var saved string
	p := New(Options{SaveCountry: func(code string) error { saved = code; return nil }})

	place, err := p.SetLocationConsent(false)
	if err != nil {
		t.Fatalf("SetLocationConsent: %v", err)
	}
	if place != "" {
		t.Errorf("place = %q, want nothing found", place)
	}
	// A refusal must leave no trace of where the listener is, even though the
	// timezone would have answered it.
	if p.HomeCountry().Code != "" {
		t.Errorf("HomeCountry() = %+v, want empty after a refusal", p.HomeCountry())
	}
	if saved != CountryDeclined {
		t.Errorf("persisted %q, want %q", saved, CountryDeclined)
	}
	if p.NeedsLocationConsent() {
		t.Error("still asking after a refusal")
	}
}

// Agreeing but being undetectable is recorded as a refusal: both mean "no
// location", and neither should re-ask on the next launch.
func TestSetLocationConsentAllowedButUndetectable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TZ", "Mars/Olympus")
	t.Setenv("LC_ALL", "C")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "")

	var saved string
	p := New(Options{SaveCountry: func(code string) error { saved = code; return nil }})

	place, err := p.SetLocationConsent(true)
	if err != nil {
		t.Fatalf("SetLocationConsent: %v", err)
	}
	if place != "" || p.HomeCountry().Code != "" {
		t.Errorf("place = %q, home = %+v, want both empty", place, p.HomeCountry())
	}
	if saved != CountryDeclined {
		t.Errorf("persisted %q, want %q", saved, CountryDeclined)
	}
	if p.NeedsLocationConsent() {
		t.Error("still asking after an answer that found nothing")
	}
}

// A failed write must not cost the listener their answer for this run.
func TestSetLocationConsentSurvivesAFailedSave(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TZ", "Europe/Oslo")

	p := New(Options{SaveCountry: func(string) error { return errors.New("disk full") }})

	if _, err := p.SetLocationConsent(true); err == nil {
		t.Fatal("SetLocationConsent should report the save failure")
	}
	if p.HomeCountry().Code != "NO" {
		t.Errorf("HomeCountry() = %+v, want the answer honored anyway", p.HomeCountry())
	}
	if p.NeedsLocationConsent() {
		t.Error("should not re-ask within the same run")
	}
}

func TestSetLocationConsentWithoutASaverIsSessionOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TZ", "Europe/Oslo")

	p := New(Options{})
	if _, err := p.SetLocationConsent(true); err != nil {
		t.Fatalf("SetLocationConsent: %v", err)
	}
	if p.HomeCountry().Code != "NO" || p.NeedsLocationConsent() {
		t.Errorf("home = %+v, asking = %v", p.HomeCountry(), p.NeedsLocationConsent())
	}
}

func TestLocationPromptSaysWhereTheLocationComesFrom(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p := New(Options{})
	if p.LocationPrompt() == "" {
		t.Fatal("LocationPrompt is empty")
	}
}
