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
	rating   int
	err      error
}

func ratingSongID(track playlist.Track) string {
	return track.Meta(provider.MetaNavidromeID)
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
	songID := track.Meta(provider.MetaNavidromeID)
	if songID == "" {
		songID = track.Path
	}
	syncKey := name + "\x00" + songID
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
			rating:   rating,
			err:      err,
		}
	}
}

func (m *Model) handleTrackRatingSynced(msg trackRatingSyncedMsg) {
	if m.playlist == nil {
		return
	}
	songID := ratingSongID(msg.track)
	if songID == "" {
		return
	}
	var index int
	var track playlist.Track
	found := false
	for i := 0; i < m.playlist.Len(); i++ {
		candidate, ok := m.playlist.Track(i)
		if ok && ratingSongID(candidate) == songID {
			index, track, found = i, candidate, true
			break
		}
	}
	if !found {
		return
	}
	if msg.err != nil {
		m.status.Warningf(statusTTLMedium, "%s did not save rating: %s", msg.provider, msg.err)
		return
	}
	track.UserRating = msg.rating
	m.playlist.SetTrack(index, track)
	if m.playingTrackActive && !m.playbackDetached && ratingSongID(m.playingTrack) == songID {
		m.playingTrack.UserRating = msg.rating
	}
	if msg.rating == 0 {
		m.status.Showf(statusTTLMedium, "Cleared rating: %s", track.DisplayName())
	} else {
		m.status.Showf(statusTTLMedium, "Rated %d/5: %s", msg.rating, track.DisplayName())
	}
}
