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
	"time"
)

// configPath returns the path to the config file.
func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "cliamp", "config.toml"), nil
}

// NavidromeConfig holds credentials for a Navidrome/Subsonic server.
// All three fields must be non-empty for a client to be constructed.
type NavidromeConfig struct {
	URL              string // e.g. "https://music.example.com"
	User             string
	Password         string
	BrowseSort       string // album browse sort order, e.g. "alphabeticalByName"
	ScrobbleDisabled bool   // true only when "scrobble = false" is explicitly set
}

// IsSet reports whether all three Navidrome credentials are present.
func (n NavidromeConfig) IsSet() bool {
	return n.URL != "" && n.User != "" && n.Password != ""
}

// ScrobbleEnabled reports whether scrobbling is active.
// Scrobbling is opt-out: it is enabled unless "scrobble = false" is explicitly set.
func (n NavidromeConfig) ScrobbleEnabled() bool {
	return !n.ScrobbleDisabled
}

// Config holds user preferences loaded from the config file.
type Config struct {
	Volume          float64     // dB, range [-30, +6]
	EQ              [10]float64 // per-band gain in dB, range [-12, +12]
	EQPreset        string      // preset name, or "" for custom
	Repeat          string      // "off", "all", or "one"
	Shuffle         bool
	Mono            bool
	SeekStepLarge   int             // seconds for Shift+Left/Right seek jumps
	Theme           string          // theme name, or "" for ANSI default
	Visualizer      string          // visualizer mode name, or "" for default (Bars)
	SampleRate      int             // output sample rate: 22050, 44100, 48000, 96000, 192000
	BufferMs        int             // speaker buffer in milliseconds (50–500)
	ResampleQuality int             // beep resample quality factor (1–4)
	BitDepth        int             // PCM bit depth for FFmpeg output: 16 or 32
	Navidrome       NavidromeConfig // optional Navidrome/Subsonic server credentials
}

// Default returns a Config with sensible defaults.
func Default() Config {
	return Config{
		Repeat:          "off",
		SeekStepLarge:   30,
		SampleRate:      44100,
		BufferMs:        100,
		ResampleQuality: 4,
		BitDepth:        16,
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
	section := "" // current [section] header, empty = top-level
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Section header: [navidrome]
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(line[1 : len(line)-1])
			continue
		}

		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)

		switch section {
		case "navidrome":
			switch key {
			case "url":
				cfg.Navidrome.URL = strings.Trim(val, `"'`)
			case "user":
				cfg.Navidrome.User = strings.Trim(val, `"'`)
			case "password":
				cfg.Navidrome.Password = strings.Trim(val, `"'`)
			case "browse_sort":
				cfg.Navidrome.BrowseSort = strings.Trim(val, `"'`)
			case "scrobble":
				// Opt-out: only mark disabled when the value is explicitly "false".
				cfg.Navidrome.ScrobbleDisabled = strings.ToLower(val) == "false"
			}
		default:
			switch key {
			case "volume":
				if v, err := strconv.ParseFloat(val, 64); err == nil {
					cfg.Volume = v
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
			case "seek_large_step_sec":
				if v, err := strconv.Atoi(val); err == nil {
					cfg.SeekStepLarge = v
				}
			case "eq":
				cfg.EQ = parseEQ(val)
			case "eq_preset":
				cfg.EQPreset = strings.Trim(val, `"'`)
			case "theme":
				cfg.Theme = strings.Trim(val, `"'`)
			case "visualizer":
				cfg.Visualizer = strings.Trim(val, `"'`)
			case "sample_rate":
				if v, err := strconv.Atoi(val); err == nil {
					cfg.SampleRate = v
				}
			case "buffer_ms":
				if v, err := strconv.Atoi(val); err == nil {
					cfg.BufferMs = v
				}
			case "resample_quality":
				if v, err := strconv.Atoi(val); err == nil {
					cfg.ResampleQuality = v
				}
			case "bit_depth":
				if v, err := strconv.Atoi(val); err == nil {
					cfg.BitDepth = v
				}
			}
		}
	}

	cfg.clamp()
	return cfg, scanner.Err()
}

// Save updates only the given key in the existing config file, preserving
// all other content, comments, and formatting. If the key doesn't exist,
// it is appended. If no config file exists, one is created with just that key.
func Save(key, value string) error {
	path, err := configPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	line := fmt.Sprintf("%s = %s", key, value)

	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return os.WriteFile(path, []byte(line+"\n"), 0o644)
	}

	// Scan existing lines and replace the matching key in-place,
	// but only in the top-level scope (before any [section] header).
	lines := strings.Split(string(data), "\n")
	found := false
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Stop searching once we hit a section header — the key
		// belongs in the top-level scope only.
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			break
		}
		k, _, ok := strings.Cut(trimmed, "=")
		if ok && strings.TrimSpace(k) == key {
			lines[i] = line
			found = true
			break
		}
	}
	if !found {
		// Insert before the first section header to keep top-level keys together.
		inserted := false
		for i, l := range lines {
			trimmed := strings.TrimSpace(l)
			if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
				lines = append(lines[:i], append([]string{line}, lines[i:]...)...)
				inserted = true
				break
			}
		}
		if !inserted {
			lines = append(lines, line)
		}
	}

	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}

// SaveNavidromeSort persists the given album browse sort type to the
// [navidrome] section of the config file. It rewrites the browse_sort key
// in-place, or appends it after the [navidrome] section if not present.
// If no [navidrome] section exists, one is appended along with the key.
func SaveNavidromeSort(sortType string) error {
	path, err := configPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	line := fmt.Sprintf("browse_sort = %q", sortType)

	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		// No file: create with section + key.
		return os.WriteFile(path, []byte("[navidrome]\n"+line+"\n"), 0o644)
	}

	lines := strings.Split(string(data), "\n")

	// Try to replace an existing browse_sort inside [navidrome].
	inNavidrome := false
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inNavidrome = strings.ToLower(trimmed[1:len(trimmed)-1]) == "navidrome"
			continue
		}
		if inNavidrome {
			k, _, ok := strings.Cut(trimmed, "=")
			if ok && strings.TrimSpace(k) == "browse_sort" {
				lines[i] = line
				return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
			}
		}
	}

	// Key not found: append after the last line in the [navidrome] section,
	// or append a new [navidrome] section at the end.
	inNavidrome = false
	insertAt := -1
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inNavidrome && insertAt >= 0 {
				break // we've moved past [navidrome]
			}
			inNavidrome = strings.ToLower(trimmed[1:len(trimmed)-1]) == "navidrome"
		}
		if inNavidrome {
			insertAt = i
		}
	}

	if insertAt >= 0 {
		// Insert after the last line we saw inside [navidrome].
		tail := append([]string{line}, lines[insertAt+1:]...)
		lines = append(lines[:insertAt+1], tail...)
	} else {
		// No [navidrome] section found: append one.
		lines = append(lines, "[navidrome]", line)
	}

	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
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

// SeekStepLargeDuration returns the configured Shift+Left/Right seek jump.
func (c Config) SeekStepLargeDuration() time.Duration {
	return time.Duration(c.SeekStepLarge) * time.Second
}

// clamp constrains all Config fields to their valid ranges.
func (c *Config) clamp() {
	c.Volume = max(min(c.Volume, 6), -30)
	c.SeekStepLarge = max(min(c.SeekStepLarge, 600), 6)
	c.SampleRate = clampSampleRate(c.SampleRate)
	c.BufferMs = max(min(c.BufferMs, 500), 50)
	c.ResampleQuality = max(min(c.ResampleQuality, 4), 1)
	c.BitDepth = clampBitDepth(c.BitDepth)
}

// clampSampleRate returns the nearest valid sample rate from the allowed set.
func clampSampleRate(v int) int {
	allowed := []int{22050, 44100, 48000, 96000, 192000}
	best := allowed[0]
	bestDist := abs(v - best)
	for _, a := range allowed[1:] {
		if d := abs(v - a); d < bestDist {
			best = a
			bestDist = d
		}
	}
	return best
}

// clampBitDepth returns the nearest valid bit depth (16 or 32).
func clampBitDepth(v int) int {
	if v >= 24 {
		return 32
	}
	return 16
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
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
