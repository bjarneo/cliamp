package main

import (
	"context"
	"testing"

	cli "github.com/urfave/cli/v3"

	"github.com/bjarneo/cliamp/config"
)

func TestInverseBoolFlags(t *testing.T) {
	tests := []struct {
		flag string
		get  func(config.Overrides) *bool
		want bool
	}{
		{"--shuffle", func(ov config.Overrides) *bool { return ov.Shuffle }, true},
		{"--no-shuffle", func(ov config.Overrides) *bool { return ov.Shuffle }, false},
		{"--mono", func(ov config.Overrides) *bool { return ov.Mono }, true},
		{"--no-mono", func(ov config.Overrides) *bool { return ov.Mono }, false},
		{"--auto-play", func(ov config.Overrides) *bool { return ov.Play }, true},
		{"--no-auto-play", func(ov config.Overrides) *bool { return ov.Play }, false},
		{"--simplified", func(ov config.Overrides) *bool { return ov.Simplified }, true},
		{"--no-simplified", func(ov config.Overrides) *bool { return ov.Simplified }, false},
		{"--help-bar", func(ov config.Overrides) *bool { return ov.HideHelpBar }, false},
		{"--no-help-bar", func(ov config.Overrides) *bool { return ov.HideHelpBar }, true},
		{"--expand-playlist", func(ov config.Overrides) *bool { return ov.ExpandPlaylist }, true},
		{"--no-expand-playlist", func(ov config.Overrides) *bool { return ov.ExpandPlaylist }, false},
		{"--low-power", func(ov config.Overrides) *bool { return ov.LowPower }, true},
		{"--no-low-power", func(ov config.Overrides) *bool { return ov.LowPower }, false},
	}

	for _, tt := range tests {
		t.Run(tt.flag, func(t *testing.T) {
			app := buildApp()
			var got config.Overrides
			app.Action = func(_ context.Context, c *cli.Command) error {
				var err error
				got, err = overridesFromFlags(c)
				return err
			}

			if err := app.Run(context.Background(), []string{"cliamp", tt.flag}); err != nil {
				t.Fatalf("Run: %v", err)
			}
			value := tt.get(got)
			if value == nil || *value != tt.want {
				t.Errorf("value = %v, want %t", value, tt.want)
			}
		})
	}
}
