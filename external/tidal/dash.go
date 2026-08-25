package tidal

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

// mpd is the subset of a Tidal MPEG-DASH manifest needed to reconstruct the
// segment URL list. Tidal audio manifests carry a single Representation with
// an absolute-URL SegmentTemplate and a SegmentTimeline.
type mpd struct {
	Periods []mpdPeriod `xml:"Period"`
}

type mpdPeriod struct {
	AdaptationSets []mpdAdaptationSet `xml:"AdaptationSet"`
}

type mpdAdaptationSet struct {
	SegmentTemplate *segmentTemplate    `xml:"SegmentTemplate"`
	Representations []mpdRepresentation `xml:"Representation"`
}

type mpdRepresentation struct {
	Codecs          string           `xml:"codecs,attr"`
	SegmentTemplate *segmentTemplate `xml:"SegmentTemplate"`
}

type segmentTemplate struct {
	Initialization string `xml:"initialization,attr"`
	Media          string `xml:"media,attr"`
	StartNumber    *int   `xml:"startNumber,attr"`
	Timeline       *struct {
		S []struct {
			D int `xml:"d,attr"`
			R int `xml:"r,attr"`
		} `xml:"S"`
	} `xml:"SegmentTimeline"`
}

// dashSegments extracts the ordered segment URL list (initialization segment
// followed by every media segment) from a raw MPD document.
func dashSegments(raw []byte) ([]string, error) {
	var doc mpd
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("tidal: parse DASH manifest: %w", err)
	}
	for _, p := range doc.Periods {
		for _, a := range p.AdaptationSets {
			for _, r := range a.Representations {
				st := r.SegmentTemplate
				if st == nil {
					st = a.SegmentTemplate
				}
				if st == nil || st.Media == "" {
					continue
				}
				return st.urls()
			}
		}
	}
	return nil, fmt.Errorf("tidal: DASH manifest has no usable segment template")
}

// urls expands the segment template into concrete URLs. Only the
// $Number$-substitution form Tidal uses is supported.
func (st *segmentTemplate) urls() ([]string, error) {
	count := 0
	if st.Timeline != nil {
		for _, s := range st.Timeline.S {
			count += 1 + s.R // r is the number of additional repeats
		}
	}
	if count == 0 {
		return nil, fmt.Errorf("tidal: DASH segment timeline is empty")
	}
	if !strings.Contains(st.Media, "$Number$") {
		return nil, fmt.Errorf("tidal: unsupported DASH media template %q", st.Media)
	}
	start := 1
	if st.StartNumber != nil {
		start = *st.StartNumber
	}

	urls := make([]string, 0, count+1)
	if st.Initialization != "" {
		urls = append(urls, st.Initialization)
	}
	for i := 0; i < count; i++ {
		urls = append(urls, strings.ReplaceAll(st.Media, "$Number$", strconv.Itoa(start+i)))
	}
	return urls, nil
}
