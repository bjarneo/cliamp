package model

import tea "charm.land/bubbletea/v2"

// filterList holds the cursor, the scroll offset and the `/` filter of a list
// overlay. The theme picker, the visualizer picker, the keymap and the file
// browser embed it. While the list is filtered, cursor and scroll index into
// filtered, which holds the raw rows that match filter.
type filterList struct {
	cursor    int
	scroll    int
	filtering bool // the filter field takes the keys
	filter    string
	filtered  []int
	// savedCursor and savedScroll hold the place from before `/`, so that
	// leaving the filter with no query goes back to it.
	savedCursor int
	savedScroll int
}

// isFiltered reports whether the list shows only the rows in filtered.
func (l *filterList) isFiltered() bool {
	return l.filtering || l.filter != ""
}

// viewCount returns how many of n raw rows the list shows.
func (l *filterList) viewCount(n int) int {
	if l.isFiltered() {
		return len(l.filtered)
	}
	return n
}

// rawIndex maps a shown row to its index in a list of n raw rows.
func (l *filterList) rawIndex(view, n int) (int, bool) {
	if l.isFiltered() {
		if view < 0 || view >= len(l.filtered) {
			return 0, false
		}
		return l.filtered[view], true
	}
	if view < 0 || view >= n {
		return 0, false
	}
	return view, true
}

// shownRows returns the rows of items that l shows, in view order.
func shownRows[T any](l *filterList, items []T) []T {
	if !l.isFiltered() {
		return items
	}
	rows := make([]T, 0, len(l.filtered))
	for _, raw := range l.filtered {
		if raw >= 0 && raw < len(items) {
			rows = append(rows, items[raw])
		}
	}
	return rows
}

// recompute keeps the raw rows below n that match, in order, and moves the
// cursor to the first of them.
func (l *filterList) recompute(n int, match func(raw int) bool) {
	l.filtered = nil
	l.cursor, l.scroll = 0, 0
	for raw := range n {
		if match(raw) {
			l.filtered = append(l.filtered, raw)
		}
	}
}

// beginFilter saves the place in the list and gives the keys to an empty
// filter field. The caller then recomputes the view.
func (l *filterList) beginFilter() {
	l.savedCursor, l.savedScroll = l.cursor, l.scroll
	l.filtering = true
	l.filter = ""
}

// clearFilter drops the query and shows every row again.
func (l *filterList) clearFilter() {
	l.filtering = false
	l.filter = ""
	l.filtered = nil
}

// restorePlace moves the cursor and the scroll back to where `/` found them.
func (l *filterList) restorePlace() {
	l.cursor, l.scroll = l.savedCursor, l.savedScroll
}

// filterKey runs a key press while the filter field of l takes the keys. n
// is the raw row count, and recompute rebuilds the view after the query
// changes. It returns true when Down leaves the field on the first row, so
// that the caller can preview that row and scroll to it.
func (m *Model) filterKey(l *filterList, field string, msg tea.KeyPressMsg, n int, recompute func()) bool {
	switch msg.String() {
	case "esc":
		l.clearFilter()
		l.restorePlace()
		return false
	case "enter":
		l.filtering = false
		if l.filter == "" {
			l.restorePlace()
		}
		return false
	case "down":
		l.filtering = false
		if l.viewCount(n) == 0 {
			return false
		}
		l.cursor = 0
		return true
	case "backspace":
		if l.filter == "" {
			l.filtering = false
			l.restorePlace()
			return false
		}
	}

	if msg.Code == tea.KeySpace && msg.Text == "" {
		m.insertText(field, &l.filter, " ")
		recompute()
		return false
	}
	if m.editText(field, &l.filter, msg) {
		recompute()
	}
	return false
}
