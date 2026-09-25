package resolve

import (
	"strings"
	"testing"
)

// yt-dlp flat-playlist entries carry live_status; a stream that is live now
// has no track boundary and must be marked Realtime like a radio station.
func TestParseYTDLTracksMarksLiveStreams(t *testing.T) {
	input := `{"url":"https://www.youtube.com/watch?v=live1","title":"Lofi radio","live_status":"is_live","duration":null}
{"url":"https://www.youtube.com/watch?v=vod1","title":"Song","live_status":"not_live","duration":237}
{"url":"https://www.youtube.com/watch?v=ended1","title":"Past stream","live_status":"was_live","duration":3600}
{"url":"https://www.youtube.com/watch?v=old1","title":"No status field","duration":100}
`
	tracks, _, err := parseYTDLTracks(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parseYTDLTracks: %v", err)
	}
	want := []struct {
		realtime bool
		duration int
	}{
		{realtime: true, duration: 0},
		{realtime: false, duration: 237},
		{realtime: false, duration: 3600},
		{realtime: false, duration: 100},
	}
	if len(tracks) != len(want) {
		t.Fatalf("got %d tracks, want %d", len(tracks), len(want))
	}
	for i, w := range want {
		if tracks[i].Realtime != w.realtime {
			t.Errorf("track %d (%s): Realtime = %v, want %v", i, tracks[i].Title, tracks[i].Realtime, w.realtime)
		}
		if tracks[i].DurationSecs != w.duration {
			t.Errorf("track %d (%s): DurationSecs = %d, want %d", i, tracks[i].Title, tracks[i].DurationSecs, w.duration)
		}
	}
}
