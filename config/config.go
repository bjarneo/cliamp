// Package config handles loading user configuration from ~/.config/cliamp/config.toml.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// configPath returns the path to the config file.
func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "cliamp", "config.toml"), nil
}

// Config holds user preferences loaded from the config file.
type Config struct {
	Volume   float64     // dB, range [-30, +6]
	EQ       [10]float64 // per-band gain in dB, range [-12, +12]
	EQPreset string      // preset name, or "" for custom
	Repeat   string      // "off", "all", or "one"
	Shuffle  bool
	Mono     bool
	Theme    string // theme name, or "" for ANSI default
}

// Default returns a Config with sensible defaults.
func Default() Config {
	return Config{
		Repeat: "off",
	}
}

// Load reads the config file from ~/.config/cliamp/config.toml.
// Returns defaults if the file does not exist.
func Load() (Config, error) {
	cfg := Default()

	path, err := configPath()
	if err != nil {
		return cfg, nil
	}

	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cfg, nil
		}
		return cfg, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)

		switch key {
		case "volume":
			if v, err := strconv.ParseFloat(val, 64); err == nil {
				cfg.Volume = max(min(v, 6), -30)
			}
		case "repeat":
			val = strings.Trim(val, `"'`)
			switch strings.ToLower(val) {
			case "all", "one", "off":
				cfg.Repeat = strings.ToLower(val)
			}
		case "shuffle":
			cfg.Shuffle = val == "true"
		case "mono":
			cfg.Mono = val == "true"
		case "eq":
			cfg.EQ = parseEQ(val)
		case "eq_preset":
			cfg.EQPreset = strings.Trim(val, `"'`)
		case "theme":
			cfg.Theme = strings.Trim(val, `"'`)
		}
	}

	return cfg, scanner.Err()
}

// Save writes the config to ~/.config/cliamp/config.toml.
func Save(cfg Config) error {
	path, err := configPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	eqParts := make([]string, 10)
	for i, v := range cfg.EQ {
		eqParts[i] = strconv.FormatFloat(v, 'f', -1, 64)
	}

	content := fmt.Sprintf(`# CLIAMP configuration

# Default volume in dB (range: -30 to 6)
volume = %s

# Repeat mode: "off", "all", or "one"
repeat = "%s"

# Start with shuffle enabled
shuffle = %t

# Start with mono output (L+R downmix)
mono = %t

# Color theme name (e.g. "catppuccin", "dracula")
# Leave empty for default ANSI terminal colors
theme = "%s"

# EQ preset name (e.g. "Rock", "Jazz", "Classical", "Bass Boost")
# Leave empty or "Custom" to use the manual eq values below
eq_preset = "%s"

# 10-band EQ gains in dB (range: -12 to 12)
# Bands: 70Hz, 180Hz, 320Hz, 600Hz, 1kHz, 3kHz, 6kHz, 12kHz, 14kHz, 16kHz
# Only used when eq_preset is "Custom" or empty
eq = [%s]
`,
		strconv.FormatFloat(cfg.Volume, 'f', -1, 64),
		cfg.Repeat,
		cfg.Shuffle,
		cfg.Mono,
		cfg.Theme,
		cfg.EQPreset,
		strings.Join(eqParts, ", "),
	)

	return os.WriteFile(path, []byte(content), 0o644)
}

// PlayerConfig is the subset of player controls needed to apply config.
type PlayerConfig interface {
	SetVolume(db float64)
	SetEQBand(band int, dB float64)
	ToggleMono()
}

// PlaylistConfig is the subset of playlist controls needed to apply config.
type PlaylistConfig interface {
	CycleRepeat()
	ToggleShuffle()
}

// ApplyPlayer applies audio-engine settings from the config.
func (c Config) ApplyPlayer(p PlayerConfig) {
	p.SetVolume(c.Volume)
	if c.EQPreset == "" || c.EQPreset == "Custom" {
		for i, gain := range c.EQ {
			p.SetEQBand(i, gain)
		}
	}
	if c.Mono {
		p.ToggleMono()
	}
}

// ApplyPlaylist applies playlist-state settings from the config.
func (c Config) ApplyPlaylist(pl PlaylistConfig) {
	switch c.Repeat {
	case "all":
		pl.CycleRepeat() // off -> all
	case "one":
		pl.CycleRepeat() // off -> all
		pl.CycleRepeat() // all -> one
	}
	if c.Shuffle {
		pl.ToggleShuffle()
	}
}

// parseEQ parses a TOML-style array like [0, 1.5, -2, ...] into 10 bands.
func parseEQ(val string) [10]float64 {
	var bands [10]float64
	val = strings.Trim(val, "[]")
	parts := strings.Split(val, ",")
	for i, p := range parts {
		if i >= 10 {
			break
		}
		if v, err := strconv.ParseFloat(strings.TrimSpace(p), 64); err == nil {
			bands[i] = max(min(v, 12), -12)
		}
	}
	return bands
}
