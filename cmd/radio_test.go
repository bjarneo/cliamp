package cmd

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/external/radio"
	"github.com/bjarneo/cliamp/internal/worldmap"
	"github.com/bjarneo/cliamp/theme"
	"github.com/bjarneo/cliamp/ui"
)

func sampleSummary() radio.Summary {
	return radio.Summary{
		Listeners: 197,
		Countries: []radio.CountryCount{
			{Code: "US", Name: "United States", Count: 48},
			{Code: "DE", Name: "Germany", Count: 22},
			{Code: "GB", Name: "United Kingdom", Count: 9},
			{Code: "SG", Name: "Singapore", Count: 1},
		},
		AllTime: []radio.CountryCount{{Code: "US", Name: "United States", Count: 30029}},
		Channels: []radio.ChannelSummary{
			{Slug: "edm", Name: "EDM", Listeners: 52, Peak: 43, Sessions: 105215, Hours: 10164.6},
			{Slug: "ncs-dnb", Name: "NCS Drum & Bass", Listeners: 31, Peak: 40, Sessions: 98001, Hours: 9001.2},
			{Slug: "amiga", Name: "Amiga", Listeners: 0, Peak: 4, Sessions: 132, Hours: 6},
		},
		Peak:     280,
		Sessions: 783681,
		Hours:    102930.3,
		Daily: []radio.DailyPoint{
			{Date: "2026-09-06", Hours: 2}, {Date: "2026-09-07", Hours: 6}, {Date: "2026-09-08", Hours: 17.2},
		},
	}
}

func TestStatsReport(t *testing.T) {
	out := statsReport(sampleSummary())
	for _, want := range []string{
		"who's listening right now",
		"listening now           197",
		"countries                 4",
		"busiest channel  EDM (52 listening)",
		"all-time high           280",
		"sessions            783,681",
		"hours streamed      102,930",
		"CHANNEL              NOW   PEAK    SESSIONS      HOURS",
		"EDM                   52     43     105,215     10,165",
		"NCS Drum & Bass       31     40      98,001      9,001",
		"Amiga                  0      4         132        6.0",
		"COUNTRY                                  LISTENING",
		"United States                                   48",
		"Singapore                                        1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}

func TestStatsReportIdle(t *testing.T) {
	s := sampleSummary()
	s.Listeners = 0
	s.Countries = nil
	s.Channels = []radio.ChannelSummary{{Slug: "lofi", Name: "Lofi", Sessions: 500000}}
	out := statsReport(s)
	for _, want := range []string{
		"nobody is tuned in right now",
		"countries                 0",
		"busiest channel  Lofi (all-time)",
		"SESSIONS · ALL-TIME",
		"United States                               30,029",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("idle report lacks %q:\n%s", want, out)
		}
	}
}

func TestChannelSlug(t *testing.T) {
	tests := map[string]string{
		"https://radio.cliamp.stream/ncs-dnb/stream": "ncs-dnb",
		"https://radio.cliamp.stream/lofi":           "lofi",
		"https://radio.cliamp.stream/":               "",
		"::not a url":                                "",
	}
	for in, want := range tests {
		if got := channelSlug(in); got != want {
			t.Errorf("channelSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCommas(t *testing.T) {
	tests := map[int]string{0: "0", 7: "7", 999: "999", 1000: "1,000", 12345: "12,345", 783681: "783,681", 1234567: "1,234,567", -1234: "-1,234"}
	for n, want := range tests {
		if got := commas(n); got != want {
			t.Errorf("commas(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestHours(t *testing.T) {
	tests := map[float64]string{0: "0.0", 6: "6.0", 17.25: "17.2", 99.96: "100.0", 100: "100", 10164.6: "10,165"}
	for h, want := range tests {
		if got := hours(h); got != want {
			t.Errorf("hours(%v) = %q, want %q", h, got, want)
		}
	}
}

func TestSparkline(t *testing.T) {
	daily := []radio.DailyPoint{{Hours: 0}, {Hours: 1}, {Hours: 4}, {Hours: 8}}
	if got := sparkline(daily, 10); got != "▁▁▄█" {
		t.Errorf("sparkline = %q", got)
	}
	if got := sparkline(daily, 2); got != "▄█" {
		t.Errorf("sparkline keeps the latest days when narrow, got %q", got)
	}
	if got := sparkline(nil, 5); got != "" {
		t.Errorf("empty sparkline = %q", got)
	}
}

func TestGlobeLayout(t *testing.T) {
	tests := []struct {
		w, h                           int
		side, cols, rows, body, sphere int
	}{
		{120, 40, 40, 76, 38, 38, 38}, // wide: panel shown, globe limited by height
		{200, 30, 40, 56, 28, 28, 28}, // very wide: still limited by height
		{100, 60, 40, 58, 29, 58, 58}, // tall and narrow: globe limited by width
		{70, 30, 0, 52, 26, 28, 26},   // no panel: strip takes two rows
		{50, 14, 0, 20, 10, 12, 10},   // minimum size
	}
	for _, tt := range tests {
		m := &globeModel{width: tt.w, height: tt.h}
		l := m.layout()
		if l.side != tt.side || l.globeCols != tt.cols || l.globeRows != tt.rows || l.bodyRows != tt.body || l.sphereRows != tt.sphere {
			t.Errorf("%dx%d: layout = %+v, want side=%d cols=%d rows=%d body=%d sphere=%d", tt.w, tt.h, l, tt.side, tt.cols, tt.rows, tt.body, tt.sphere)
		}
	}
}

func newTestGlobeModel(t *testing.T) *globeModel {
	t.Helper()
	world, err := worldmap.Load()
	if err != nil {
		t.Fatal(err)
	}
	return newGlobeModel(world, globeStylesFromTheme(theme.Default()))
}

func TestGlobeModelView(t *testing.T) {
	m := newTestGlobeModel(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.Update(globeStatsMsg{}) // marks the data as loaded
	m.apply(sampleSummary())

	view := m.View()
	if !view.AltScreen {
		t.Error("globe view should use the alternate screen")
	}
	lines := strings.Split(view.Content, "\n")
	if len(lines) != m.height {
		t.Fatalf("view has %d lines, want %d", len(lines), m.height)
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w > m.width {
			t.Errorf("line %d is %d cells wide, want <= %d", i, w, m.width)
		}
	}
	for _, want := range []string{"cliamp radio", "LISTENERS", "United States", "EDM", "783,681", "LISTENING HOURS · LAST 31 DAYS", "q quit", "● live"} {
		if !strings.Contains(view.Content, want) {
			t.Errorf("view lacks %q", want)
		}
	}
	if !strings.ContainsRune(view.Content, '⣿') {
		t.Error("view has no land drawn")
	}
	if !strings.Contains(view.Content, "US 48") {
		t.Error("heaviest country should be labelled on the globe")
	}
	if !strings.Contains(view.Content, ansi.Style{}.ForegroundColor(ui.ColorText).String()) {
		t.Error("countries with listeners should be lit")
	}

	m.Update(tea.WindowSizeMsg{Width: 70, Height: 30})
	if content := ansi.Strip(m.View().Content); !strings.Contains(content, "197 listening") || strings.Contains(content, "LISTENING HOURS") {
		t.Errorf("narrow view should swap the panel for the strip:\n%s", content)
	}
	if lines := strings.Split(m.View().Content, "\n"); len(lines) != 30 {
		t.Errorf("narrow view has %d lines, want 30", len(lines))
	}

	m.Update(tea.WindowSizeMsg{Width: 30, Height: 10})
	if content := m.View().Content; !strings.Contains(content, "Terminal too small") {
		t.Errorf("tiny view = %q", content)
	}
}

func TestGlobeMarks(t *testing.T) {
	m := &globeModel{summary: sampleSummary()}
	marks := m.marks()
	if len(marks) != 4 {
		t.Fatalf("got %d marks, want one per country (Singapore via tzdata)", len(marks))
	}
	if marks[0].Weight != 1 || marks[0].Label != "US 48" {
		t.Errorf("heaviest mark = %+v", marks[0])
	}
	if marks[3].Weight >= marks[2].Weight || marks[3].Weight <= 0 {
		t.Errorf("weights should shrink with count: %+v", marks)
	}
	m.summary.Countries = nil
	if marks := m.marks(); len(marks) != 1 || marks[0].Label != "US 30,029" {
		t.Errorf("with nobody listening marks fall back to all-time sessions: %+v", marks)
	}
}

func TestGlobeModelUpdate(t *testing.T) {
	m := newTestGlobeModel(t)
	stats := radio.Statistics{PeakListeners: 5, Stations: map[string]radio.StationStats{
		"edm": {ActiveListeners: 2, ActiveListenerCountries: []radio.CountryStats{{Country: "Norway", CountryCode: "NO", Sessions: 2}}},
	}}
	if _, cmd := m.Update(globeStatsMsg{stats: stats}); cmd != nil {
		t.Error("statistics arriving should not start another refresh chain")
	}
	if _, cmd := m.Update(globeRefreshMsg{}); cmd == nil {
		t.Error("the refresh tick should fetch and re-arm")
	}
	if !m.loaded() || m.summary.Listeners != 2 || !m.surface.lit[worldmap.ID("NO")] {
		t.Errorf("model after stats: loaded=%t listeners=%d litNO=%t", m.loaded(), m.summary.Listeners, m.surface.lit[worldmap.ID("NO")])
	}
	if m.summary.Channels[0].Name != "edm" {
		t.Errorf("channel name before names arrive = %q", m.summary.Channels[0].Name)
	}
	m.Update(globeNamesMsg{"edm": "EDM"})
	if m.summary.Channels[0].Name != "EDM" {
		t.Errorf("channel name after names arrive = %q", m.summary.Channels[0].Name)
	}

	lon := m.globe.Lon()
	m.Update(globeFrameMsg{})
	if m.globe.Lon() == lon {
		t.Error("a frame should spin the globe")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.View()
	m.Update(globeFrameMsg{})
	if m.dirty {
		t.Error("a frame tick while paused should not invalidate the cached view")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if !m.dirty {
		t.Error("spinning by key should invalidate the cached view")
	}

	m.Update(globeStatsMsg{err: errors.New("boom")})
	if m.err == nil || !m.loaded() {
		t.Error("a failed refresh keeps the old data and records the error")
	}
	if !strings.Contains(m.View().Content, "refresh failed: boom") {
		t.Error("the footer should report a failed refresh")
	}
}
