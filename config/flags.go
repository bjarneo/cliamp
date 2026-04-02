package config

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
	Compact         *bool
	AudioDevice     *string
}

// Apply merges non-nil overrides into cfg and clamps the result.
func (o Overrides) Apply(cfg *Config) {
	if o.Volume != nil {
		cfg.Volume = *o.Volume
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
	if o.Compact != nil {
		cfg.Compact = *o.Compact
	}
	if o.Play != nil {
		cfg.AutoPlay = *o.Play
	}
	if o.AudioDevice != nil {
		cfg.AudioDevice = *o.AudioDevice
	}
	cfg.clamp()
}
