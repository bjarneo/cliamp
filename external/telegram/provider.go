// Package telegram implements a playlist.Provider backed by a Telegram chat.
//
// It talks MTProto directly through gotd/td: no daemon, no helper process.
// The chat is configured in config.toml under [telegram].
package telegram

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/gopxl/beep/v2"
	"github.com/gotd/td/session"
	tgclient "github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
)

// Compile-time interface checks.
var (
	_ playlist.Provider       = (*Provider)(nil)
	_ provider.CustomStreamer = (*Provider)(nil)
	_ provider.TrackPager     = (*Provider)(nil)
)

const (
	uriPrefix       = "telegram:"
	connectWait     = 45 * time.Second
	trackPageSize   = 100
	dialogsPageSize = 100
)

// Provider lists the audio of one Telegram chat and streams its documents.
type Provider struct {
	cfg         config.TelegramConfig
	client      *tgclient.Client
	api         *tg.Client
	ready       chan struct{}
	closeOnce   sync.Once
	connectErr  error
	cacheDir    string
	docsMu      sync.Mutex
	docs        map[int]*tg.Document
	downloadMu  sync.Mutex
	interactive bool
}

// NewFromConfig returns nil when [telegram] is not configured.
func NewFromConfig(cfg config.TelegramConfig, interactive bool) *Provider {
	if !cfg.IsSet() {
		return nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = os.TempDir()
	}
	p := &Provider{
		cfg:         cfg,
		ready:       make(chan struct{}),
		cacheDir:    filepath.Join(cache, "cliamp", "telegram"),
		docs:        make(map[int]*tg.Document),
		interactive: interactive,
	}
	go p.run()
	// The sign-in prompt writes to stdin/stderr and must happen before the TUI
	// takes over the terminal.
	select {
	case <-p.ready:
	case <-time.After(connectWait):
	}
	return p
}

func (p *Provider) sessionPath() string {
	path := p.cfg.SessionFile
	if path == "" {
		path = "~/.local/share/cliamp/telegram.session"
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	return path
}

func (p *Provider) run() {
	if err := os.MkdirAll(filepath.Dir(p.sessionPath()), 0o755); err != nil {
		p.finish(err)
		return
	}
	p.client = tgclient.NewClient(p.cfg.AppID, p.cfg.AppHash, tgclient.Options{
		SessionStorage: &session.FileStorage{Path: p.sessionPath()},
	})
	err := p.client.Run(context.Background(), func(ctx context.Context) error {
		status, err := p.client.Auth().Status(ctx)
		if err != nil {
			return err
		}
		if !status.Authorized {
			if !p.interactive {
				return playlist.ErrNeedsAuth
			}
			if err := p.login(ctx); err != nil {
				return err
			}
		}
		p.api = p.client.API()
		p.finish(nil)
		// Keep the client (and its connection) alive for the provider lifetime.
		<-ctx.Done()
		return nil
	})
	if err != nil {
		p.finish(err)
	}
}

func (p *Provider) finish(err error) {
	p.closeOnce.Do(func() {
		p.connectErr = err
		close(p.ready)
	})
}

func (p *Provider) login(ctx context.Context) error {
	if p.cfg.Phone == "" {
		return errors.New("telegram: [telegram] phone is required to sign in")
	}
	code := auth.CodeAuthenticatorFunc(func(_ context.Context, _ *tg.AuthSentCode) (string, error) {
		fmt.Fprint(os.Stderr, "Telegram login code: ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(line), nil
	})
	flow := auth.NewFlow(
		auth.Constant(p.cfg.Phone, p.cfg.Password, code),
		auth.SendCodeOptions{},
	)
	if err := flow.Run(ctx, p.client.Auth()); err != nil {
		return fmt.Errorf("telegram: sign in failed: %w", err)
	}
	return nil
}

func (p *Provider) wait() error {
	select {
	case <-p.ready:
	case <-time.After(connectWait):
		return errors.New("telegram: still connecting, try again")
	}
	if p.connectErr != nil {
		return p.connectErr
	}
	if p.api == nil {
		return playlist.ErrNeedsAuth
	}
	return nil
}

// Name implements playlist.Provider.
func (p *Provider) Name() string { return "Telegram" }

// Playlists implements playlist.Provider. With [telegram] chat set it returns
// only that chat; otherwise every dialog of the account, grouped by kind.
func (p *Provider) Playlists() ([]playlist.PlaylistInfo, error) {
	if err := p.wait(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if p.cfg.Chat != "" {
		peer, err := p.resolvePeer(ctx, p.cfg.Chat)
		if err != nil {
			return nil, err
		}
		return []playlist.PlaylistInfo{{
			ID:   peerID(peer),
			Name: p.cfg.Chat,
		}}, nil
	}

	dialogs, err := p.api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
		Limit:      dialogsPageSize,
		OffsetPeer: &tg.InputPeerEmpty{},
	})
	if err != nil {
		return nil, fmt.Errorf("telegram: dialogs: %w", err)
	}
	out := dialogsAsPlaylists(dialogs)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Section != out[j].Section {
			return out[i].Section < out[j].Section
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// Tracks implements playlist.Provider.
func (p *Provider) Tracks(playlistID string) ([]playlist.Track, error) {
	tracks, _, err := p.TracksPage(playlistID, 0)
	return tracks, err
}

// TracksPage implements provider.TrackPager: it walks the chat history in
// pages of trackPageSize messages and returns the id of the last message as
// the next offset (0 when the history is exhausted).
func (p *Provider) TracksPage(playlistID string, offset int) ([]playlist.Track, int, error) {
	if err := p.wait(); err != nil {
		return nil, 0, err
	}
	peer, err := parsePeerID(playlistID)
	if err != nil {
		return nil, 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	history, err := p.api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer:     peer,
		OffsetID: offset,
		Limit:    trackPageSize,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("telegram: history of %q: %w", playlistID, err)
	}

	raw := messagesOf(history)
	tracks := make([]playlist.Track, 0, len(raw))
	lastID := 0
	for _, message := range raw {
		msg, ok := message.(*tg.Message)
		if !ok {
			continue
		}
		if lastID == 0 || msg.ID < lastID {
			lastID = msg.ID
		}
		media, ok := msg.Media.(*tg.MessageMediaDocument)
		if !ok {
			continue
		}
		doc, ok := media.Document.(*tg.Document)
		if !ok {
			continue
		}
		info, ok := audioInfo(doc)
		if !ok {
			continue
		}
		p.docsMu.Lock()
		p.docs[msg.ID] = doc
		p.docsMu.Unlock()
		track := playlist.Track{
			Path:         fmt.Sprintf("%s%s:%d", uriPrefix, playlistID, msg.ID),
			Title:        info.title,
			Artist:       info.performer,
			DurationSecs: info.duration,
			ProviderMeta: map[string]string{"telegram.id": strconv.Itoa(msg.ID)},
		}
		tracks = append(tracks, track)
	}
	next := 0
	if len(raw) >= trackPageSize {
		next = lastID
	}
	return tracks, next, nil
}

// dialogsAsPlaylists maps the dialogs of a getDialogs response to playlists.
func dialogsAsPlaylists(dialogs tg.MessagesDialogsClass) []playlist.PlaylistInfo {
	var (
		users  []tg.UserClass
		chats  []tg.ChatClass
		result []playlist.PlaylistInfo
	)
	switch value := dialogs.(type) {
	case *tg.MessagesDialogs:
		users, chats = value.Users, value.Chats
	case *tg.MessagesDialogsSlice:
		users, chats = value.Users, value.Chats
	default:
		return nil
	}
	for _, entity := range users {
		user, ok := entity.(*tg.User)
		if !ok || user.Self || user.Deleted {
			continue
		}
		name := strings.TrimSpace(user.FirstName + " " + user.LastName)
		if name == "" {
			name = user.Username
		}
		if name == "" {
			continue
		}
		section := "Chats"
		if user.Bot {
			section = "Bots"
		}
		result = append(result, playlist.PlaylistInfo{
			ID:      fmt.Sprintf("u:%d:%d", user.ID, user.AccessHash),
			Name:    name,
			Section: section,
		})
	}
	for _, entity := range chats {
		switch chat := entity.(type) {
		case *tg.Channel:
			result = append(result, playlist.PlaylistInfo{
				ID:      fmt.Sprintf("ch:%d:%d", chat.ID, chat.AccessHash),
				Name:    chat.Title,
				Section: "Canales",
			})
		case *tg.Chat:
			result = append(result, playlist.PlaylistInfo{
				ID:      fmt.Sprintf("c:%d", chat.ID),
				Name:    chat.Title,
				Section: "Grupos",
			})
		}
	}
	return result
}

// peerID encodes an input peer so it can round-trip through a playlist id.
func peerID(peer tg.InputPeerClass) string {
	switch value := peer.(type) {
	case *tg.InputPeerUser:
		return fmt.Sprintf("u:%d:%d", value.UserID, value.AccessHash)
	case *tg.InputPeerChannel:
		return fmt.Sprintf("ch:%d:%d", value.ChannelID, value.AccessHash)
	case *tg.InputPeerChat:
		return fmt.Sprintf("c:%d", value.ChatID)
	default:
		return ""
	}
}

// parsePeerID decodes the ids produced by peerID and dialogsAsPlaylists.
func parsePeerID(id string) (tg.InputPeerClass, error) {
	parts := strings.Split(id, ":")
	switch parts[0] {
	case "u", "ch":
		if len(parts) != 3 {
			break
		}
		entityID, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			break
		}
		accessHash, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			break
		}
		if parts[0] == "u" {
			return &tg.InputPeerUser{UserID: entityID, AccessHash: accessHash}, nil
		}
		return &tg.InputPeerChannel{ChannelID: entityID, AccessHash: accessHash}, nil
	case "c":
		if len(parts) != 2 {
			break
		}
		chatID, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			break
		}
		return &tg.InputPeerChat{ChatID: chatID}, nil
	}
	return nil, fmt.Errorf("telegram: invalid playlist id %q", id)
}

type audioMetadata struct {
	title     string
	performer string
	duration  int
}

func audioInfo(doc *tg.Document) (audioMetadata, bool) {
	var meta audioMetadata
	filename := ""
	isAudio := strings.HasPrefix(doc.MimeType, "audio/")
	for _, attribute := range doc.Attributes {
		switch attr := attribute.(type) {
		case *tg.DocumentAttributeAudio:
			meta.title = attr.Title
			meta.performer = attr.Performer
			meta.duration = attr.Duration
			isAudio = true
		case *tg.DocumentAttributeFilename:
			filename = attr.FileName
		}
	}
	if !isAudio {
		return meta, false
	}
	if meta.title == "" && filename != "" {
		meta.title = strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	}
	if meta.title == "" {
		meta.title = "Telegram audio"
	}
	return meta, true
}

func messagesOf(history tg.MessagesMessagesClass) []tg.MessageClass {
	switch value := history.(type) {
	case *tg.MessagesMessages:
		return value.Messages
	case *tg.MessagesMessagesSlice:
		return value.Messages
	case *tg.MessagesChannelMessages:
		return value.Messages
	default:
		return nil
	}
}

func (p *Provider) resolvePeer(ctx context.Context, chat string) (tg.InputPeerClass, error) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(chat), "@")
	resolved, err := p.api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: trimmed})
	if err != nil {
		return nil, fmt.Errorf("telegram: cannot resolve %q: %w", trimmed, err)
	}
	switch peer := resolved.Peer.(type) {
	case *tg.PeerUser:
		for _, entity := range resolved.Users {
			if user, ok := entity.(*tg.User); ok && user.ID == peer.UserID {
				return &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}, nil
			}
		}
	case *tg.PeerChat:
		return &tg.InputPeerChat{ChatID: peer.ChatID}, nil
	case *tg.PeerChannel:
		for _, entity := range resolved.Chats {
			if channel, ok := entity.(*tg.Channel); ok && channel.ID == peer.ChannelID {
				return &tg.InputPeerChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash}, nil
			}
		}
	}
	return nil, fmt.Errorf("telegram: unsupported peer for %q", trimmed)
}

// URISchemes implements provider.CustomStreamer.
func (p *Provider) URISchemes() []string { return []string{"telegram"} }

// NewStreamer implements provider.CustomStreamer: it starts playback as soon
// as the configured buffer is on disk and keeps downloading in the background.
func (p *Provider) NewStreamer(uri string) (beep.StreamSeekCloser, beep.Format, time.Duration, error) {
	if err := p.wait(); err != nil {
		return nil, beep.Format{}, 0, err
	}
	chat, msgID, err := parseURI(uri)
	if err != nil {
		return nil, beep.Format{}, 0, err
	}
	return p.openStream(chat, msgID)
}

func parseURI(uri string) (string, int, error) {
	if !strings.HasPrefix(uri, uriPrefix) {
		return "", 0, fmt.Errorf("telegram: invalid track uri %q", uri)
	}
	rest := strings.TrimPrefix(uri, uriPrefix)
	sep := strings.LastIndex(rest, ":")
	if sep <= 0 {
		return "", 0, fmt.Errorf("telegram: invalid track uri %q", uri)
	}
	id, err := strconv.Atoi(rest[sep+1:])
	if err != nil {
		return "", 0, fmt.Errorf("telegram: invalid message id in %q", uri)
	}
	return rest[:sep], id, nil
}

func docDuration(doc *tg.Document) int {
	for _, attribute := range doc.Attributes {
		if audio, ok := attribute.(*tg.DocumentAttributeAudio); ok {
			return audio.Duration
		}
	}
	return 0
}

func safeChat(chat string) string {
	return strings.NewReplacer("/", "_", "\\", "_", ":", "_", "@", "").Replace(chat)
}

func extensionFor(doc *tg.Document) string {
	for _, attribute := range doc.Attributes {
		if filename, ok := attribute.(*tg.DocumentAttributeFilename); ok {
			if ext := strings.ToLower(filepath.Ext(filename.FileName)); ext != "" {
				return ext
			}
		}
	}
	switch strings.ToLower(doc.MimeType) {
	case "audio/mpeg":
		return ".mp3"
	case "audio/ogg", "audio/opus":
		return ".ogg"
	default:
		return ".flac"
	}
}
