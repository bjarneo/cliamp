package model

import (
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/luaplugin"
)

// LuaInputMsg asks the UI to show a text input requested by a plugin.
// OnSubmit runs with the text the user typed; OnSelect runs with the 0-based
// index of a live result; OnCancel runs on Esc.
type LuaInputMsg struct {
	Token       uint64
	Title       string
	Placeholder string
	Value       string
	Suggestions []luaplugin.SelectOption
	OnChange    func(text string)
	OnSubmit    func(text string)
	OnSelect    func(index int)
	OnCancel    func()
}

// LuaInputSuggestionsMsg replaces the suggestions under the open input.
// Live rows are search results: Enter selects one instead of submitting the
// text.
type LuaInputSuggestionsMsg struct {
	Token   uint64
	Options []luaplugin.SelectOption
	Live    bool
	// Query is the text the results belong to. QuerySet distinguishes an
	// omitted query from an explicitly empty one.
	Query    string
	QuerySet bool
}

// luaInputState is the open plugin input.
type luaInputState struct {
	token       uint64
	title       string
	placeholder string
	value       string
	cursor      int
	suggestions []luaplugin.SelectOption
	filtered    []luaplugin.SelectOption
	live        bool
	choice      int
	onChange    func(string)
	onSubmit    func(string)
	onSelect    func(int)
	onCancel    func()
}

func (m *Model) openLuaInput(title, placeholder, value string, suggestions []luaplugin.SelectOption, onChange, onSubmit func(string), onSelect func(int), onCancel func()) {
	m.luaInput = &luaInputState{
		title:       title,
		placeholder: placeholder,
		value:       value,
		cursor:      len(value),
		suggestions: suggestions,
		onChange:    onChange,
		onSubmit:    onSubmit,
		onSelect:    onSelect,
		onCancel:    onCancel,
	}
	m.luaInput.filter()
	m.textInput.begin("lua-input", value)
	m.textInput.cursor = len(value)
}

// filter keeps command suggestions that match a prefix. Live search results
// are already for the current text, so they are never filtered locally.
func (input *luaInputState) filter() {
	if input.live {
		input.filtered = input.suggestions
	} else {
		value := strings.TrimSpace(input.value)
		input.filtered = nil
		switch {
		case value == "":
			input.filtered = input.suggestions
		case strings.HasPrefix(value, "/") && !strings.ContainsAny(value, " \t"):
			prefix := strings.ToLower(value)
			for _, suggestion := range input.suggestions {
				if strings.HasPrefix(strings.ToLower(suggestion.Title), prefix) {
					input.filtered = append(input.filtered, suggestion)
				}
			}
		}
	}
	if input.choice >= len(input.filtered) {
		input.choice = max(0, len(input.filtered)-1)
	}
	if input.choice < 0 {
		input.choice = 0
	}
}

// handleLuaInputKey drives the open input. It returns true when the key was
// consumed.
func (m *Model) handleLuaInputKey(msg tea.KeyPressMsg) bool {
	input := m.luaInput
	if input == nil {
		return false
	}
	switch msg.String() {
	case "esc":
		m.luaInput = nil
		if input.onCancel != nil {
			input.onCancel()
		}
	case "up":
		if len(input.filtered) > 0 {
			input.choice = (input.choice - 1 + len(input.filtered)) % len(input.filtered)
		}
	case "down":
		if len(input.filtered) > 0 {
			input.choice = (input.choice + 1) % len(input.filtered)
		}
	case "tab":
		if !input.live && len(input.filtered) > 0 {
			input.value = input.filtered[input.choice].Title
			if strings.HasPrefix(input.value, "/") && !strings.HasSuffix(input.value, " ") {
				input.value += " "
			}
			input.cursor = len(input.value)
			m.textInput.begin("lua-input", input.value)
			m.textInput.cursor = input.cursor
			input.afterEdit()
		}
	case "enter":
		if input.live && len(input.filtered) > 0 {
			index := input.choice
			m.luaInput = nil
			if input.onSelect != nil {
				input.onSelect(index)
			}
			return true
		}
		text := strings.TrimSpace(input.value)
		if text == "" && len(input.filtered) > 0 {
			text = input.filtered[input.choice].Title
		}
		if text == "" {
			return true
		}
		m.luaInput = nil
		if input.onSubmit != nil {
			input.onSubmit(text)
		}
	default:
		before := input.value
		if m.editText("lua-input", &input.value, msg) {
			input.cursor = m.textInput.cursor
			if input.value != before {
				input.afterEdit()
			}
		}
	}
	return true
}

// afterEdit applies the state changes that every edit shares, whether it came
// from a key or a paste.
func (input *luaInputState) afterEdit() {
	// A live result belongs to the previous text. Clear it until the plugin
	// sends the results for this query.
	if input.live {
		input.live = false
		input.suggestions = nil
		input.filtered = nil
		input.choice = 0
	} else {
		input.filter()
	}
	if input.onChange != nil {
		input.onChange(input.value)
	}
}

// inputWindow shows the part of value around cursor that fits in width cells.
// The cursor marker takes one cell, so the text budget is width-1.
func inputWindow(value string, cursor, width int) string {
	if width < 1 {
		width = 1
	}
	runes := []rune(value)
	cursorRune := utf8.RuneCountInString(value[:min(cursor, len(value))])
	if cursorRune > len(runes) {
		cursorRune = len(runes)
	}
	budget := width - 1
	start, used := cursorRune, 0
	for start > 0 {
		w := lipgloss.Width(string(runes[start-1]))
		if used+w > budget {
			break
		}
		start--
		used += w
	}
	end := cursorRune
	for end < len(runes) {
		w := lipgloss.Width(string(runes[end]))
		if used+w > budget {
			break
		}
		end++
		used += w
	}
	var b strings.Builder
	b.WriteString(string(runes[start:cursorRune]))
	b.WriteString("▏")
	b.WriteString(string(runes[cursorRune:end]))
	return b.String()
}

// inputRows is the number of lines one option needs in the input.
func imageLines(image string) []string {
	if strings.TrimSpace(image) == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(image, "\n"), "\n")
}

func inputRows(option luaplugin.SelectOption) int {
	rows := len(imageLines(option.Image)) + 1
	if option.Subtitle != "" {
		rows++
	}
	if option.Description != "" {
		rows++
	}
	return rows
}

// renderLuaInput draws the input and its command menu or live results as a
// centered box.
func (m Model) renderLuaInput() string {
	input := m.luaInput
	if input == nil {
		return ""
	}
	width := max(40, lipgloss.Width(input.title)+4)
	width = max(width, lipgloss.Width(input.placeholder)+8)
	for _, suggestion := range input.filtered {
		width = max(width, lipgloss.Width(oneLine(suggestion.Title))+6)
		width = max(width, lipgloss.Width(oneLine(suggestion.Subtitle))+6)
		width = max(width, lipgloss.Width(oneLine(suggestion.Description))+6)
		for _, line := range imageLines(suggestion.Image) {
			width = max(width, lipgloss.Width(line)+4)
		}
	}
	width = min(width, max(20, m.layout.frameWidth-4))

	shown := inputWindow(input.value, input.cursor, width-4)
	if strings.TrimSpace(input.value) == "" && input.placeholder != "" {
		shown = dimStyle.Render(truncate(input.placeholder, width-4))
	}

	var b strings.Builder
	b.WriteString(truncate(input.title, width-2))
	b.WriteString("\n\n")
	b.WriteString("> " + truncate(shown, width-4))
	if len(input.filtered) > 0 {
		b.WriteString("\n")
		if input.live {
			m.renderLiveInputRows(&b, input, width)
		} else {
			visible := min(5, len(input.filtered))
			start := 0
			if input.choice >= visible {
				start = input.choice - visible + 1
			}
			for i := start; i < start+visible; i++ {
				suggestion := input.filtered[i]
				prefix := "  "
				if i == input.choice {
					prefix = "▸ "
				}
				line := prefix + suggestion.Title
				if suggestion.Description != "" {
					line += "  " + dimStyle.Render(suggestion.Description)
				}
				b.WriteString("\n" + truncate(line, width-4))
			}
			if start+visible < len(input.filtered) {
				b.WriteString("\n" + dimStyle.Render("  ↓ more"))
			}
		}
	}
	b.WriteString("\n\n")
	if input.live {
		b.WriteString(dimStyle.Render(truncate("  enter select   esc cancel", width-4)))
	} else {
		b.WriteString(dimStyle.Render(truncate("  tab complete   enter   esc", width-4)))
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1).
		Width(width).
		Render(b.String())
}

// renderLiveInputRows writes the search rows under the input. The window
// keeps the selected row visible and counts the lines each row occupies.
func (m Model) renderLiveInputRows(b *strings.Builder, input *luaInputState, width int) {
	maxRows := max(3, m.height-14)
	start := max(0, min(input.choice, len(input.filtered)-1))
	rows := inputRows(input.filtered[start])
	for start > 0 && rows+inputRows(input.filtered[start-1]) <= maxRows {
		start--
		rows += inputRows(input.filtered[start])
	}
	end := start
	rows = 0
	for end < len(input.filtered) && rows+inputRows(input.filtered[end]) <= maxRows {
		rows += inputRows(input.filtered[end])
		end++
	}
	if end <= input.choice {
		end = min(len(input.filtered), input.choice+1)
		if start > input.choice {
			start = input.choice
		}
	}
	if start > 0 {
		b.WriteString(dimStyle.Render("  ↑ more"))
		b.WriteString("\n")
	}
	for i := start; i < end; i++ {
		option := input.filtered[i]
		if i > start {
			b.WriteString("\n")
		}
		lines := imageLines(option.Image)
		if budget := maxRows - (inputRows(option) - len(lines)); len(lines) > budget {
			lines = lines[:max(0, budget)]
		}
		for _, line := range lines {
			b.WriteString("  " + line + imageResetSuffix() + "\n")
		}
		marker := "  "
		if i == input.choice {
			marker = "▸ "
		}
		b.WriteString(marker + truncate(oneLine(option.Title), width-6))
		if option.Subtitle != "" {
			b.WriteString("\n  " + dimStyle.Render(truncate(oneLine(option.Subtitle), width-6)))
		}
		if option.Description != "" {
			b.WriteString("\n  " + dimStyle.Render(truncate(oneLine(option.Description), width-6)))
		}
	}
	if end < len(input.filtered) {
		b.WriteString("\n" + dimStyle.Render("  ↓ more"))
	}
}
