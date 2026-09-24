// radio_globe.go implements `cliamp radio --stats --globe`: the listener
// statistics on a spinning Braille globe, the terminal twin of the globe on
// cliamp.stream. Like the setup wizard it is a small standalone Bubbletea
// program rather than part of the player Model.
package cmd

import (
	"context"
	"fmt"
	"image/color"
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/external/radio"
	"github.com/bjarneo/cliamp/internal/globe"
	"github.com/bjarneo/cliamp/internal/worldmap"
	"github.com/bjarneo/cliamp/theme"
	"github.com/bjarneo/cliamp/ui"
)

const (
	globeFrame     = 50 * time.Millisecond                          // 20 frames per second
	globeTurn      = 40 * time.Second                               // one full rotation
	globeSpinStep  = 360 * float64(globeFrame) / float64(globeTurn) // degrees per frame
	globeNudge     = 10.0                                           // degrees per arrow key
	globeRefresh   = 15 * time.Second                               // same cadence as the website
	globeSideWidth = 40
	globeGap       = 2 // columns between globe and panel
	globeMinWidth  = 50
	globeMinHeight = 14
)

// RadioGlobe shows the listener statistics on a spinning globe until the
// user quits. themeName, when set, overrides the configured theme.
func RadioGlobe(ctx context.Context, themeName string) error {
	world, err := worldmap.Load()
	if err != nil {
		return err
	}
	m := newGlobeModel(world, globeStylesFromTheme(globeTheme(themeName)))
	_, err = tea.NewProgram(m, tea.WithContext(ctx)).Run()
	return err
}

// globeTheme resolves the theme to draw with: the named one, else the
// configured one, else the ANSI default.
func globeTheme(name string) theme.Theme {
	if name == "" {
		if cfg, err := config.Load(); err == nil {
			name = cfg.Theme
		}
	}
	if t, ok := theme.Find(name); ok {
		return t
	}
	return theme.Default()
}

type globeStyles struct {
	palette globe.Palette
	title   lipgloss.Style
	dim     lipgloss.Style
	accent  lipgloss.Style
	text    lipgloss.Style
	warn    lipgloss.Style
}

func globeStylesFromTheme(t theme.Theme) globeStyles {
	ui.ApplyThemeColors(t)
	fg := func(c color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }
	sgr := func(c color.Color) ansi.Style { return ansi.Style{}.ForegroundColor(c) }
	return globeStyles{
		title:  fg(ui.ColorTitle).Bold(true),
		dim:    fg(ui.ColorDim),
		accent: fg(ui.ColorAccent),
		text:   fg(ui.ColorText),
		warn:   fg(ui.ColorWarning),
		palette: globe.Palette{
			Grid:    sgr(ui.ColorDim).Faint(),
			Rim:     sgr(ui.ColorAccent).Faint(),
			Land:    sgr(ui.ColorDim),
			Lit:     sgr(ui.ColorText),
			Mark:    sgr(ui.ColorAccent).Bold(),
			MarkFar: sgr(ui.ColorAccent).Faint(),
			Label:   sgr(ui.ColorAccent),
		},
	}
}

// listenerSurface is the world map with the countries that have listeners
// lit; it is what the globe samples.
type listenerSurface struct {
	world *worldmap.Map
	lit   [256]bool
}

func (s *listenerSurface) At(lat, lon float64) globe.Terrain {
	id := s.world.IDAt(lat, lon)
	switch {
	case id == 0:
		return globe.Sea
	case s.lit[id]:
		return globe.Lit
	}
	return globe.Land
}

type globeModel struct {
	surface   *listenerSurface
	globe     *globe.Globe
	styles    globeStyles
	width     int
	height    int
	stats     radio.Statistics
	summary   radio.Summary
	names     map[string]string
	fetchedAt time.Time // zero until the first statistics arrive
	err       error
	spinning  bool
	panel     []string // side panel lines, rebuilt when the data or the size changes
	frame     string   // last rendered view, reused while nothing changes
	dirty     bool
}

type globeStatsMsg struct {
	stats radio.Statistics
	err   error
}

type globeNamesMsg map[string]string

type globeFrameMsg time.Time

type globeRefreshMsg struct{}

func newGlobeModel(world *worldmap.Map, styles globeStyles) *globeModel {
	surface := &listenerSurface{world: world}
	g := globe.New(surface)
	g.SetLon(-20)
	return &globeModel{surface: surface, globe: g, styles: styles, spinning: true, dirty: true}
}

func (m *globeModel) Init() tea.Cmd {
	return tea.Batch(fetchGlobeStats, fetchGlobeNames, refreshTick(), globeTick())
}

func fetchGlobeStats() tea.Msg {
	ctx, cancel := context.WithTimeout(context.Background(), statsTimeout)
	defer cancel()
	stats, _, err := radio.FetchStatistics(ctx)
	return globeStatsMsg{stats: stats, err: err}
}

func fetchGlobeNames() tea.Msg {
	return globeNamesMsg(channelNames())
}

func globeTick() tea.Cmd {
	return tea.Tick(globeFrame, func(t time.Time) tea.Msg { return globeFrameMsg(t) })
}

func refreshTick() tea.Cmd {
	return tea.Tick(globeRefresh, func(time.Time) tea.Msg { return globeRefreshMsg{} })
}

func (m *globeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Every message changes the picture except a frame tick while paused.
	if _, frame := msg.(globeFrameMsg); !frame || m.spinning {
		m.dirty = true
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.refreshPanel()
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case globeFrameMsg:
		if m.spinning {
			m.globe.Spin(globeSpinStep)
		}
		return m, globeTick()
	case globeRefreshMsg:
		return m, tea.Batch(fetchGlobeStats, refreshTick())
	case globeStatsMsg:
		m.err = msg.err
		if msg.err == nil {
			m.stats = msg.stats
			m.fetchedAt = time.Now()
			m.summarize()
		}
	case globeNamesMsg:
		m.names = msg
		if m.loaded() {
			m.summarize()
		}
	}
	return m, nil
}

func (m *globeModel) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case "left", "h":
		m.globe.Spin(-globeNudge)
	case "right", "l":
		m.globe.Spin(globeNudge)
	case "up", "k":
		m.globe.TiltBy(globeNudge)
	case "down", "j":
		m.globe.TiltBy(-globeNudge)
	case "space", " ":
		m.spinning = !m.spinning
	case "r":
		return m, fetchGlobeStats
	}
	return m, nil
}

// loaded reports whether any statistics have arrived yet.
func (m *globeModel) loaded() bool { return !m.fetchedAt.IsZero() }

// summarize rebuilds everything derived from the last statistics document.
func (m *globeModel) summarize() {
	m.apply(m.stats.Summarize(m.names))
}

// apply installs a summary: the lit countries on the globe and the side
// panel follow from it.
func (m *globeModel) apply(s radio.Summary) {
	m.summary = s
	m.surface.lit = [256]bool{}
	rows, _ := countryRows(s)
	for _, c := range rows {
		if id := worldmap.ID(c.Code); id != 0 {
			m.surface.lit[id] = true
		}
	}
	m.refreshPanel()
}

// refreshPanel re-renders the side panel for the current data and size.
func (m *globeModel) refreshPanel() {
	l := m.layout()
	m.panel = nil
	if l.side > 0 {
		m.panel = m.renderSide(l.side, l.sphereRows)
	}
}

// marks sizes each country's dot by the square root of its share of the
// heaviest country, as the website does, so small counts stay visible. The
// globe itself decides which of the labels fit.
func (m *globeModel) marks() []globe.Mark {
	rows, _ := countryRows(m.summary)
	if len(rows) == 0 {
		return nil
	}
	heaviest := float64(rows[0].Count)
	marks := make([]globe.Mark, 0, len(rows))
	for _, c := range rows {
		lat, lon, ok := worldmap.Centroid(c.Code)
		if !ok {
			continue
		}
		marks = append(marks, globe.Mark{
			Lat: lat, Lon: lon,
			Weight: math.Sqrt(float64(c.Count) / heaviest),
			Label:  c.Code + " " + commas(c.Count),
		})
	}
	return marks
}

// globeLayout splits the terminal into the globe and, when there is room, a
// statistics panel beside it. Without the panel a one-line strip of totals
// sits under the header instead.
type globeLayout struct {
	globeCols  int
	globeRows  int
	side       int // panel width, 0 when hidden
	gap        int // columns between globe and panel, 0 when hidden
	bodyRows   int // rows between header and footer
	sphereRows int // rows the globe may use: bodyRows less the totals strip
}

func (m *globeModel) layout() globeLayout {
	l := globeLayout{bodyRows: max(0, m.height-2)}
	l.sphereRows = l.bodyRows
	if m.width >= globeMinWidth+globeSideWidth+globeGap {
		l.side, l.gap = globeSideWidth, globeGap
	} else {
		l.sphereRows = max(0, l.bodyRows-2) // totals strip and a blank line
	}
	avail := m.width - l.side - l.gap
	l.globeRows = max(0, min(l.sphereRows, avail/2))
	l.globeCols = 2 * l.globeRows
	return l
}

func (m *globeModel) View() tea.View {
	if m.dirty || m.frame == "" {
		if m.width < globeMinWidth || m.height < globeMinHeight {
			m.frame = fmt.Sprintf("Terminal too small. Resize to at least %dx%d (current: %dx%d).", globeMinWidth, globeMinHeight, m.width, m.height)
		} else {
			m.frame = m.render()
		}
		m.dirty = false
	}
	v := tea.NewView(m.frame)
	v.AltScreen = true
	if ui.ColorBackground != nil {
		v.BackgroundColor = ui.ColorBackground
		v.ForegroundColor = ui.ColorText
	}
	return v
}

// render lays out header, globe (centred in its column), the cached side
// panel or the totals strip, and footer. Globe lines are exactly globeCols
// cells wide and panel lines exactly side wide, so rows are joined by plain
// padding.
func (m *globeModel) render() string {
	l := m.layout()
	m.globe.Resize(l.globeCols, l.globeRows)
	sphere := strings.Split(m.globe.Render(m.marks(), m.styles.palette), "\n")
	globeWidth := m.width - l.side - l.gap
	left := (globeWidth - l.globeCols) / 2
	top := (l.sphereRows - l.globeRows) / 2

	var b strings.Builder
	b.WriteString(m.renderHeader())
	b.WriteByte('\n')
	if l.side == 0 {
		b.WriteString(ansi.Truncate(m.renderStrip(), m.width, "…"))
		b.WriteString("\n\n")
	}
	for row := range l.sphereRows {
		if i := row - top; i >= 0 && i < len(sphere) {
			b.WriteString(strings.Repeat(" ", left))
			b.WriteString(sphere[i])
			if l.side > 0 {
				b.WriteString(strings.Repeat(" ", globeWidth-left-l.globeCols+l.gap))
			}
		} else if l.side > 0 {
			b.WriteString(strings.Repeat(" ", globeWidth+l.gap))
		}
		if l.side > 0 && row < len(m.panel) {
			b.WriteString(m.panel[row])
		}
		b.WriteByte('\n')
	}
	b.WriteString(m.renderFooter())
	return b.String()
}

func (m *globeModel) renderHeader() string {
	title := m.styles.title.Render("cliamp radio") + m.styles.dim.Render(" · who's listening right now")
	var status string
	switch {
	case !m.loaded() && m.err != nil:
		status = m.styles.warn.Render("live statistics unavailable")
	case !m.loaded():
		status = m.styles.dim.Render("loading…")
	case m.summary.Listeners > 0:
		status = m.styles.accent.Render("● live") + m.styles.dim.Render(" · updated "+m.fetchedAt.Format("15:04:05"))
	default:
		status = m.styles.dim.Render("○ all-time · updated " + m.fetchedAt.Format("15:04:05"))
	}
	return spread(title, status, m.width)
}

func (m *globeModel) renderFooter() string {
	keys := m.styles.dim.Render("←/→ spin   ↑/↓ tilt   space pause   r refresh   q quit")
	var note string
	if m.err != nil && m.loaded() {
		note = m.styles.warn.Render(ansi.Truncate("refresh failed: "+m.err.Error(), max(10, m.width/2), "…"))
	}
	return spread(keys, note, m.width)
}

// renderStrip is the totals line shown when the terminal is too narrow for
// the side panel.
func (m *globeModel) renderStrip() string {
	s := m.summary
	parts := []string{
		m.styles.text.Render(commas(s.Listeners)) + m.styles.dim.Render(" listening"),
		m.styles.text.Render(commas(len(s.Countries))) + m.styles.dim.Render(" countries"),
	}
	if len(s.Channels) > 0 && s.Channels[0].Listeners > 0 {
		parts = append(parts, m.styles.dim.Render("busiest ")+m.styles.text.Render(s.Channels[0].Name))
	}
	parts = append(parts, m.styles.dim.Render("all-time high ")+m.styles.text.Render(commas(s.Peak)))
	return strings.Join(parts, m.styles.dim.Render(" · "))
}

// renderSide is the statistics panel: headline numbers, top countries, the
// all-time tiles, the daily sparkline, and the channels people are on now.
// It returns exactly height lines, each exactly width cells wide.
func (m *globeModel) renderSide(width, height int) []string {
	s := m.summary
	dim, text, accent := m.styles.dim.Render, m.styles.text.Render, m.styles.accent.Render
	pair := func(left, right string) string {
		return lipgloss.PlaceHorizontal(width/2, lipgloss.Left, left) + right
	}
	rows, live := countryRows(s)

	head := []string{
		pair(dim("LISTENERS"), dim("COUNTRIES")),
		pair(text(commas(s.Listeners)), text(commas(len(s.Countries)))),
		"",
	}
	if live {
		head = append(head, spread(dim("TOP COUNTRIES"), dim("LISTENERS"), width))
	} else {
		head = append(head, spread(dim("TOP COUNTRIES · ALL-TIME"), dim("SESSIONS"), width))
	}

	tail := []string{""}
	switch {
	case len(s.Channels) > 0 && s.Channels[0].Listeners > 0:
		c := s.Channels[0]
		tail = append(tail, dim("BUSIEST CHANNEL NOW"), text(c.Name)+dim(" · "+commas(c.Listeners)+" listening"))
	case len(s.Channels) > 0:
		c := s.Channels[0]
		tail = append(tail, dim("BUSIEST CHANNEL · ALL-TIME"), text(c.Name)+dim(" · "+commas(c.Sessions)+" sessions"))
	default:
		tail = append(tail, dim("BUSIEST CHANNEL"), dim("–"))
	}
	tail = append(tail,
		pair(dim("ALL-TIME HIGH"), dim("SESSIONS")),
		pair(text(commas(s.Peak)), text(commas(s.Sessions))),
		dim("HOURS STREAMED")+" "+text(hours(s.Hours)),
		"",
		dim(fmt.Sprintf("LISTENING HOURS · LAST %d DAYS", radio.DailyWindow)),
	)
	if len(s.Daily) > 0 {
		first, last := s.Daily[0], s.Daily[len(s.Daily)-1]
		tail = append(tail, accent(sparkline(s.Daily, width)), spread(dim(shortDate(first.Date)), dim(shortDate(last.Date)), width))
	} else {
		tail = append(tail, dim("–"), "")
	}

	// The country table gets whatever height the fixed sections leave.
	lines := head
	if len(rows) == 0 {
		if m.loaded() {
			lines = append(lines, dim("nobody is tuned in right now"))
		} else {
			lines = append(lines, dim("…"))
		}
	}
	const barWidth, countWidth = 10, 7
	nameWidth := width - barWidth - countWidth - 2
	for _, c := range rows[:min(len(rows), max(3, height-len(head)-len(tail)))] {
		filled := max(1, int(math.Round(float64(c.Count)/float64(rows[0].Count)*barWidth)))
		bar := accent(strings.Repeat("█", filled)) + dim(strings.Repeat("·", barWidth-filled))
		name := lipgloss.PlaceHorizontal(nameWidth, lipgloss.Left, text(ansi.Truncate(c.Name, nameWidth, "…")))
		lines = append(lines, name+" "+bar+" "+lipgloss.PlaceHorizontal(countWidth, lipgloss.Right, text(commas(c.Count))))
	}
	lines = append(lines, tail...)

	if rest := height - len(lines) - 2; rest > 0 {
		var active []radio.ChannelSummary
		for _, c := range s.Channels {
			if c.Listeners > 0 {
				active = append(active, c)
			}
		}
		if len(active) > 0 {
			lines = append(lines, "", spread(dim("CHANNELS"), dim("LISTENERS"), width))
			for _, c := range active[:min(len(active), rest)] {
				lines = append(lines, spread(text(ansi.Truncate(c.Name, width-8, "…")), text(commas(c.Listeners)), width))
			}
		}
	}

	lines = lines[:min(len(lines), height)]
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i, l := range lines {
		lines[i] = lipgloss.PlaceHorizontal(width, lipgloss.Left, ansi.Truncate(l, width, ""))
	}
	return lines
}

// sparkline draws hours per day as bar glyphs, one per column, keeping the
// most recent days when there are more than fit.
func sparkline(daily []radio.DailyPoint, width int) string {
	if width <= 0 {
		return ""
	}
	if len(daily) > width {
		daily = daily[len(daily)-width:]
	}
	var peak float64
	for _, d := range daily {
		peak = max(peak, d.Hours)
	}
	levels := []rune("▁▂▃▄▅▆▇█")
	var b strings.Builder
	for _, d := range daily {
		level := 0
		if peak > 0 {
			level = min(len(levels)-1, int(math.Ceil(d.Hours/peak*float64(len(levels)))-1))
		}
		b.WriteRune(levels[max(0, level)])
	}
	return b.String()
}

// shortDate turns "2026-09-08" into "Sep 8".
func shortDate(iso string) string {
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return iso
	}
	return t.Format("Jan 2")
}

// spread puts left and right at opposite ends of a line width cells wide.
func spread(left, right string, width int) string {
	gap := width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return ansi.Truncate(left+" "+right, width, "")
	}
	return left + strings.Repeat(" ", gap) + right
}
