package model

import (
	"strconv"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/playlist"
)

const metadataPaneMaxRows = 8

type metadataField struct {
	label string
	value string
}

// selectedMetadataTrack reads the highlighted row, even while another track is
// playing or a setting has focus. Inspecting metadata never resolves a source.
func (m Model) selectedMetadataTrack() playlist.Track {
	if m.playlist == nil {
		return playlist.Track{}
	}
	track, _ := m.playlist.Track(m.plCursor)
	return track
}

func metadataText(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, ansi.Strip(value))
	return strings.Join(strings.Fields(value), " ")
}

func (m Model) metadataFields() []metadataField {
	track := m.selectedMetadataTrack()
	var fields []metadataField
	add := func(label, value string) {
		if value = metadataText(value); value != "" {
			fields = append(fields, metadataField{label, value})
		}
	}
	artist, genre, number := "Artist", "Genre", "Track"
	if track.Meta("podcast.feed") != "" {
		artist, number = "Show", "Episode"
	}
	if track.IsLive() {
		genre = "Tags"
	}
	add("Title", track.Title)
	add(artist, track.Artist)
	if track.Album != track.Artist {
		add("Album", track.Album)
	}
	add(genre, track.Genre)
	if date := track.Meta("podcast.published"); date != "" {
		add("Date", date)
	} else if track.Year > 0 {
		add("Year", strconv.Itoa(track.Year))
	}
	if track.TrackNumber > 0 {
		add(number, strconv.Itoa(track.TrackNumber))
	}
	if track.DurationSecs > 0 {
		add("Length", formatTrackTime(track.DurationSecs))
	}
	add("Country", track.Meta("radio.country"))
	add("Region", track.Meta("radio.state"))
	add("Codec", track.Meta("radio.codec"))
	if bitrate := track.Meta("radio.bitrate"); bitrate != "" {
		add("Bitrate", bitrate+" kbps")
	}
	if track.IsLive() {
		if playlist.IsYTDL(track.Path) {
			add("Type", "Live stream")
		} else {
			add("Type", "Live radio")
		}
		if playing, _ := m.currentPlaybackTrack(); m.player != nil && m.player.IsPlaying() && track.Path != "" && track.Path == playing.Path {
			add("Playing", m.streamTitle)
		}
	}
	return fields
}

// metadataPaneRows reserves details only after every direct setting can fit,
// with shuffle, repeat, and speed sharing a row. This budget is shared by
// rendering and Tab navigation.
func (m Model) metadataPaneRows(rows int) int {
	if !m.showMetadata {
		return 0
	}
	controls := 3
	if len(m.providers) > 1 {
		controls++
	}
	available := rows - controls
	if available < 3 {
		return 0
	}
	return min(metadataPaneMaxRows, available)
}

// metadataPaneBudget returns a blank row before the metadata block when the
// settings column has the full metadata height budget, and how many rows
// metadata may use after that. A cramped pane omits the gap so every field
// row can stay.
func (m Model) metadataPaneBudget(rows int) (gap, metaRows int) {
	metaRows = m.metadataPaneRows(rows)
	if metaRows == 0 || metaRows < metadataPaneMaxRows || m.layout.height < gapMinHeight {
		return 0, metaRows
	}
	if withGap := m.metadataPaneRows(rows - 1); withGap > 0 {
		return 1, withGap
	}
	return 0, metaRows
}

func (m Model) renderMetadataPane(rows int) []string {
	if rows <= 0 {
		return nil
	}
	w := m.layout.settingsWidth
	lines := []string{fillSeparator(sepHeader("Metadata", m.layout.panelWidth), w)}
	fields := m.metadataFields()
	if len(fields) == 0 {
		return append(lines, dimStyle.Render(truncate("No metadata available", w)))
	}
	count := rows - 1
	if len(fields) > count {
		count-- // keep a route to the complete, scrollable inspector
	}
	for _, field := range fields[:min(len(fields), max(0, count))] {
		label := labelStyle.Render(field.label + " ")
		lines = append(lines, label+trackStyle.Render(truncate(field.value, max(1, w-lipgloss.Width(label)))))
	}
	if len(fields) > count {
		lines = append(lines, dimStyle.Render("i: more details"))
	}
	return lines
}

func (m *Model) toggleMetadata() {
	m.SetShowMetadata(!m.showMetadata)
	_ = m.saveConfigBool("show_metadata", m.showMetadata)
	if m.showMetadata && (!m.layout.twoColumn || m.metadataPaneRows(m.layout.listRows()) == 0) {
		m.info = infoOverlay{visible: true}
		m.refreshChrome()
	}
}
