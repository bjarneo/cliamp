package model

// The playlist view lists tracks in play order. While shuffle is on, a view
// row and a track index can differ. m.plCursor holds a track index, so each
// cursor action acts on the track under the cursor. m.plScroll holds a view
// row. The helpers below are the one place that converts between the two.

// shuffleMoveWarning tells the user why Shift+Up and Shift+Down do not move
// a track while shuffle is on.
const shuffleMoveWarning = "Turn off shuffle to move tracks"
const shuffleReverseWarning = "Turn off shuffle to reverse the playlist"
const pagingReverseWarning = "Wait for the playlist to finish loading"

// shuffleHeadersWarning tells the user why Ctrl+H does not show the album
// headers while shuffle is on.
const shuffleHeadersWarning = "Turn off shuffle to show album headers"

// plShowsAlbumHeaders reports whether the playlist view draws album headers.
// In the shuffled play order, the tracks of one album are not next to each
// other, so the headers would split the album. The headers come back when
// shuffle is off.
func (m Model) plShowsAlbumHeaders() bool {
	return m.showAlbumHeaders && !m.playlist.Shuffled()
}

// plCursorRow returns the view row of the playlist cursor.
func (m Model) plCursorRow() int {
	if row := m.playlist.OrderPosition(m.plCursor); row >= 0 {
		return row
	}
	return m.plCursor
}

// setPlCursorRow puts the playlist cursor on the track at the view row and
// scrolls the view to show the cursor.
func (m *Model) setPlCursorRow(row int) {
	if indices, _ := m.playlist.OrderWindow(row, 1); len(indices) == 1 {
		m.plCursor = indices[0]
	} else {
		m.plCursor = row
	}
	m.adjustScroll()
}

// keepPlCursorRow runs change, then puts the playlist cursor back on its
// view row. While shuffle is on, a change to the tracks can move the track
// under the cursor to a different row.
func (m *Model) keepPlCursorRow(change func()) {
	row := m.plCursorRow()
	change()
	if n := m.playlist.Len(); n > 0 {
		m.setPlCursorRow(min(row, n-1))
	}
}
