package model

// The playlist view and cursor use stored track indices. Shuffle changes
// playback order only, keeping album groups and track numbers stable.

// shuffleMoveWarning tells the user why Shift+Up and Shift+Down do not move
// a track while shuffle is on.
const shuffleMoveWarning = "Turn off shuffle to move tracks"
const shuffleReverseWarning = "Turn off shuffle to reverse the playlist"
const pagingReverseWarning = "Wait for the playlist to finish loading"

// plCursorRow returns the view row of the playlist cursor.
func (m Model) plCursorRow() int {
	return m.plCursor
}

// setPlCursorRow puts the playlist cursor on the track at the view row and
// scrolls the view to show the cursor.
func (m *Model) setPlCursorRow(row int) {
	m.plCursor = row
	m.adjustScroll()
}

// keepPlCursorRow runs change, then puts the playlist cursor back on its
// view row after a change to the tracks.
func (m *Model) keepPlCursorRow(change func()) {
	row := m.plCursorRow()
	change()
	if n := m.playlist.Len(); n > 0 {
		m.setPlCursorRow(min(row, n-1))
	}
}
