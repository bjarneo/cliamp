package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAudioBackendConfigParsing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", dir)
	data := []byte(`audio_backend = "mpv"
audio_device = "alsa/hw:CARD=Generic,DEV=0"
audio_reservation = "Audio2"
bit_perfect = true
volume = 0
speed = 1.0
eq_preset = "Flat"
`)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AudioBackend != "mpv" || cfg.AudioDevice != "alsa/hw:CARD=Generic,DEV=0" || cfg.AudioReservation != "Audio2" || !cfg.BitPerfect {
		t.Fatalf("audio config = %+v", cfg)
	}
}

func TestValidateAudio(t *testing.T) {
	validMPV := defaultConfig()
	validMPV.AudioBackend = "mpv"
	validMPV.AudioDevice = "alsa/hw:CARD=Generic,DEV=0"
	validMPV.BitPerfect = true

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{name: "valid native"},
		{name: "valid MPV bit-perfect", mutate: func(c *Config) { *c = validMPV }},
		{name: "invalid backend", mutate: func(c *Config) { c.AudioBackend = "vlc" }, wantErr: "native or mpv"},
		{name: "native bit-perfect", mutate: func(c *Config) { c.BitPerfect = true }, wantErr: "requires audio_backend"},
		{name: "MPV mono", mutate: func(c *Config) { c.AudioBackend, c.Mono = "mpv", true }, wantErr: "mono is unsupported"},
		{name: "MPV EQ", mutate: func(c *Config) { c.AudioBackend, c.EQPreset = "mpv", "Rock" }, wantErr: "EQ is unsupported"},
		{name: "non-direct device", mutate: func(c *Config) { *c = validMPV; c.AudioDevice = "alsa/default" }, wantErr: "direct MPV ALSA device"},
		{name: "volume conflict", mutate: func(c *Config) { *c = validMPV; c.Volume = -3 }, wantErr: "requires volume = 0"},
		{name: "speed conflict", mutate: func(c *Config) { *c = validMPV; c.Speed = 1.25 }, wantErr: "requires speed = 1.0"},
		{name: "native reservation", mutate: func(c *Config) { c.AudioReservation = "Audio2" }, wantErr: "audio_reservation requires"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := defaultConfig()
			if test.mutate != nil {
				test.mutate(&cfg)
			}
			err := cfg.ValidateAudio()
			if test.wantErr == "" && err != nil {
				t.Fatalf("ValidateAudio() = %v", err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("ValidateAudio() = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestAudioBackendOverrides(t *testing.T) {
	cfg := defaultConfig()
	backend := "mpv"
	device := "alsa/hw:CARD=Generic,DEV=0"
	reservation := "Audio2"
	bitPerfect := true
	Overrides{
		AudioBackend:     &backend,
		AudioDevice:      &device,
		AudioReservation: &reservation,
		BitPerfect:       &bitPerfect,
	}.Apply(&cfg)
	if err := cfg.ValidateAudio(); err != nil {
		t.Fatal(err)
	}
	if cfg.AudioBackend != backend || cfg.AudioDevice != device || cfg.AudioReservation != reservation || !cfg.BitPerfect {
		t.Fatalf("overridden config = %+v", cfg)
	}
}

func TestAudioValidationRunsAfterOverrides(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("audio_backend = \"mpv\"\nmono = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	backend, mono := "native", false
	Overrides{AudioBackend: &backend, Mono: &mono}.Apply(&cfg)
	if err := cfg.ValidateAudio(); err != nil {
		t.Fatalf("overridden config: %v", err)
	}
}
