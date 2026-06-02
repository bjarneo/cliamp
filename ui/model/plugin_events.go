package model

import (
	"cliamp/luaplugin"
)

// pluginEmitState is the last player/queue state emitted to plugins. It lets
// emitPluginEvents diff against the previous Update and fire an event only when
// a value actually changed, regardless of which code path mutated it.
type pluginEmitState struct {
	ready    bool
	shuffle  bool
	repeat   string
	volume   float64
	eqBands  [10]float64
	plCount  int
	plIndex  int
	queueLen int
}

// emitPlugin sends a single event to plugins, guarding the nil/has-hooks case
// so callers don't repeat it. Safe to call when no plugins are loaded.
func (m *Model) emitPlugin(event string, data map[string]any) {
	if m.luaMgr == nil || !m.luaMgr.HasHook(event) {
		return
	}
	m.luaMgr.Emit(event, data)
}

// emitPluginEvents diffs current player/queue state against the last emission
// and fires the corresponding delta events. Called once per Update (after the
// message is handled) so every mutation path is covered from a single site.
//
// Each branch is gated on HasHook so the live-state reads (some of which take
// the speaker lock, e.g. Volume/EQBands) only happen when a plugin is actually
// listening for that event.
func (m *Model) emitPluginEvents() {
	if m.luaMgr == nil || m.pluginEmit == nil {
		return
	}
	pe := m.pluginEmit

	// First call: snapshot current state without emitting so a freshly started
	// app doesn't fire spurious "changed" events.
	if !pe.ready {
		pe.shuffle = m.playlist.Shuffled()
		pe.repeat = m.playlist.Repeat().String()
		pe.volume = m.player.Volume()
		pe.eqBands = m.player.EQBands()
		pe.plCount = m.playlist.Len()
		pe.plIndex = m.playlist.Index()
		pe.queueLen = m.playlist.QueueLen()
		pe.ready = true
		return
	}

	if m.luaMgr.HasHook(luaplugin.EventPlayerMode) {
		shuffle := m.playlist.Shuffled()
		repeat := m.playlist.Repeat().String()
		if shuffle != pe.shuffle || repeat != pe.repeat {
			pe.shuffle, pe.repeat = shuffle, repeat
			m.luaMgr.Emit(luaplugin.EventPlayerMode, map[string]any{
				"shuffle": shuffle,
				"repeat":  repeat,
			})
		}
	}

	if m.luaMgr.HasHook(luaplugin.EventPlayerVolume) {
		if v := m.player.Volume(); v != pe.volume {
			pe.volume = v
			m.luaMgr.Emit(luaplugin.EventPlayerVolume, map[string]any{"db": v})
		}
	}

	if m.luaMgr.HasHook(luaplugin.EventPlayerEQ) {
		if bands := m.player.EQBands(); bands != pe.eqBands {
			pe.eqBands = bands
			m.luaMgr.Emit(luaplugin.EventPlayerEQ, map[string]any{
				"bands":  bands[:],
				"preset": m.EQPresetName(),
			})
		}
	}

	if m.luaMgr.HasHook(luaplugin.EventQueueChange) {
		count, index, qlen := m.playlist.Len(), m.playlist.Index(), m.playlist.QueueLen()
		if count != pe.plCount || index != pe.plIndex || qlen != pe.queueLen {
			pe.plCount, pe.plIndex, pe.queueLen = count, index, qlen
			m.luaMgr.Emit(luaplugin.EventQueueChange, map[string]any{
				"count":  count,
				"index":  index,
				"queued": qlen,
			})
		}
	}
}
