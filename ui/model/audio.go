package model

import (
	"strings"
	"time"
)

const (
	speedSaveDebounce = time.Second
	eqSaveDebounce    = time.Second
)

// SetEQPreset sets a built-in preset by name. Supplying bands selects the
// persistent Custom slot and uses name as its optional label.
func (m *Model) SetEQPreset(name string, bands *[10]float64) {
	m.eqCustomLabel = ""
	if bands != nil {
		m.eqPresetIdx = -1
		if name != "" && !strings.EqualFold(name, "Custom") {
			m.eqCustomLabel = name
		}
		m.applyEQBands(*bands)
		m.eqCustomBands = m.player.EQBands()
		return
	}

	// Check built-in presets first.
	for i, p := range eqPresets {
		if strings.EqualFold(p.Name, name) {
			m.eqPresetIdx = i
			m.applyEQPreset()
			return
		}
	}

	// "Custom" restores the saved curve. Other names keep the current bands and
	// use the name as a plugin-defined label.
	m.eqPresetIdx = -1
	if name == "" || strings.EqualFold(name, "Custom") {
		m.applyEQBands(m.eqCustomBands)
		return
	}
	m.eqCustomLabel = name
	m.eqCustomBands = m.player.EQBands()
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
	m.applyEQBands(eqPresets[m.eqPresetIdx].Bands)
}

func (m *Model) applyEQBands(bands [eqBandCount]float64) {
	for i, gain := range bands {
		m.player.SetEQBand(i, gain)
	}
}

func (m *Model) setCustomEQBand(band int, gain float64) {
	m.player.SetEQBand(band, gain)
	m.eqPresetIdx = -1
	m.eqCustomLabel = ""
	m.eqCustomBands = m.player.EQBands()
	m.scheduleEQSave()
}

func (m *Model) cycleEQPreset() {
	if m.eqPresetIdx >= len(eqPresets)-1 {
		m.eqPresetIdx = -1
		m.applyEQBands(m.eqCustomBands)
		return
	}
	m.eqPresetIdx++
	m.applyEQPreset()
}

// saveEQ persists the current EQ state (preset name and band values) to config.
func (m *Model) saveEQ() {
	_ = m.saveConfigString("eq_preset", m.EQPresetName())
	_ = m.saveConfigFloats("eq", m.eqCustomBands[:])
}

// saveSpeed persists the current playback speed to the config file.
func (m *Model) saveSpeed() {
	_ = m.saveConfigFloat("speed", m.player.Speed(), 2)
}

func (m *Model) changeSpeed(delta float64) {
	m.setSpeed(m.player.Speed() + delta)
}

// setSpeed changes the playback speed now and saves it after
// speedSaveDebounce, so a run of changes writes the config once.
func (m *Model) setSpeed(ratio float64) {
	m.player.SetSpeed(ratio)
	m.speedSaveAfter = speedSaveDebounce
}

// scheduleEQSave mirrors speed persistence: audio changes immediately while
// repeated cursor adjustments collapse into one config write.
func (m *Model) scheduleEQSave() {
	m.eqSaveAfter = eqSaveDebounce
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

func (m *Model) tickPendingEQSave(dt time.Duration) {
	if m.eqSaveAfter <= 0 {
		return
	}
	m.eqSaveAfter -= dt
	if m.eqSaveAfter > 0 {
		return
	}
	m.eqSaveAfter = 0
	m.saveEQ()
}

func (m *Model) flushPendingEQSave() {
	if m.eqSaveAfter <= 0 {
		return
	}
	m.eqSaveAfter = 0
	m.saveEQ()
}
