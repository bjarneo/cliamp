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
		{"--mono", func(ov config.Overrides) *bool { return ov.Mono }, true},
		{"--no-mono", func(ov config.Overrides) *bool { return ov.Mono }, false},
		{"--expand-playlist", func(ov config.Overrides) *bool { return ov.ExpandPlaylist }, true},
		{"--no-expand-playlist", func(ov config.Overrides) *bool { return ov.ExpandPlaylist }, false},
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
