package mass

import (
	"context"
	"errors"
	"strings"
)

// Player is a Music Assistant player or sync group.
type Player struct {
	ID        string
	Name      string
	Available bool
	State     string   // "playing", "paused", "idle"
	SyncedTo  string   // leader id when this player follows another
	Members   []string // follower ids when this player leads a group
	CanGroup  bool     // supports set_members
}

// Grouped reports whether the player leads or follows a sync group.
func (pl Player) Grouped() bool { return pl.SyncedTo != "" || len(pl.Members) > 0 }

type massPlayer struct {
	PlayerID          string   `json:"player_id"`
	DisplayName       string   `json:"display_name"`
	Name              string   `json:"name"`
	Available         bool     `json:"available"`
	HideInUI          bool     `json:"hide_in_ui"`
	PlaybackState     string   `json:"playback_state"`
	State             string   `json:"state"`
	SyncedTo          string   `json:"synced_to"`
	GroupMembers      []string `json:"group_members"`
	SupportedFeatures []string `json:"supported_features"`
	OutputProtocols   []struct {
		ID     string `json:"output_protocol_id"`
		Domain string `json:"protocol_domain"`
	} `json:"output_protocols"`
}

func (mp massPlayer) player() Player {
	name := mp.DisplayName
	if name == "" {
		name = mp.Name
	}
	state := mp.PlaybackState
	if state == "" {
		state = mp.State
	}
	pl := Player{
		ID:        mp.PlayerID,
		Name:      name,
		Available: mp.Available,
		State:     state,
		SyncedTo:  mp.SyncedTo,
	}
	// A leader lists itself in group_members.
	for _, id := range mp.GroupMembers {
		if id != mp.PlayerID {
			pl.Members = append(pl.Members, id)
		}
	}
	for _, f := range mp.SupportedFeatures {
		if f == "set_members" {
			pl.CanGroup = true
		}
	}
	return pl
}

func (p *Provider) allPlayers(ctx context.Context) ([]massPlayer, error) {
	var raw []massPlayer
	if err := p.rpcRequest(ctx, "players/all", nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// Players lists the players Music Assistant shows, available ones first in
// server order.
func (p *Provider) Players(ctx context.Context) ([]Player, error) {
	raw, err := p.allPlayers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Player, 0, len(raw))
	var off []Player
	for _, mp := range raw {
		if mp.HideInUI {
			continue
		}
		if pl := mp.player(); pl.Available {
			out = append(out, pl)
		} else {
			off = append(off, pl)
		}
	}
	return append(out, off...), nil
}

// ErrPlayerNotFound means Music Assistant has not registered the Sendspin
// client yet.
var ErrPlayerNotFound = errors.New("mass: Sendspin player not registered yet")

// SendspinPlayerID returns the id of the Music Assistant player that plays
// through the Sendspin client clientID. Music Assistant either exposes the
// client directly (player_id == clientID) or wraps it in a player that lists
// the client as its sendspin output protocol.
func (p *Provider) SendspinPlayerID(ctx context.Context, clientID string) (string, error) {
	raw, err := p.allPlayers(ctx)
	if err != nil {
		return "", err
	}
	direct := ""
	for _, mp := range raw {
		if mp.PlayerID == clientID {
			direct = mp.PlayerID
		}
		for _, op := range mp.OutputProtocols {
			if op.Domain == "sendspin" && strings.EqualFold(op.ID, clientID) {
				return mp.PlayerID, nil
			}
		}
	}
	if direct != "" {
		return direct, nil
	}
	return "", ErrPlayerNotFound
}

func (p *Provider) do(ctx context.Context, command string, args map[string]any) error {
	return p.rpcRequest(ctx, command, args, nil)
}

// PlayMedia replaces queueID's queue with uris and starts the first one.
func (p *Provider) PlayMedia(ctx context.Context, queueID string, uris []string) error {
	return p.do(ctx, "player_queues/play_media", map[string]any{
		"queue_id": queueID,
		"media":    uris,
		"option":   "replace",
	})
}

// Pause pauses queueID.
func (p *Provider) Pause(ctx context.Context, queueID string) error {
	return p.do(ctx, "player_queues/pause", map[string]any{"queue_id": queueID})
}

// Resume resumes queueID.
func (p *Provider) Resume(ctx context.Context, queueID string) error {
	return p.do(ctx, "player_queues/play", map[string]any{"queue_id": queueID})
}

// Next skips to the next queue item.
func (p *Provider) Next(ctx context.Context, queueID string) error {
	return p.do(ctx, "player_queues/next", map[string]any{"queue_id": queueID})
}

// Previous skips to the previous queue item.
func (p *Provider) Previous(ctx context.Context, queueID string) error {
	return p.do(ctx, "player_queues/previous", map[string]any{"queue_id": queueID})
}

// Join syncs playerID to leaderID's playback.
func (p *Provider) Join(ctx context.Context, leaderID, playerID string) error {
	return p.do(ctx, "players/cmd/set_members", map[string]any{
		"target_player":     leaderID,
		"player_ids_to_add": []string{playerID},
	})
}

// Leave removes playerID from whatever sync group it is in.
func (p *Provider) Leave(ctx context.Context, playerID string) error {
	return p.do(ctx, "players/cmd/ungroup", map[string]any{"player_id": playerID})
}
