package config

import "math"

// Overrides holds CLI flag values. Nil pointers mean "not set".
type Overrides struct {
	Volume          *float64
	Shuffle         *bool
	Repeat          *string
	Mono            *bool
	Provider        *string
	Theme           *string
	Visualizer      *string
	EQPreset        *string
	SampleRate      *int
	BufferMs        *int
	ResampleQuality *int
	BitDepth        *int
	Play            *bool
	Simplified      *bool
	HideHelpBar     *bool
	Expanded        *bool
	AudioDevice     *string
	Playlist        *string
	LogLevel        *string
	LowPower        *bool
	ExpandPlaylist  *bool
}

// Apply merges non-nil overrides into cfg and clamps the result.
func (o Overrides) Apply(cfg *Config) {
	if o.Volume != nil {
		if math.IsNaN(*o.Volume) || math.IsInf(*o.Volume, 0) {
			cfg.Volume = defaultConfig().Volume
		} else {
			cfg.Volume = *o.Volume
		}
	}
	if o.Shuffle != nil {
		cfg.Shuffle = *o.Shuffle
	}
	if o.Repeat != nil {
		cfg.Repeat = *o.Repeat
	}
	if o.Mono != nil {
		cfg.Mono = *o.Mono
	}
	if o.Provider != nil {
		cfg.Provider = *o.Provider
	}
	if o.Theme != nil {
		cfg.Theme = *o.Theme
	}
	if o.Visualizer != nil {
		cfg.Visualizer = *o.Visualizer
	}
	if o.EQPreset != nil {
		cfg.EQPreset = *o.EQPreset
	}
	if o.SampleRate != nil {
		cfg.SampleRate = *o.SampleRate
	}
	if o.BufferMs != nil {
		cfg.BufferMs = *o.BufferMs
	}
	if o.ResampleQuality != nil {
		cfg.ResampleQuality = *o.ResampleQuality
	}
	if o.BitDepth != nil {
		cfg.BitDepth = *o.BitDepth
	}
	if o.Simplified != nil {
		cfg.Simplified = *o.Simplified
	}
	if o.HideHelpBar != nil {
		cfg.HideHelpBar = *o.HideHelpBar
	}
	if o.Expanded != nil {
		cfg.Expanded = *o.Expanded
	}
	if o.Play != nil {
		cfg.AutoPlay = *o.Play
	}
	if o.AudioDevice != nil {
		cfg.AudioDevice = *o.AudioDevice
	}
	if o.Playlist != nil {
		cfg.Playlist = *o.Playlist
	}
	if o.LogLevel != nil {
		cfg.LogLevel = *o.LogLevel
	}
	if o.LowPower != nil {
		cfg.LowPower = *o.LowPower
		if !*o.LowPower && cfg.lowPowerClamped {
			if o.Visualizer == nil {
				cfg.Visualizer = cfg.lowPowerSavedVis
			}
			cfg.lowPowerSavedVis = ""
			cfg.lowPowerClamped = false
		}
	}
	if o.ExpandPlaylist != nil {
		cfg.YouTubeMusic.ExpandPlaylist = o.ExpandPlaylist
	}
	cfg.clamp()
	// An explicit CLI --visualizer overrides low_power from config.toml unless
	// --low-power was also passed on the CLI.
	if o.Visualizer != nil && (o.LowPower == nil || !*o.LowPower) {
		cfg.Visualizer = *o.Visualizer
	}
}
