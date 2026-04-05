package model

import (
	"fmt"
	"strconv"
	"strings"
	"time"

)

const speedSaveDebounce = time.Second

// SetEQPreset sets the preset by name. If it matches a built-in preset,
// those bands are applied. Otherwise the name is used as a custom label.
// If bands is non-nil, they are applied regardless of whether the name matches.
func (m *Model) SetEQPreset(name string, bands *[10]float64) {
	m.eqCustomLabel = ""

	// Check built-in presets first.
	for i, p := range eqPresets {
		if strings.EqualFold(p.Name, name) {
			m.eqPresetIdx = i
			if bands != nil {
				for j, gain := range bands {
					m.player.SetEQBand(j, gain)
				}
			} else {
				m.applyEQPreset()
			}
			return
		}
	}

	// Custom label — set bands if provided, otherwise keep current.
	m.eqPresetIdx = -1
	m.eqCustomLabel = name
	if bands != nil {
		for i, gain := range bands {
			m.player.SetEQBand(i, gain)
		}
	}
}

// EQPresetName returns the current preset name, or "Custom".
func (m Model) EQPresetName() string {
	if m.eqPresetIdx >= 0 && m.eqPresetIdx < len(eqPresets) {
		return eqPresets[m.eqPresetIdx].Name
	}
	if m.eqCustomLabel != "" {
		return m.eqCustomLabel
	}
	return "Custom"
}

// applyEQPreset writes the current preset's bands to the player.
func (m *Model) applyEQPreset() {
	if m.eqPresetIdx < 0 || m.eqPresetIdx >= len(eqPresets) {
		return
	}
	bands := eqPresets[m.eqPresetIdx].Bands
	for i, gain := range bands {
		m.player.SetEQBand(i, gain)
	}
}

// saveEQ persists the current EQ state (preset name and band values) to config.
func (m *Model) saveEQ() {
	name := m.EQPresetName()
	if err := m.configSaver.Save("eq_preset", fmt.Sprintf("%q", name)); err != nil {
		m.status.Showf(statusTTLDefault, "Config save failed: %s", err)
	}
	bands := m.player.EQBands()
	parts := make([]string, len(bands))
	for i, g := range bands {
		parts[i] = strconv.FormatFloat(g, 'f', -1, 64)
	}
	eqVal := "[" + strings.Join(parts, ", ") + "]"
	if err := m.configSaver.Save("eq", eqVal); err != nil {
		m.status.Showf(statusTTLDefault, "Config save failed: %s", err)
	}
}

// saveSpeed persists the current playback speed to the config file.
func (m *Model) saveSpeed() {
	speed := m.player.Speed()
	if err := m.configSaver.Save("speed", fmt.Sprintf("%.2f", speed)); err != nil {
		m.status.Showf(statusTTLDefault, "Config save failed: %s", err)
	}
}

func (m *Model) changeSpeed(delta float64) {
	m.player.SetSpeed(m.player.Speed() + delta)
	m.speedSaveAfter = speedSaveDebounce
}

func (m *Model) tickPendingSpeedSave(dt time.Duration) {
	if m.speedSaveAfter <= 0 {
		return
	}
	m.speedSaveAfter -= dt
	if m.speedSaveAfter > 0 {
		return
	}
	m.speedSaveAfter = 0
	m.saveSpeed()
}

func (m *Model) flushPendingSpeedSave() {
	if m.speedSaveAfter <= 0 {
		return
	}
	m.speedSaveAfter = 0
	m.saveSpeed()
}
