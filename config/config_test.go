package config

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := defaultConfig()

	if cfg.Repeat != "off" {
		t.Errorf("Repeat = %q, want off", cfg.Repeat)
	}
	if cfg.Speed != 1.0 {
		t.Errorf("Speed = %f, want 1.0", cfg.Speed)
	}
	if cfg.SeekStepLarge != 30 {
		t.Errorf("SeekStepLarge = %d, want 30", cfg.SeekStepLarge)
	}
	if cfg.SampleRate != 0 {
		t.Errorf("SampleRate = %d, want 0 (auto)", cfg.SampleRate)
	}
	if cfg.BufferMs != 100 {
		t.Errorf("BufferMs = %d, want 100", cfg.BufferMs)
	}
	if cfg.ResampleQuality != 4 {
		t.Errorf("ResampleQuality = %d, want 4", cfg.ResampleQuality)
	}
	if cfg.BitDepth != 16 {
		t.Errorf("BitDepth = %d, want 16", cfg.BitDepth)
	}
	if cfg.PaddingH != 3 {
		t.Errorf("PaddingH = %d, want 3", cfg.PaddingH)
	}
	if cfg.PaddingV != 1 {
		t.Errorf("PaddingV = %d, want 1", cfg.PaddingV)
	}
	if cfg.Spotify.Bitrate != 320 {
		t.Errorf("Spotify.Bitrate = %d, want 320", cfg.Spotify.Bitrate)
	}
	if cfg.AutoPlay {
		t.Error("AutoPlay should be false by default")
	}
	if cfg.Shuffle {
		t.Error("Shuffle should be false by default")
	}
	if cfg.Mono {
		t.Error("Mono should be false by default")
	}
}

func TestClampVolume(t *testing.T) {
	tests := []struct {
		name string
		vol  float64
		want float64
	}{
		{"within range", -10, -10},
		{"too low", -50, -30},
		{"too high", 20, 6},
		{"min boundary", -30, -30},
		{"max boundary", 6, 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultConfig()
			cfg.Volume = tt.vol
			cfg.clamp()
			if cfg.Volume != tt.want {
				t.Errorf("Volume = %f, want %f", cfg.Volume, tt.want)
			}
		})
	}
}

func TestClampSpeed(t *testing.T) {
	tests := []struct {
		name  string
		speed float64
		want  float64
	}{
		{"normal", 1.0, 1.0},
		{"valid slow", 0.5, 0.5},
		{"valid fast", 1.5, 1.5},
		{"too slow", 0.1, 1.0},
		{"too fast", 3.0, 1.0},
		{"min boundary", 0.25, 0.25},
		{"max boundary", 2.0, 2.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultConfig()
			cfg.Speed = tt.speed
			cfg.clamp()
			if cfg.Speed != tt.want {
				t.Errorf("Speed = %f, want %f", cfg.Speed, tt.want)
			}
		})
	}
}

func TestClampSampleRate(t *testing.T) {
	tests := []struct {
		input int
		want  int
	}{
		{0, 0},         // auto-detect preserved
		{44100, 44100}, // exact match
		{48000, 48000},
		{96000, 96000},
		{30000, 22050}, // rounds to nearest
		{45000, 44100},
		{50000, 48000},
		{100000, 96000},
		{200000, 192000},
	}
	for _, tt := range tests {
		got := clampSampleRate(tt.input)
		if got != tt.want {
			t.Errorf("clampSampleRate(%d) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestClampBitDepth(t *testing.T) {
	tests := []struct {
		input int
		want  int
	}{
		{8, 16},
		{16, 16},
		{24, 32},
		{32, 32},
	}
	for _, tt := range tests {
		got := clampBitDepth(tt.input)
		if got != tt.want {
			t.Errorf("clampBitDepth(%d) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestClampSpotifyBitrate(t *testing.T) {
	tests := []struct {
		input int
		want  int
	}{
		{-1, 320},
		{0, 320},
		{96, 96},
		{160, 160},
		{320, 320},
		{120, 96},
		{128, 96},
		{200, 160},
		{240, 160},
		{500, 320},
	}
	for _, tt := range tests {
		got := clampSpotifyBitrate(tt.input)
		if got != tt.want {
			t.Errorf("clampSpotifyBitrate(%d) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestClampBufferMs(t *testing.T) {
	tests := []struct {
		input int
		want  int
	}{
		{100, 100},
		{10, 50},
		{600, 500},
		{50, 50},
		{500, 500},
	}
	for _, tt := range tests {
		cfg := defaultConfig()
		cfg.BufferMs = tt.input
		cfg.clamp()
		if cfg.BufferMs != tt.want {
			t.Errorf("BufferMs(%d) = %d, want %d", tt.input, cfg.BufferMs, tt.want)
		}
	}
}

func TestClampResampleQuality(t *testing.T) {
	tests := []struct {
		input int
		want  int
	}{
		{0, 1},
		{1, 1},
		{4, 4},
		{5, 4},
	}
	for _, tt := range tests {
		cfg := defaultConfig()
		cfg.ResampleQuality = tt.input
		cfg.clamp()
		if cfg.ResampleQuality != tt.want {
			t.Errorf("ResampleQuality(%d) = %d, want %d", tt.input, cfg.ResampleQuality, tt.want)
		}
	}
}

func TestClampPadding(t *testing.T) {
	tests := []struct {
		name         string
		inH, inV     int
		wantH, wantV int
	}{
		{"negative clamped to 0", -1, -1, 0, 0},
		{"over max clamped", 20, 10, 10, 5},
		{"within range", 3, 1, 3, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultConfig()
			cfg.PaddingH = tt.inH
			cfg.PaddingV = tt.inV
			cfg.clamp()
			if cfg.PaddingH != tt.wantH {
				t.Errorf("PaddingH = %d, want %d", cfg.PaddingH, tt.wantH)
			}
			if cfg.PaddingV != tt.wantV {
				t.Errorf("PaddingV = %d, want %d", cfg.PaddingV, tt.wantV)
			}
		})
	}
}

func TestClampSeekStepLarge(t *testing.T) {
	tests := []struct {
		input int
		want  int
	}{
		{30, 30},
		{1, 6},
		{700, 600},
		{6, 6},
		{600, 600},
	}
	for _, tt := range tests {
		cfg := defaultConfig()
		cfg.SeekStepLarge = tt.input
		cfg.clamp()
		if cfg.SeekStepLarge != tt.want {
			t.Errorf("SeekStepLarge(%d) = %d, want %d", tt.input, cfg.SeekStepLarge, tt.want)
		}
	}
}

func TestParseEQ(t *testing.T) {
	tests := []struct {
		name string
		val  string
		want [10]float64
	}{
		{
			name: "all zeros",
			val:  "[0, 0, 0, 0, 0, 0, 0, 0, 0, 0]",
			want: [10]float64{},
		},
		{
			name: "mixed values",
			val:  "[3, -2, 0, 1.5, -5, 0, 0, 0, 0, 0]",
			want: [10]float64{3, -2, 0, 1.5, -5, 0, 0, 0, 0, 0},
		},
		{
			name: "clamped to range",
			val:  "[15, -20, 0, 0, 0, 0, 0, 0, 0, 0]",
			want: [10]float64{12, -12, 0, 0, 0, 0, 0, 0, 0, 0},
		},
		{
			name: "fewer than 10",
			val:  "[1, 2, 3]",
			want: [10]float64{1, 2, 3, 0, 0, 0, 0, 0, 0, 0},
		},
		{
			name: "empty",
			val:  "[]",
			want: [10]float64{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseEQ(tt.val)
			if got != tt.want {
				t.Errorf("parseEQ(%q) = %v, want %v", tt.val, got, tt.want)
			}
		})
	}
}

func TestNavidromeIsSet(t *testing.T) {
	tests := []struct {
		name string
		cfg  NavidromeConfig
		want bool
	}{
		{"all set", NavidromeConfig{URL: "https://x", User: "u", Password: "p"}, true},
		{"missing URL", NavidromeConfig{User: "u", Password: "p"}, false},
		{"missing user", NavidromeConfig{URL: "https://x", Password: "p"}, false},
		{"missing password", NavidromeConfig{URL: "https://x", User: "u"}, false},
		{"all empty", NavidromeConfig{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.IsSet(); got != tt.want {
				t.Errorf("IsSet() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNavidromeScrobbleEnabled(t *testing.T) {
	tests := []struct {
		name     string
		disabled bool
		want     bool
	}{
		{"default enabled", false, true},
		{"explicitly disabled", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NavidromeConfig{ScrobbleDisabled: tt.disabled}
			if got := cfg.ScrobbleEnabled(); got != tt.want {
				t.Errorf("ScrobbleEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSpotifyIsSet(t *testing.T) {
	tests := []struct {
		name string
		cfg  SpotifyConfig
		want bool
	}{
		{"set", SpotifyConfig{ClientID: "abc"}, true},
		{"no id", SpotifyConfig{}, false},
		{"disabled", SpotifyConfig{Disabled: true, ClientID: "abc"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.IsSet(); got != tt.want {
				t.Errorf("IsSet() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoadSpotifyBitrate(t *testing.T) {
	tests := []struct {
		name    string
		bitrate int
		want    int
	}{
		{"exact supported value", 160, 160},
		{"rounded to nearest supported value", 200, 160},
		{"non-positive value", 0, 320},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())

			path := filepath.Join(os.Getenv("HOME"), ".config", "cliamp", "config.toml")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			data := []byte("[spotify]\nbitrate = " + strconv.Itoa(tt.bitrate) + "\n")
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.Spotify.Bitrate != tt.want {
				t.Fatalf("Spotify.Bitrate = %d, want %d", cfg.Spotify.Bitrate, tt.want)
			}
		})
	}
}

func TestPlexIsSet(t *testing.T) {
	tests := []struct {
		name string
		cfg  PlexConfig
		want bool
	}{
		{"both set", PlexConfig{URL: "http://x", Token: "t"}, true},
		{"no URL", PlexConfig{Token: "t"}, false},
		{"no token", PlexConfig{URL: "http://x"}, false},
		{"empty", PlexConfig{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.IsSet(); got != tt.want {
				t.Errorf("IsSet() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestJellyfinIsSet(t *testing.T) {
	tests := []struct {
		name string
		cfg  JellyfinConfig
		want bool
	}{
		{"token auth", JellyfinConfig{URL: "http://x", Token: "t"}, true},
		{"password auth", JellyfinConfig{URL: "http://x", User: "u", Password: "p"}, true},
		{"no URL", JellyfinConfig{Token: "t"}, false},
		{"no auth", JellyfinConfig{URL: "http://x"}, false},
		{"user without password", JellyfinConfig{URL: "http://x", User: "u"}, false},
		{"empty", JellyfinConfig{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.IsSet(); got != tt.want {
				t.Errorf("IsSet() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestYouTubeMusicIsSetOrFallback(t *testing.T) {
	hasFallback := func() (string, string) { return "id", "secret" }
	noFallback := func() (string, string) { return "", "" }

	tests := []struct {
		name       string
		cfg        YouTubeMusicConfig
		fallbackFn func() (string, string)
		want       bool
	}{
		{"enabled section", YouTubeMusicConfig{Enabled: true}, nil, true},
		{"disabled", YouTubeMusicConfig{Disabled: true}, hasFallback, false},
		{"fallback available", YouTubeMusicConfig{}, hasFallback, true},
		{"no fallback", YouTubeMusicConfig{}, noFallback, false},
		{"nil fallback", YouTubeMusicConfig{}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.IsSetOrFallback(tt.fallbackFn); got != tt.want {
				t.Errorf("IsSetOrFallback() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestYouTubeMusicResolveCredentials(t *testing.T) {
	fallback := func() (string, string) { return "fb_id", "fb_secret" }

	tests := []struct {
		name       string
		cfg        YouTubeMusicConfig
		fallbackFn func() (string, string)
		wantID     string
		wantSecret string
	}{
		{"user credentials take priority", YouTubeMusicConfig{ClientID: "my_id", ClientSecret: "my_secret"}, fallback, "my_id", "my_secret"},
		{"falls back when empty", YouTubeMusicConfig{}, fallback, "fb_id", "fb_secret"},
		{"nil fallback returns empty", YouTubeMusicConfig{}, nil, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, secret := tt.cfg.ResolveCredentials(tt.fallbackFn)
			if id != tt.wantID || secret != tt.wantSecret {
				t.Errorf("got (%q, %q), want (%q, %q)", id, secret, tt.wantID, tt.wantSecret)
			}
		})
	}
}

func TestOverridesApply(t *testing.T) {
	cfg := defaultConfig()

	vol := -15.0
	shuffle := true
	repeat := "all"
	mono := true
	theme := "dark"
	compact := true
	sr := 48000
	play := true

	overrides := Overrides{
		Volume:     &vol,
		Shuffle:    &shuffle,
		Repeat:     &repeat,
		Mono:       &mono,
		Theme:      &theme,
		Compact:    &compact,
		SampleRate: &sr,
		Play:       &play,
	}

	overrides.Apply(&cfg)

	if cfg.Volume != -15 {
		t.Errorf("Volume = %f, want -15", cfg.Volume)
	}
	if !cfg.Shuffle {
		t.Error("Shuffle should be true")
	}
	if cfg.Repeat != "all" {
		t.Errorf("Repeat = %q, want all", cfg.Repeat)
	}
	if !cfg.Mono {
		t.Error("Mono should be true")
	}
	if cfg.Theme != "dark" {
		t.Errorf("Theme = %q, want dark", cfg.Theme)
	}
	if !cfg.Compact {
		t.Error("Compact should be true")
	}
	if cfg.SampleRate != 48000 {
		t.Errorf("SampleRate = %d, want 48000", cfg.SampleRate)
	}
	if !cfg.AutoPlay {
		t.Error("AutoPlay should be true")
	}
}

func TestOverridesApplyNil(t *testing.T) {
	cfg := defaultConfig()
	original := cfg

	overrides := Overrides{} // all nil
	overrides.Apply(&cfg)

	// Nothing should change except clamp effects
	if cfg.Volume != original.Volume {
		t.Error("nil overrides changed Volume")
	}
	if cfg.Shuffle != original.Shuffle {
		t.Error("nil overrides changed Shuffle")
	}
}

func TestOverridesApplyClamps(t *testing.T) {
	cfg := defaultConfig()

	vol := 100.0 // out of range
	overrides := Overrides{Volume: &vol}
	overrides.Apply(&cfg)

	if cfg.Volume != 6 {
		t.Errorf("Volume should be clamped to 6, got %f", cfg.Volume)
	}
}

// Mock player for ApplyPlayer tests
type mockPlayer struct {
	volume float64
	speed  float64
	eq     [10]float64
	mono   bool
}

func (m *mockPlayer) SetVolume(db float64)           { m.volume = db }
func (m *mockPlayer) SetSpeed(ratio float64)         { m.speed = ratio }
func (m *mockPlayer) SetEQBand(band int, dB float64) { m.eq[band] = dB }
func (m *mockPlayer) ToggleMono()                    { m.mono = !m.mono }

func TestApplyPlayer(t *testing.T) {
	cfg := defaultConfig()
	cfg.Volume = -10
	cfg.Speed = 1.5
	cfg.EQ = [10]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	cfg.EQPreset = "" // Custom
	cfg.Mono = true

	p := &mockPlayer{}
	cfg.ApplyPlayer(p)

	if p.volume != -10 {
		t.Errorf("volume = %f, want -10", p.volume)
	}
	if p.speed != 1.5 {
		t.Errorf("speed = %f, want 1.5", p.speed)
	}
	for i, want := range cfg.EQ {
		if p.eq[i] != want {
			t.Errorf("eq[%d] = %f, want %f", i, p.eq[i], want)
		}
	}
	if !p.mono {
		t.Error("mono should be true")
	}
}

func TestApplyPlayerDefaultSpeed(t *testing.T) {
	cfg := defaultConfig()
	cfg.Speed = 1.0 // default, should NOT call SetSpeed

	p := &mockPlayer{}
	cfg.ApplyPlayer(p)

	if p.speed != 0 {
		t.Errorf("speed = %f, should not have been set for 1.0x", p.speed)
	}
}

func TestApplyPlayerWithPreset(t *testing.T) {
	cfg := defaultConfig()
	cfg.EQPreset = "Rock"
	cfg.EQ = [10]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

	p := &mockPlayer{}
	cfg.ApplyPlayer(p)

	// With a preset set (not Custom/""), individual EQ bands should NOT be applied
	for i, v := range p.eq {
		if v != 0 {
			t.Errorf("eq[%d] = %f, want 0 (preset should skip band apply)", i, v)
		}
	}
}

// Mock playlist for ApplyPlaylist tests
type mockPlaylist struct {
	repeatCycles int
	shuffled     bool
}

func (m *mockPlaylist) CycleRepeat()   { m.repeatCycles++ }
func (m *mockPlaylist) ToggleShuffle() { m.shuffled = !m.shuffled }

func TestApplyPlaylist(t *testing.T) {
	tests := []struct {
		name        string
		repeat      string
		shuffle     bool
		wantCycles  int
		wantShuffle bool
	}{
		{"off no shuffle", "off", false, 0, false},
		{"all", "all", false, 1, false},
		{"one", "one", false, 2, false},
		{"shuffle", "off", true, 0, true},
		{"all + shuffle", "all", true, 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultConfig()
			cfg.Repeat = tt.repeat
			cfg.Shuffle = tt.shuffle

			pl := &mockPlaylist{}
			cfg.ApplyPlaylist(pl)

			if pl.repeatCycles != tt.wantCycles {
				t.Errorf("repeat cycles = %d, want %d", pl.repeatCycles, tt.wantCycles)
			}
			if pl.shuffled != tt.wantShuffle {
				t.Errorf("shuffled = %v, want %v", pl.shuffled, tt.wantShuffle)
			}
		})
	}
}
