package config

import "testing"

func TestParseFlagsProviderJellyfin(t *testing.T) {
	_, ov, positional, err := ParseFlags([]string{"--provider", "jellyfin"})
	if err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if ov.Provider == nil {
		t.Fatal("Provider override is nil")
	}
	if *ov.Provider != "jellyfin" {
		t.Fatalf("Provider override = %q, want jellyfin", *ov.Provider)
	}
	if len(positional) != 0 {
		t.Fatalf("positional = %v, want empty", positional)
	}
}
