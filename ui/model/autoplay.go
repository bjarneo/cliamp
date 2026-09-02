package model

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/resolve"
)

// autoplayFetchCount is how many Mix entries are requested per refill. It
// matches the initial batch used for pasted Radio/Mix URLs.
const autoplayFetchCount = resolve.YTDLRadioInitialItems

// autoplayTracksMsg carries related tracks fetched for autoplay continuation.
// gen must match requests.autoplay or the result is stale and dropped.
type autoplayTracksMsg struct {
	gen    uint64
	seed   string
	tracks []playlist.Track
	err    error
}

// fetchAutoplayCmd resolves the Mix playlist seeded from the finished track.
// resolve.ResolveYTDLBatch shells out to yt-dlp --flat-playlist with a 30s
// timeout and per-host browser cookies, same as pasted RD... URLs.
func fetchAutoplayCmd(mixURL, seedPath string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		tracks, err := resolve.ResolveYTDLBatch(mixURL, 0, autoplayFetchCount)
		return autoplayTracksMsg{gen: gen, seed: seedPath, tracks: tracks, err: err}
	}
}

// autoplayEligibleSeed returns the Mix URL seeded from the active playback
// track when autoplay is able to continue playback: the feature is on,
// playback is attached, the track is a YouTube video, and its Mix has not
// already come back empty or failed.
func (m *Model) autoplayEligibleSeed() (string, bool) {
	if !m.autoplayRadio || m.playbackDetached {
		return "", false
	}
	track, idx := m.currentPlaybackTrack()
	if idx < 0 || track.Path == m.autoplayFailedSeed {
		return "", false
	}
	return resolve.RadioMixURL(track.Path)
}

// continueWithAutoplay reports whether autoplay will supply more tracks after
// queue exhaustion. pending=true means either a fetch was just started
// (cmd != nil) or one is already in flight (cmd == nil); the
// autoplayTracksMsg handler advances playback when the result lands.
func (m *Model) continueWithAutoplay() (tea.Cmd, bool) {
	if m.autoplayLoading {
		return nil, true
	}
	mixURL, ok := m.autoplayEligibleSeed()
	if !ok {
		return nil, false
	}
	track, _ := m.currentPlaybackTrack()
	m.autoplayLoading = true
	m.status.Activity("Autoplay: finding related tracks…", statusTTLLong)
	return fetchAutoplayCmd(mixURL, track.Path, nextRequest(&m.requests.autoplay)), true
}

// autoplayPrefetchLeadTime starts the Mix fetch well before the final track
// ends so related tracks can arrive and the gapless preloader can arm the
// transition (yt-dlp resolve typically takes a few seconds).
const autoplayPrefetchLeadTime = 45 * time.Second

// maybePrefetchAutoplay fetches related tracks while the last queue track is
// still playing so the autoplay transition is gapless. The tick loop retries
// preloadNext every pass, so this fires once per exhaustion thanks to the
// autoplayLoading guard (and autoplayFailedSeed after a failed fetch).
func (m *Model) maybePrefetchAutoplay() tea.Cmd {
	if m.autoplayLoading {
		return nil
	}
	mixURL, ok := m.autoplayEligibleSeed()
	if !ok {
		return nil
	}
	dur := m.player.Duration()
	if dur <= 0 {
		return nil
	}
	if dur-m.player.Position() > autoplayPrefetchLeadTime {
		return nil
	}
	track, _ := m.currentPlaybackTrack()
	m.autoplayLoading = true
	return fetchAutoplayCmd(mixURL, track.Path, nextRequest(&m.requests.autoplay))
}

// appendAutoplayTracks appends Mix tracks that are not already in the
// playlist, keyed by YouTube video ID so www./music./youtu.be URL variants
// match. Returns how many tracks were added.
func (m *Model) appendAutoplayTracks(tracks []playlist.Track) int {
	seen := make(map[string]bool, m.playlist.Len()+len(tracks))
	for i := 0; i < m.playlist.Len(); i++ {
		t, ok := m.playlist.Track(i)
		if !ok {
			continue
		}
		seen[autoplayDedupeKey(t.Path)] = true
	}
	var fresh []playlist.Track
	for _, t := range tracks {
		key := autoplayDedupeKey(t.Path)
		if seen[key] {
			continue
		}
		seen[key] = true
		fresh = append(fresh, t)
	}
	if len(fresh) == 0 {
		return 0
	}
	m.playlist.Add(fresh...)
	m.loadedPlaylist = ""
	m.addToHeaderState(fresh)
	return len(fresh)
}

// toggleAutoplayRadio flips autoplay continuation and persists the choice,
// mirroring the shuffle (z) and repeat (r) toggles.
func (m *Model) toggleAutoplayRadio() {
	m.autoplayRadio = !m.autoplayRadio
	state := "off"
	if m.autoplayRadio {
		state = "on"
	}
	m.autoplayFailedSeed = ""
	m.status.Showf(statusTTLDefault, "Autoplay radio %s", state)
	if err := m.configSaver.Save("autoplay_radio", fmt.Sprintf("%v", m.autoplayRadio)); err != nil {
		m.status.Errorf(statusTTLDefault, "Config save failed: %s", err)
	}
}

func autoplayDedupeKey(path string) string {
	if id := resolve.YouTubeVideoID(path); id != "" {
		return id
	}
	return path
}
