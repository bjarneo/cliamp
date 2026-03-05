package ui

import (
	"testing"
	"time"
)

func TestSetSeekStepLarge(t *testing.T) {
	t.Run("sets positive value", func(t *testing.T) {
		m := Model{}
		m.SetSeekStepLarge(45 * time.Second)
		if got, want := m.seekStepLarge, 45*time.Second; got != want {
			t.Fatalf("seekStepLarge = %v, want %v", got, want)
		}
	})

	t.Run("resets non-positive to default", func(t *testing.T) {
		tests := []time.Duration{0, -5 * time.Second}
		for _, in := range tests {
			m := Model{}
			m.SetSeekStepLarge(in)
			if got, want := m.seekStepLarge, 30*time.Second; got != want {
				t.Fatalf("SetSeekStepLarge(%v): seekStepLarge = %v, want %v", in, got, want)
			}
		}
	})

	t.Run("clamps too-small positive value", func(t *testing.T) {
		m := Model{}
		m.SetSeekStepLarge(5 * time.Second)
		if got, want := m.seekStepLarge, 6*time.Second; got != want {
			t.Fatalf("seekStepLarge = %v, want %v", got, want)
		}
	})
}
