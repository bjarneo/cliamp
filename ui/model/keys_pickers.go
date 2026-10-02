package model

import tea "charm.land/bubbletea/v2"

func (m *Model) handleThemeFilterKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.filterKey(&m.themePicker.filterList, "theme-picker-filter", msg, m.themeCount(), m.themePickerRecomputeFilter) {
		m.themePickerApply()
		m.themePickerMaybeAdjustScroll(m.effectivePlaylistVisible())
	}
	return nil
}

// handleThemeKey processes key presses while the theme picker is open.
func (m *Model) handleThemeKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.themePicker.filtering {
		return m.handleThemeFilterKey(msg)
	}

	visible := m.effectivePlaylistVisible()
	if stepListCursor(msg.String(), &m.themePicker.cursor, m.themePickerViewCount(), visible) {
		m.themePickerApply()
		m.themePickerMaybeAdjustScroll(visible)
		return nil
	}
	switch msg.String() {
	case "ctrl+x":
		m.toggleExpandedView()
		m.themePickerMaybeAdjustScroll(m.effectivePlaylistVisible())

	case "enter":
		m.themePickerSelect()

	case "/":
		m.themePicker.beginFilter()
		m.themePickerRecomputeFilter()
		return nil

	case "esc", "q", "t":
		m.themePickerCancel()
	}
	return nil
}

func (m *Model) handleVisPickerFilterKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.filterKey(&m.visPicker.filterList, "visualizer-picker-filter", msg, len(m.visPicker.modes), m.visPickerRecomputeFilter) {
		m.visPickerApply()
		m.visPickerMaybeAdjustScroll(m.effectivePlaylistVisible())
	}
	return nil
}

// handleVisPickerKey processes key presses while the visualizer picker is open.
func (m *Model) handleVisPickerKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.visPicker.filtering {
		return m.handleVisPickerFilterKey(msg)
	}

	// The apply can cross VisNone and resize the rows, so fit the scroll after it.
	if stepListCursor(msg.String(), &m.visPicker.cursor, m.visPickerViewCount(), m.effectivePlaylistVisible()) {
		m.visPickerApply()
		m.visPickerMaybeAdjustScroll(m.effectivePlaylistVisible())
		return nil
	}
	switch msg.String() {
	case "ctrl+x":
		m.toggleExpandedView()
		m.visPickerMaybeAdjustScroll(m.effectivePlaylistVisible())

	case "enter":
		m.visPickerSelect()

	case "/":
		m.visPicker.beginFilter()
		m.visPickerRecomputeFilter()
		return nil

	case "esc", "q", "ctrl+v":
		m.visPickerCancel()
	}
	return nil
}

func (m *Model) deviceMaybeAdjustScroll(visible int) {
	clampScroll(&m.devicePicker.cursor, &m.devicePicker.scroll, len(m.devicePicker.devices), visible)
}

// handleDeviceKey processes key presses while the audio device picker is open.
func (m *Model) handleDeviceKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+x":
		m.toggleExpandedView()
		m.deviceMaybeAdjustScroll(m.effectivePlaylistVisible())
	case "up", "k":
		if m.devicePicker.cursor > 0 {
			m.devicePicker.cursor--
		} else if len(m.devicePicker.devices) > 0 {
			m.devicePicker.cursor = len(m.devicePicker.devices) - 1
		}
		m.deviceMaybeAdjustScroll(m.effectivePlaylistVisible())
	case "down", "j":
		if m.devicePicker.cursor < len(m.devicePicker.devices)-1 {
			m.devicePicker.cursor++
		} else if len(m.devicePicker.devices) > 0 {
			m.devicePicker.cursor = 0
		}
		m.deviceMaybeAdjustScroll(m.effectivePlaylistVisible())
	case "enter":
		if len(m.devicePicker.devices) > 0 && m.devicePicker.cursor < len(m.devicePicker.devices) {
			dev := m.devicePicker.devices[m.devicePicker.cursor]
			m.devicePicker.visible = false
			return switchDeviceCmd(dev.Name)
		}
	case "esc", "d":
		m.devicePicker.visible = false
	}
	return nil
}
