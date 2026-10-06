package model

import (
	"net/url"
	"strings"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/resolve"

	tea "charm.land/bubbletea/v2"
)

const ytdlBatchSize = 100 // items per background batch

// resetYTDLBatch invalidates any in-progress batch loading session.
// Incrementing the generation ensures that stale in-flight responses
// are discarded by the handler, even if the same URL is reloaded.
func (m *Model) resetYTDLBatch() {
	m.ytdlBatch.gen++
	m.ytdlBatch.url = ""
	m.ytdlBatch.offset = 0
	m.ytdlBatch.done = false
	m.ytdlBatch.loading = false
}

// initYTDLBatch detects YouTube URLs with a list= parameter among the given
// source URLs and kicks off incremental batch loading. It triggers for any
// YouTube Music URL with a playlist (list=) and for any URL with an RD-prefix
// playlist (YouTube auto-generated Radio/Mix). The offset is derived from the
// known initial fetch size (resolve.YTDLRadioInitialItems) so it stays correct
// regardless of how many tracks other URLs contributed to the same load.
func (m *Model) initYTDLBatch(urls []string) tea.Cmd {
	for _, u := range urls {
		parsed, err := url.Parse(u)
		if err != nil {
			continue
		}
		list := parsed.Query().Get("list")
		if list == "" {
			continue
		}
		if playlist.IsYouTubeMusicURL(u) || strings.HasPrefix(list, "RD") {
			m.ytdlBatch.gen++
			m.ytdlBatch.url = u
			m.ytdlBatch.offset = resolve.YTDLRadioInitialItems
			m.ytdlBatch.done = false
			m.ytdlBatch.loading = true
			return fetchYTDLBatchCmd(m.ytdlBatch.gen, u, resolve.YTDLRadioInitialItems, ytdlBatchSize)
		}
	}
	return nil
}
