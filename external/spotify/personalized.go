package spotify

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/devgianlu/go-librespot/spclient"
)

const (
	madeForYouSection    = "Made for You"
	homeShortcutsSection = "Home shortcuts"
	madeForYouShelf      = "spotify:section:0JQ5DAUnp4wcj0bCb3wh3S"
	homeShortcutsShelf   = "spotify:section:0JQ5DAIiKWzVFULQfUm85Y"
)

func (p *SpotifyProvider) personalizedPlaylists(ctx context.Context) ([]playlist.PlaylistInfo, error) {
	p.mu.Lock()
	sess := p.session
	p.mu.Unlock()
	if sess == nil {
		return nil, playlist.ErrNeedsAuth
	}
	sess.mu.RLock()
	defer sess.mu.RUnlock()
	if sess.sess == nil {
		return nil, nil
	}
	return fetchPersonalizedPlaylists(ctx, sess.sess.Spclient())
}

func fetchPersonalizedPlaylists(ctx context.Context, sp *spclient.Spclient) ([]playlist.PlaylistInfo, error) {
	hub, hubErr := fetchMadeForYouHub(ctx, sp)
	home, homeErr := fetchHomePlaylists(ctx, sp)
	seen := make(map[string]bool)
	all := make([]playlist.PlaylistInfo, 0, len(hub)+len(home))
	for _, list := range append(hub, home...) {
		if !seen[list.ID] {
			seen[list.ID] = true
			all = append(all, list)
		}
	}
	return all, errors.Join(hubErr, homeErr)
}

type homeFeedCard struct {
	Component struct {
		Category string `json:"category"`
	} `json:"component"`
	Text struct {
		Title string `json:"title"`
	} `json:"text"`
	Metadata struct {
		URI       string `json:"uri"`
		SectionID string `json:"sectionId"`
	} `json:"metadata"`
	Children []homeFeedCard `json:"children"`
}

type homeFeed struct {
	ID   string         `json:"id"`
	Body []homeFeedCard `json:"body"`
}

func fetchMadeForYouHub(ctx context.Context, sp *spclient.Spclient) ([]playlist.PlaylistInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := sp.Request(ctx, http.MethodGet, "/hubview-mobile-v1/browse/0JQ5DAt0tbjZptfcdMSKl3", url.Values{
		"platform": {"android"}, "locale": {"en"}, "podcast": {"false"}, "client-timezone": {"UTC"},
	}, http.Header{
		"Accept": {"application/json"}, "App-Platform": {"Android"},
		"User-Agent": {"Spotify/8.9.96.476 Android/31 (Android SDK built for x86_64)"},
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("spotify: Made for You hub: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("spotify: Made for You hub: HTTP %d", resp.StatusCode)
	}
	var hub homeFeed
	if err := decodeBody(resp, &hub); err != nil {
		return nil, fmt.Errorf("spotify: parse Made for You hub: %w", err)
	}
	if hub.ID != "mfy-hub" {
		return nil, fmt.Errorf("spotify: Made for You hub unavailable")
	}
	var lists []playlist.PlaylistInfo
	cards := 0
	for _, row := range hub.Body {
		for _, card := range row.Children {
			cards++
			if cards > 1000 {
				return nil, fmt.Errorf("spotify: Made for You hub exceeded 1000 cards")
			}
			if card.Component.Category != "card" || card.Text.Title == "" {
				continue
			}
			if _, ok := playlistURLID(card.Metadata.URI); ok {
				lists = append(lists, playlist.PlaylistInfo{ID: card.Metadata.URI, Name: card.Text.Title, Section: madeForYouSection})
			}
		}
	}
	return lists, nil
}

func fetchHomePlaylists(ctx context.Context, sp *spclient.Spclient) ([]playlist.PlaylistInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := sp.Request(ctx, http.MethodGet, "/homeview/v1/home", url.Values{"platform": {"desktop"}, "locale": {"en"}}, http.Header{"Accept": {"application/json"}}, nil)
	if err != nil {
		return nil, fmt.Errorf("spotify: Made for You discovery: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("spotify: Made for You discovery: HTTP %d", resp.StatusCode)
	}
	var home homeFeed
	if err := decodeBody(resp, &home); err != nil {
		return nil, fmt.Errorf("spotify: parse Made for You: %w", err)
	}
	found := false
	for _, card := range home.Body {
		if card.Metadata.SectionID == madeForYouShelf {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("spotify: Made for You shelf is unavailable in Home")
	}
	var lists []playlist.PlaylistInfo
	seen := make(map[string]bool)
	for _, shelf := range []struct{ id, name string }{{madeForYouShelf, madeForYouSection}, {homeShortcutsShelf, homeShortcutsSection}} {
		for _, card := range home.Body {
			if card.Metadata.SectionID != shelf.id || card.Component.Category != "card" || card.Text.Title == "" {
				continue
			}
			uri := card.Metadata.URI
			if _, ok := playlistURLID(uri); !ok || seen[uri] {
				continue
			}
			seen[uri] = true
			lists = append(lists, playlist.PlaylistInfo{ID: uri, Name: card.Text.Title, Section: shelf.name})
		}
	}
	return lists, nil
}
