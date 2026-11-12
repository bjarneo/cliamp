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
	url := testAuthURL(400)
	tests := []struct {
		name    string
		width   int
		budget  int
		wantURL bool
	}{
		{name: "narrow panel", width: 40, budget: 20, wantURL: true},
		{name: "wide panel", width: 80, budget: 12, wantURL: true},
		// Loading row, hint row, 5 URL rows and the log row, with no blank row.
		{name: "exact fit", width: 80, budget: 8, wantURL: true},
		{name: "too short", width: 40, budget: 6, wantURL: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := signingInModel(keybindingTestModel(), url)
			m.layout.panelWidth = tt.width
			m.plVisible = tt.budget

			body := ansi.Strip(m.renderProviderList())
			lines := strings.Split(body, "\n")
			if len(lines) != tt.budget {
				t.Fatalf("rows = %d, want %d:\n%s", len(lines), tt.budget, body)
			}
			var urlRows []string
			for _, line := range lines {
				if w := lipgloss.Width(line); w > tt.width {
					t.Fatalf("row width = %d, want <= %d: %q", w, tt.width, line)
				}
				// The other rows are indented. A URL row starts at the edge.
				if line != "" && !strings.HasPrefix(line, " ") {
					urlRows = append(urlRows, line)
				}
			}
			if !strings.Contains(body, "cliamp.log") {
				t.Fatalf("body does not name cliamp.log:\n%s", body)
			}
			got := strings.Join(urlRows, "")
			if tt.wantURL && got != url {
				t.Fatalf("joined URL rows = %q, want %q", got, url)
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
		})
	}
}
