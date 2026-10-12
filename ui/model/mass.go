package model

import (
	"context"
	"errors"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/external/mass"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/sendspin"
)

// massCommandTimeout bounds one Music Assistant call made for a key press.
// It covers the wait for a new speaker to be listed: Music Assistant takes
// about 15 seconds to list a Sendspin player after it connects.
const massCommandTimeout = 25 * time.Second

// massRegisterPoll is how often the speaker list is checked while waiting.
const massRegisterPoll = 500 * time.Millisecond

// massQueueLimit caps how many playlist rows are sent to Music Assistant when
// a row is played, so it keeps playing past the selected row.
const massQueueLimit = 200

// MusicAssistant is the part of the Music Assistant provider the model drives.
type MusicAssistant interface {
	SendspinPlayerID(ctx context.Context, clientID string) (string, error)
	Players(ctx context.Context) ([]mass.Player, error)
	PlayMedia(ctx context.Context, queueID string, uris []string) error
	Pause(ctx context.Context, queueID string) error
	Resume(ctx context.Context, queueID string) error
	Next(ctx context.Context, queueID string) error
	Previous(ctx context.Context, queueID string) error
	Join(ctx context.Context, leaderID, playerID string) error
	Leave(ctx context.Context, playerID string) error
}

// SendspinRenderer is the local Sendspin player Music Assistant streams to.
type SendspinRenderer interface {
	ClientID() string
	Connected() bool
	Streaming() bool
	NowPlaying() sendspin.TrackInfo
}

var errRendererOffline = errors.New("the Sendspin player is not connected to Music Assistant")

// massLink pairs the provider with the renderer and remembers which Music
// Assistant player wraps the renderer. Commands run off the Update goroutine,
// so the id is guarded.
type massLink struct {
	ma MusicAssistant
	r  SendspinRenderer

	mu       sync.Mutex
	playerID string
}

// localPlayerID returns the id of the Music Assistant player that wraps the
// renderer, waiting until ctx ends for Music Assistant to list it.
func (l *massLink) localPlayerID(ctx context.Context) (string, error) {
	if !l.r.Connected() {
		return "", errRendererOffline
	}
	l.mu.Lock()
	id := l.playerID
	l.mu.Unlock()
	if id != "" {
		return id, nil
	}
	for {
		id, err := l.ma.SendspinPlayerID(ctx, l.r.ClientID())
		if err == nil {
			l.mu.Lock()
			l.playerID = id
			l.mu.Unlock()
			return id, nil
		}
		if !errors.Is(err, mass.ErrPlayerNotFound) {
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", err
		case <-time.After(massRegisterPoll):
		}
	}
}

// SetMusicAssistant connects the Music Assistant provider and the local
// Sendspin player. Without it, Music Assistant rows cannot be played.
func (m *Model) SetMusicAssistant(ma MusicAssistant, r SendspinRenderer) {
	m.massLink = &massLink{ma: ma, r: r}
}

// massOwnsSpeaker reports whether Music Assistant's stream is what plays now,
// so transport keys should drive its queue instead of the local playlist.
func (m *Model) massOwnsSpeaker() bool {
	return m.massLink != nil && m.massLink.r.Streaming()
}

// massRun runs fn with the local player id in a command and reports failure
// in the status bar.
func (m *Model) massRun(what string, fn func(ctx context.Context, ma MusicAssistant, id string) error) tea.Cmd {
	link := m.massLink
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), massCommandTimeout)
		defer cancel()
		id, err := link.localPlayerID(ctx)
		if err == nil {
			err = fn(ctx, link.ma, id)
		}
		if err != nil {
			return ShowStatusMsg{Text: "Music Assistant " + what + ": " + err.Error()}
		}
		return nil
	}
}

// playOnMusicAssistant plays a Music Assistant row: Music Assistant queues it
// and the rows after it on the local player, which streams back over
// Sendspin. It reports false for rows from other sources.
func (m *Model) playOnMusicAssistant(track playlist.Track) (tea.Cmd, bool) {
	if !mass.IsTrackPath(track.Path) {
		return nil, false
	}
	if m.massLink == nil {
		m.status.Show("Music Assistant playback needs the Sendspin player; see docs/music-assistant.md", statusTTLLong)
		return nil, true
	}
	uris := m.massQueueFrom(track)
	cmd := m.massRun("play", func(ctx context.Context, ma MusicAssistant, id string) error {
		return ma.PlayMedia(ctx, id, uris)
	})
	if !m.massOwnsSpeaker() {
		cmd = tea.Batch(cmd, playStreamCmd(m.player, sendspin.URI, 0, func() time.Duration { return 0 }, m.requests.stream))
	}
	m.status.Activity("Starting on Music Assistant...", statusTTLDefault)
	return cmd, true
}

// massQueueFrom returns the Music Assistant URIs of track and the rows that
// follow it in the playlist.
func (m *Model) massQueueFrom(track playlist.Track) []string {
	uris := []string{track.ProviderMeta[mass.MetaURI]}
	tracks := m.playlist.Tracks()
	start := m.playlist.Index()
	if start < 0 || start >= len(tracks) || tracks[start].Path != track.Path {
		return uris
	}
	for _, t := range tracks[start+1:] {
		if len(uris) == massQueueLimit {
			break
		}
		if uri := t.ProviderMeta[mass.MetaURI]; mass.IsTrackPath(t.Path) && uri != "" {
			uris = append(uris, uri)
		}
	}
	return uris
}

// massTransport sends a transport key to Music Assistant when its stream owns
// the speaker. It returns nil otherwise.
func (m *Model) massTransport(kind string) tea.Cmd {
	if !m.massOwnsSpeaker() {
		return nil
	}
	return m.massRun(kind, func(ctx context.Context, ma MusicAssistant, id string) error {
		switch kind {
		case "pause":
			return ma.Pause(ctx, id)
		case "resume":
			return ma.Resume(ctx, id)
		case "next":
			return ma.Next(ctx, id)
		case "previous":
			return ma.Previous(ctx, id)
		}
		return nil
	})
}

// releaseMusicAssistant pauses Music Assistant when local playback takes the
// speaker, so its queue does not run on unheard.
func (m *Model) releaseMusicAssistant() tea.Cmd {
	return m.massTransport("pause")
}

// applyMassNowPlaying shows Music Assistant's current track while its stream
// plays.
func (m *Model) applyMassNowPlaying() {
	if !m.massOwnsSpeaker() || m.player == nil {
		return
	}
	info := m.massLink.r.NowPlaying()
	if info.Title == "" {
		return
	}
	track := m.playingTrack
	track.Path = sendspin.URI
	track.Title = info.Title
	track.Artist = info.Artist
	track.Album = info.Album
	track.AlbumArtURL = info.ArtworkURL
	track.DurationSecs = info.DurationSecs
	track.Stream = true
	changed := track.Title != m.playingTrack.Title || track.Artist != m.playingTrack.Artist ||
		track.Album != m.playingTrack.Album || track.DurationSecs != m.playingTrack.DurationSecs ||
		track.AlbumArtURL != m.playingTrack.AlbumArtURL
	m.playingTrack = track
	m.playingTrackActive = true
	if !changed {
		return
	}
	if info.DurationSecs > 0 {
		if setter, ok := m.player.(interface{ SetKnownDuration(time.Duration) }); ok {
			setter.SetKnownDuration(time.Duration(info.DurationSecs) * time.Second)
		}
	}
	m.notice.sent = false
	m.notifyPlaybackChange()
}
