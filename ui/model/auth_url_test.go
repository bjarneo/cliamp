package model

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// testAuthURL returns a sign-in URL of n characters. The counter in the URL
// makes a lost or repeated piece visible in a comparison.
func testAuthURL(n int) string {
	var b strings.Builder
	b.WriteString("https://accounts.spotify.com/authorize?state=")
	for i := 0; b.Len() < n; i++ {
		b.WriteString(strconv.Itoa(i) + "-")
	}
	return b.String()[:n]
}

// signingInModel shows the provider pane while a sign-in waits for the
// browser.
func signingInModel(m Model, url string) Model {
	m.provider = commandsTestProvider{name: "Spotify"}
	m.focus = focusProvider
	m.provPane.loading = true
	m.provPane.authURL = url
	return m
}

func TestProviderListWrapsTheSignInURL(t *testing.T) {
	long := testAuthURL(400)
	tests := []struct {
		name     string
		url      string
		width    int
		budget   int
		wantURL  bool
		wantLink bool
	}{
		{name: "narrow panel", url: long, width: 40, budget: 20, wantURL: true, wantLink: true},
		{name: "wide panel", url: long, width: 80, budget: 12, wantURL: true, wantLink: true},
		// Loading row, hint row, 5 URL rows and the log row, with no blank row.
		{name: "exact fit", url: long, width: 80, budget: 8, wantURL: true, wantLink: true},
		{name: "too short", url: long, width: 40, budget: 6},
		// OSC 8 allows only printable ASCII in the link target.
		{name: "non-ASCII URL", url: "https://example.com/" + strings.Repeat("ø", 60), width: 40, budget: 10, wantURL: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := signingInModel(keybindingTestModel(), tt.url)
			m.layout.panelWidth = tt.width
			m.plVisible = tt.budget

			raw := m.renderProviderList()
			body := ansi.Strip(raw)
			rows := strings.Split(raw, "\n")
			if len(rows) != tt.budget {
				t.Fatalf("rows = %d, want %d:\n%s", len(rows), tt.budget, body)
			}
			link, unlink := ansi.SetHyperlink(tt.url), ansi.ResetHyperlink()
			var urlRows []string
			for _, row := range rows {
				line := ansi.Strip(row)
				if w := lipgloss.Width(row); w > tt.width || w != lipgloss.Width(line) {
					t.Fatalf("row width = %d, want <= %d and equal to the plain width %d: %q", w, tt.width, lipgloss.Width(line), row)
				}
				// The other rows are indented. A URL row starts at the edge.
				if line == "" || strings.HasPrefix(line, " ") {
					continue
				}
				urlRows = append(urlRows, line)
				want := line
				if tt.wantLink {
					want = link + line + unlink
				}
				if row != want {
					t.Fatalf("URL row = %q, want %q", row, want)
				}
			}
			if !strings.Contains(body, "cliamp.log") {
				t.Fatalf("body does not name cliamp.log:\n%s", body)
			}
			// Every URL row but the last fills the panel, so the URL breaks
			// at exact cell widths.
			for i, row := range urlRows[:max(0, len(urlRows)-1)] {
				if w := lipgloss.Width(row); w != tt.width {
					t.Fatalf("URL row %d width = %d, want %d", i, w, tt.width)
				}
			}
			got := strings.Join(urlRows, "")
			if tt.wantURL && got != tt.url {
				t.Fatalf("joined URL rows = %q, want %q", got, tt.url)
			}
			if !tt.wantURL {
				if got != "" {
					t.Fatalf("URL rows = %q, want none when the URL does not fit", got)
				}
				if !strings.Contains(strings.Join(strings.Fields(body), " "), "does not fit") {
					t.Fatalf("body does not say that the URL does not fit:\n%s", body)
				}
			}
		})
	}
}

func TestSignInURLViewFitsTerminal(t *testing.T) {
	url := testAuthURL(400)
	for _, tt := range []struct {
		width, height int
		wantURL       bool
	}{
		{width: 40, height: 10},
		{width: 80, height: 24, wantURL: true},
		{width: 120, height: 40, wantURL: true},
	} {
		t.Run(fmt.Sprintf("%dx%d", tt.width, tt.height), func(t *testing.T) {
			m := signingInModel(newLayoutTestModel(tt.width, tt.height), url)
			m.recomputeLayout()

			view := m.View().Content
			assertViewFits(t, view, tt.width, tt.height)
			// The frame pads each row with spaces, and the URL rows follow
			// each other, so the URL shows once the white space is gone.
			compact := strings.Join(strings.Fields(ansi.Strip(view)), "")
			if tt.wantURL && !strings.Contains(compact, url) {
				t.Fatalf("view does not hold the full URL:\n%s", ansi.Strip(view))
			}
			// The frame and FitRect keep each link whole and closed.
			links := strings.Count(view, ansi.SetHyperlink(url))
			if tt.wantURL && links == 0 {
				t.Fatal("view has no link to the full URL")
			}
			if resets := strings.Count(view, ansi.ResetHyperlink()); resets != links {
				t.Fatalf("view opens %d links and closes %d, want the same", links, resets)
			}
		})
	}
}
