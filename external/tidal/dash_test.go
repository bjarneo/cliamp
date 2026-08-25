package tidal

import "testing"

// sampleMPD mirrors the structure of Tidal's live hi-res manifests: one FLAC
// representation, absolute URLs with XML-escaped query strings, $Number$
// media template, and a SegmentTimeline using a repeat count.
const sampleMPD = `<?xml version="1.0" encoding="UTF-8"?>
<MPD xmlns="urn:mpeg:dash:schema:mpd:2011" type="static">
  <Period>
    <AdaptationSet contentType="audio" mimeType="audio/mp4">
      <Representation id="0" codecs="flac" audioSamplingRate="48000" bandwidth="1500000">
        <SegmentTemplate initialization="https://cdn.tidal.com/init.mp4?token=t&amp;x=1" media="https://cdn.tidal.com/seg$Number$.m4s" startNumber="1" timescale="48000">
          <SegmentTimeline>
            <S t="0" d="192512" r="1"/>
            <S d="65536"/>
          </SegmentTimeline>
        </SegmentTemplate>
      </Representation>
    </AdaptationSet>
  </Period>
</MPD>`

func TestDashSegments(t *testing.T) {
	segments, err := dashSegments([]byte(sampleMPD))
	if err != nil {
		t.Fatalf("dashSegments: %v", err)
	}
	// r="1" means one extra repeat: S(d=192512) x2 + S(d=65536) = 3 media
	// segments, plus the init segment.
	if len(segments) != 4 {
		t.Fatalf("got %d segments, want 4: %v", len(segments), segments)
	}
	if segments[0] != "https://cdn.tidal.com/init.mp4?token=t&x=1" {
		t.Errorf("init = %q (entities must be decoded)", segments[0])
	}
	if segments[3] != "https://cdn.tidal.com/seg3.m4s" {
		t.Errorf("last = %q", segments[3])
	}
}

func TestDashSegmentsStartNumber(t *testing.T) {
	const mpd = `<MPD><Period><AdaptationSet>
	  <SegmentTemplate initialization="https://x/init" media="https://x/s$Number$" startNumber="5">
	    <SegmentTimeline><S d="1"/><S d="1"/></SegmentTimeline>
	  </SegmentTemplate>
	  <Representation codecs="flac"/>
	</AdaptationSet></Period></MPD>`
	segments, err := dashSegments([]byte(mpd))
	if err != nil {
		t.Fatalf("dashSegments: %v", err)
	}
	want := []string{"https://x/init", "https://x/s5", "https://x/s6"}
	for i := range want {
		if segments[i] != want[i] {
			t.Errorf("segment[%d] = %q, want %q", i, segments[i], want[i])
		}
	}
}

func TestDashSegmentsErrors(t *testing.T) {
	tests := []struct {
		name string
		mpd  string
	}{
		{"not xml", "{json}"},
		{"no template", `<MPD><Period><AdaptationSet><Representation codecs="flac"/></AdaptationSet></Period></MPD>`},
		{"empty timeline", `<MPD><Period><AdaptationSet><Representation codecs="flac"><SegmentTemplate media="https://x/$Number$"/></Representation></AdaptationSet></Period></MPD>`},
		{"unsupported template", `<MPD><Period><AdaptationSet><Representation codecs="flac"><SegmentTemplate media="https://x/$Time$"><SegmentTimeline><S d="1"/></SegmentTimeline></SegmentTemplate></Representation></AdaptationSet></Period></MPD>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if segs, err := dashSegments([]byte(tt.mpd)); err == nil {
				t.Errorf("expected error, got %v", segs)
			}
		})
	}
}
