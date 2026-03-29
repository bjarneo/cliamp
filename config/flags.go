package config

import (
	"fmt"
	"strconv"
	"strings"
)

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
	cfg.clamp()
}

// ParseFlags parses CLI arguments into an action string, overrides, and
// positional args. It handles flags intermixed with positional arguments
// and correctly treats negative numbers as flag values rather than flags.
//
// Returned action is one of "help", "version", "upgrade", or "" (run).
func ParseFlags(args []string) (action string, ov Overrides, positional []string, err error) {
	// Subcommand: cliamp plugins [list|install|remove] [args...]
	if len(args) > 0 && args[0] == "plugins" {
		if len(args) == 1 {
			return "plugins", ov, nil, nil
		}
		return "plugins-" + args[1], ov, args[2:], nil
	}

	i := 0
	for i < len(args) {
		arg := args[i]

		// Non-flag argument → positional.
		if !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
			i++
			continue
		}

		switch arg {
		// Action flags — return immediately.
		case "--help", "-h":
			return "help", ov, nil, nil
		case "--version", "-v":
			return "version", ov, nil, nil
		case "--upgrade":
			return "upgrade", ov, nil, nil

		// Boolean flags.
		case "--shuffle":
			ov.Shuffle = ptrBool(true)
		case "--mono":
			ov.Mono = ptrBool(true)
		case "--no-mono":
			ov.Mono = ptrBool(false)
		case "--auto-play":
			ov.Play = ptrBool(true)
		case "--compact":
			ov.Compact = ptrBool(true)
		// Key-value flags.
		case "--provider":
			v, e := requireNextString(args, &i, arg)
			if e != nil {
				return "", ov, nil, e
			}
			v = strings.ToLower(v)
			switch v {
			case "radio", "navidrome", "spotify", "yt", "youtube", "ytmusic":
			default:
				return "", ov, nil, fmt.Errorf("flag --provider value must be radio, navidrome, spotify, yt, youtube, or ytmusic (got %q)", v)
			}
			ov.Provider = &v
		case "--volume":
			v, e := requireNextFloat64(args, &i, arg)
			if e != nil {
				return "", ov, nil, e
			}
			ov.Volume = &v
		case "--repeat":
			v, e := requireNextString(args, &i, arg)
			if e != nil {
				return "", ov, nil, e
			}
			v = strings.ToLower(v)
			switch v {
			case "off", "all", "one":
			default:
				return "", ov, nil, fmt.Errorf("flag --repeat value must be off, all, or one (got %q)", v)
			}
			ov.Repeat = &v
		case "--theme":
			v, e := requireNextString(args, &i, arg)
			if e != nil {
				return "", ov, nil, e
			}
			ov.Theme = &v
		case "--visualizer":
			v, e := requireNextString(args, &i, arg)
			if e != nil {
				return "", ov, nil, e
			}
			ov.Visualizer = &v
		case "--eq-preset":
			v, e := requireNextString(args, &i, arg)
			if e != nil {
				return "", ov, nil, e
			}
			ov.EQPreset = &v
		case "--sample-rate":
			v, e := requireNextInt(args, &i, arg)
			if e != nil {
				return "", ov, nil, e
			}
			ov.SampleRate = &v
		case "--buffer-ms":
			v, e := requireNextInt(args, &i, arg)
			if e != nil {
				return "", ov, nil, e
			}
			ov.BufferMs = &v
		case "--resample-quality":
			v, e := requireNextInt(args, &i, arg)
			if e != nil {
				return "", ov, nil, e
			}
			ov.ResampleQuality = &v
		case "--bit-depth":
			v, e := requireNextInt(args, &i, arg)
			if e != nil {
				return "", ov, nil, e
			}
			ov.BitDepth = &v

		default:
			return "", ov, nil, fmt.Errorf("unknown flag: %s", arg)
		}
		i++
	}
	return "", ov, positional, nil
}

// requireNextFloat64 consumes the next arg as a float64 value.
// It advances *idx past the consumed value. Handles negative numbers.
func requireNextFloat64(args []string, idx *int, flag string) (float64, error) {
	if *idx+1 >= len(args) {
		return 0, fmt.Errorf("flag %s requires a value", flag)
	}
	*idx++
	v, err := strconv.ParseFloat(args[*idx], 64)
	if err != nil {
		return 0, fmt.Errorf("flag %s: invalid number %q", flag, args[*idx])
	}
	return v, nil
}

// requireNextString consumes the next arg as a string value.
func requireNextString(args []string, idx *int, flag string) (string, error) {
	if *idx+1 >= len(args) {
		return "", fmt.Errorf("flag %s requires a value", flag)
	}
	*idx++
	return args[*idx], nil
}

// requireNextInt consumes the next arg as an int value.
func requireNextInt(args []string, idx *int, flag string) (int, error) {
	if *idx+1 >= len(args) {
		return 0, fmt.Errorf("flag %s requires a value", flag)
	}
	*idx++
	v, err := strconv.Atoi(args[*idx])
	if err != nil {
		return 0, fmt.Errorf("flag %s: invalid integer %q", flag, args[*idx])
	}
	return v, nil
}

func ptrBool(v bool) *bool { return &v }
