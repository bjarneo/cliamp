package model

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

const trackRatingTimeout = 30 * time.Second

type trackRatingSyncedMsg struct {
	provider string
	track    playlist.Track
	request  uint64
	rating   int
	err      error
}

func ratingTrackIdentity(track playlist.Track) string {
	if songID := track.Meta(provider.MetaNavidromeID); songID != "" {
		return songID
	}
	return track.Path
}

func (m *Model) findTrackRater(track playlist.Track) (provider.TrackRater, string) {
	return findCapable(m, func(r provider.TrackRater) bool { return r.CanRateTrack(track) })
}

func (m *Model) rateSelectedTrack(rating int) tea.Cmd {
	if rating < 0 || rating > 5 || m.playlist == nil || m.plCursor < 0 || m.plCursor >= m.playlist.Len() {
		return nil
	}
	track, ok := m.playlist.Track(m.plCursor)
	if !ok {
		return nil
	}
	rater, name := m.findTrackRater(track)
	if rater == nil {
		m.status.Warningf(statusTTLMedium, "Rating is not available for %s", track.DisplayName())
		return nil
	}
	if m.ratingSync == nil {
		m.ratingSync = &favorites.SyncQueue{}
	}
	identity := ratingTrackIdentity(track)
	syncKey := name + "\x00" + identity
	if m.ratingLatest == nil {
		m.ratingLatest = make(map[string]uint64)
	}
	m.ratingRequest++
	request := m.ratingRequest
	m.ratingLatest[syncKey] = request
	run := m.ratingSync.Enqueue(syncKey, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), trackRatingTimeout)
		defer cancel()
		return rater.SetTrackRating(ctx, track, rating)
	})
	return func() tea.Msg {
		ran, err := run()
		if !ran {
			return nil
		}
		return trackRatingSyncedMsg{
			provider: name,
			track:    track,
			request:  request,
			rating:   rating,
			err:      err,
		}
	}
}

func (m *Model) handleTrackRatingSynced(msg trackRatingSyncedMsg) {
	if m.playlist == nil {
		return
	}
	identity := ratingTrackIdentity(msg.track)
	if identity == "" {
		return
	}
	syncKey := msg.provider + "\x00" + identity
	if msg.request != 0 && m.ratingLatest[syncKey] > msg.request {
		return
	}
	var matched []int
	var displayTrack playlist.Track
	for i := 0; i < m.playlist.Len(); i++ {
		candidate, ok := m.playlist.Track(i)
		if ok && ratingTrackIdentity(candidate) == identity {
			if len(matched) == 0 {
				displayTrack = candidate
			}
			matched = append(matched, i)
		}
	}
	if len(matched) == 0 {
		return
	}
	if msg.err != nil {
		m.status.Warningf(statusTTLMedium, "%s did not save rating: %s", msg.provider, msg.err)
		return
	}
	for _, index := range matched {
		track, _ := m.playlist.Track(index)
		track.UserRating = msg.rating
		m.playlist.SetTrack(index, track)
	}
	if m.playingTrackActive && !m.playbackDetached && ratingTrackIdentity(m.playingTrack) == identity {
		m.playingTrack.UserRating = msg.rating
	}
	if msg.rating == 0 {
		m.status.Showf(statusTTLMedium, "Cleared rating: %s", displayTrack.DisplayName())
	} else {
		m.status.Showf(statusTTLMedium, "Rated %d/5: %s", msg.rating, displayTrack.DisplayName())
	}
}
