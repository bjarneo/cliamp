package ui

import (
	"net/url"
	"strings"

	"cliamp/resolve"

	tea "github.com/charmbracelet/bubbletea"
)

const ytdlBatchSize = 100 // items per background batch

// resetYTDLBatch invalidates any in-progress batch loading session.
// Incrementing the generation ensures that stale in-flight responses
// are discarded by the handler, even if the same URL is reloaded.
func (m *Model) resetYTDLBatch() {
	m.ytdlBatchGen++
	m.ytdlBatchURL = ""
	m.ytdlBatchOffset = 0
	m.ytdlBatchDone = false
	m.ytdlBatchLoading = false
}

// initYTDLBatch detects a YouTube Radio URL among the given source URLs and
// kicks off incremental batch loading. The offset is derived from the known
// initial fetch size (resolve.YTDLRadioInitialItems) so it stays correct
// regardless of how many tracks other URLs contributed to the same load.
func (m *Model) initYTDLBatch(urls []string) tea.Cmd {
	for _, u := range urls {
		parsed, err := url.Parse(u)
		if err != nil {
			continue
		}
		if strings.HasPrefix(parsed.Query().Get("list"), "RD") {
			m.ytdlBatchGen++
			m.ytdlBatchURL = u
			m.ytdlBatchOffset = resolve.YTDLRadioInitialItems
			m.ytdlBatchDone = false
			m.ytdlBatchLoading = true
			return fetchYTDLBatchCmd(m.ytdlBatchGen, u, resolve.YTDLRadioInitialItems, ytdlBatchSize)
		}
	}
	return nil
}
