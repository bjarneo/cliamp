package model

import (
	"fmt"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// subsLoadMode says what to do with the episodes a subscription returns.
type subsLoadMode int

const (
	// subsLoadAppend adds every episode to the playlist.
	subsLoadAppend subsLoadMode = iota
	// subsLoadPlay adds every episode and starts the newest.
	subsLoadPlay
	// subsLoadQueue adds every episode and queues them in feed order.
	subsLoadQueue
	// subsLoadLatest adds only the newest episode and queues it next.
	subsLoadLatest
)

// subsLatestWorkers bounds the concurrent feed fetches when sweeping every
// subscription. Feeds are small, but a long subscription list should not open
// one connection per show at once.
const subsLatestWorkers = 6

// subsEpisodesMsg carries one show's episodes back to the update loop.
type subsEpisodesMsg struct {
	mode   subsLoadMode
	name   string
	tracks []playlist.Track
	err    error
}

// subsLatestAllMsg carries the newest episode of every subscribed show.
type subsLatestAllMsg struct {
	tracks []playlist.Track
	failed []string
}

// episodeLoader returns the provider that can fetch a show's episodes.
func (m Model) episodeLoader() provider.AlbumTrackLoader {
	if l, ok := m.provider.(provider.AlbumTrackLoader); ok {
		return l
	}
	for _, pe := range m.providers {
		if pe.Provider == nil {
			continue
		}
		if l, ok := pe.Provider.(provider.AlbumTrackLoader); ok {
			return l
		}
	}
	return nil
}

// latestEpisode returns the newest episode of a feed. Feeds are conventionally
// newest-first, but publication dates decide when they are present so a feed
// listing oldest-first still resolves correctly.
func latestEpisode(tracks []playlist.Track) (playlist.Track, bool) {
	if len(tracks) == 0 {
		return playlist.Track{}, false
	}
	best, bestDate := tracks[0], tracks[0].Meta(provider.MetaPodcastPublished)
	for _, t := range tracks[1:] {
		// Dates are YYYY-MM-DD, so string order is date order.
		if date := t.Meta(provider.MetaPodcastPublished); date > bestDate {
			best, bestDate = t, date
		}
	}
	return best, true
}

// loadSubscription fetches the episodes of the show highlighted in the
// subscriptions overlay.
func (m *Model) loadSubscription(mode subsLoadMode) tea.Cmd {
	show, ok := m.selectedSubscription()
	if !ok {
		return nil
	}
	return m.loadShowEpisodes(show.ID, show.Name, mode)
}

// loadShowEpisodes fetches one show's episodes by provider ID. It serves both
// the subscriptions overlay and the provider list, which address a show the
// same way.
func (m *Model) loadShowEpisodes(id, name string, mode subsLoadMode) tea.Cmd {
	if id == "" || m.subs.loading {
		return nil
	}
	loader := m.episodeLoader()
	if loader == nil {
		m.subs.err = "No provider can load episodes."
		m.status.Warning("No provider can load episodes.", statusTTLDefault)
		return nil
	}
	m.subs.loading = true
	m.subs.err = ""
	m.subs.status = "Loading " + name + "..."
	if !m.subs.visible {
		// Outside the overlay there is no status line of its own, so the
		// player's one has to carry the wait.
		m.status.Activityf(statusTTLLong, "Loading the latest from %s...", name)
	}
	return func() tea.Msg {
		tracks, err := loader.AlbumTracks(id)
		if err == nil && mode == subsLoadLatest {
			if latest, ok := latestEpisode(tracks); ok {
				tracks = []playlist.Track{latest}
			} else {
				tracks = nil
			}
		}
		return subsEpisodesMsg{mode: mode, name: name, tracks: tracks, err: err}
	}
}

// loadLatestFromAllSubscriptions fetches every subscribed show's newest
// episode, in subscription order.
func (m *Model) loadLatestFromAllSubscriptions() tea.Cmd {
	if m.subs.loading || len(m.subs.shows) == 0 {
		return nil
	}
	loader := m.episodeLoader()
	if loader == nil {
		m.subs.err = "No provider can load episodes."
		return nil
	}
	shows := append([]provider.SubscriptionInfo(nil), m.subs.shows...)
	m.subs.loading = true
	m.subs.err = ""
	m.subs.status = fmt.Sprintf("Loading the newest episode of %d shows...", len(shows))

	return func() tea.Msg {
		latest := make([]*playlist.Track, len(shows))
		failed := make([]string, len(shows))

		jobs := make(chan int)
		var wg sync.WaitGroup
		workers := min(len(shows), subsLatestWorkers)
		wg.Add(workers)
		for range workers {
			go func() {
				defer wg.Done()
				for i := range jobs {
					tracks, err := loader.AlbumTracks(shows[i].ID)
					if err != nil {
						failed[i] = shows[i].Name
						continue
					}
					if track, ok := latestEpisode(tracks); ok {
						latest[i] = &track
					} else {
						failed[i] = shows[i].Name
					}
				}
			}()
		}
		for i := range shows {
			jobs <- i
		}
		close(jobs)
		wg.Wait()

		msg := subsLatestAllMsg{}
		for i := range shows {
			if latest[i] != nil {
				msg.tracks = append(msg.tracks, *latest[i])
			}
			if failed[i] != "" {
				msg.failed = append(msg.failed, failed[i])
			}
		}
		return msg
	}
}

// handleSubsEpisodes applies one show's fetched episodes.
func (m *Model) handleSubsEpisodes(msg subsEpisodesMsg) tea.Cmd {
	m.subs.loading = false
	m.subs.status = ""
	if msg.err != nil {
		m.subs.err = msg.err.Error()
		if !m.subs.visible {
			m.status.Errorf(statusTTLDefault, "%s: %s", msg.name, msg.err)
		}
		return nil
	}
	return m.appendSubscriptionTracks(msg.tracks, msg.mode, msg.name)
}

// addLatestSweep appends the newest episode of every subscribed show. The
// sweep builds a list to work through, so it queues nothing. It reports
// whether anything was added.
func (m *Model) addLatestSweep(msg subsLatestAllMsg) bool {
	m.subs.loading = false
	m.subs.status = ""
	if len(msg.tracks) == 0 {
		m.subs.err = "No episodes found across your subscriptions."
		return false
	}
	m.playlist.Add(msg.tracks...)
	m.loadedPlaylist = ""
	m.addToHeaderState(msg.tracks)
	if len(msg.failed) > 0 {
		m.subs.err = fmt.Sprintf("%d show(s) failed to load: %s", len(msg.failed), msg.failed[0])
	}
	m.status.Showf(statusTTLDefault, "Added the newest episode of %d show(s)", len(msg.tracks))
	return true
}

// handleSubsLatestAll applies the sweep and returns the command to run.
func (m *Model) handleSubsLatestAll(msg subsLatestAllMsg) tea.Cmd {
	if !m.addLatestSweep(msg) {
		return nil
	}
	return m.rearmPreload()
}
