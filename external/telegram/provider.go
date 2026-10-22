// Package telegram implements a playlist.Provider backed by a Telegram chat.
//
// It talks MTProto directly through gotd/td: no daemon, no helper process.
// The chat is configured in config.toml under [telegram].
package telegram

import (
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
	loginWait       = 5 * time.Minute
	trackPageSize   = 100
	dialogsPageSize = 100
	dialogsMaxPages = 10
	// Telegram rate-limits messages.getDialogs, so pages are paced and a
	// FLOOD_WAIT is waited out instead of failing the whole list.
	dialogsPageDelay = 250 * time.Millisecond
	floodAttempts    = 3
	floodMaxWait     = time.Minute
)

// msgKey identifies a document. Telegram message IDs are unique per chat, not
// per account, so a bare ID collides between chats.
type msgKey struct {
	chat string
	id   int
}

// Provider lists the audio of one Telegram chat and streams its documents.
type Provider struct {
	cfg         config.TelegramConfig
	client      *tgclient.Client
	api         *tg.Client
	ready       chan struct{}
	signIn      chan struct{}
	signInOnce  sync.Once
	closeOnce   sync.Once
	connectErr  error
	cacheDir    string
	artMu       sync.Mutex
	artCache    map[msgKey]string
	chatsMu     sync.Mutex
	chatsCache  map[string]playlist.PlaylistInfo
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
		signIn:      make(chan struct{}),
		cacheDir:    filepath.Join(cache, "cliamp", "telegram"),
		artCache:    make(map[msgKey]string),
		interactive: interactive,
	}
	go p.run()
	// The sign-in prompt writes to stdin/stderr and must happen before the TUI
	// takes over the terminal. Connecting keeps the 45s budget, because a slow
	// or unreachable network must not hold the interface back; once the sign-in
	// starts, reading the code from the user gets its own budget so the TUI
	// does not take the terminal while the reader is still on stdin.
	select {
	case <-p.ready:
	case <-p.signIn:
		select {
		case <-p.ready:
		case <-time.After(loginWait):
		}
	case <-time.After(connectWait):
	}
	return p
}

func (p *Provider) sessionPath() string { return sessionPath(p.cfg.SessionFile) }

func (p *Provider) run() {
	if err := os.MkdirAll(filepath.Dir(p.sessionPath()), 0o700); err != nil {
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
	p.signInOnce.Do(func() { close(p.signIn) })
	if p.cfg.Phone == "" {
		return errors.New("telegram: [telegram] phone is required to sign in")
	}
	flow := auth.NewFlow(
		auth.Constant(normalizePhone(p.cfg.Phone), p.cfg.Password, promptCode()),
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

	if len(p.cfg.Chats) > 0 {
		out := make([]playlist.PlaylistInfo, 0, len(p.cfg.Chats))
		seen := make(map[string]struct{}, len(p.cfg.Chats))
		for _, chat := range p.cfg.Chats {
			info, err := p.resolveChatCached(ctx, chat)
			if err != nil {
				return nil, err
			}
			if _, duplicate := seen[info.ID]; duplicate {
				continue
			}
			seen[info.ID] = struct{}{}
			out = append(out, info)
		}
		return out, nil
	}

	out, err := p.dialogs(ctx)
	if err != nil {
		return nil, err
	}
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
		// The offset for the next page comes from every message class: a page
		// with only service items (joins, pins) still advances the history.
		if id := message.GetID(); lastID == 0 || id < lastID {
			lastID = id
		}
		msg, ok := message.(*tg.Message)
		if !ok {
			continue
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
		track := playlist.Track{
			Path:         fmt.Sprintf("%s%s:%d", uriPrefix, playlistID, msg.ID),
			Title:        info.title,
			Artist:       info.performer,
			DurationSecs: info.duration,
			ProviderMeta: map[string]string{"telegram.id": strconv.Itoa(msg.ID)},
		}
		if cover := p.coverURL(playlistID, msg.ID); cover != "" {
			track.AlbumArtURL = cover
		}
		tracks = append(tracks, track)
	}
	next := 0
	if len(raw) >= trackPageSize {
		next = lastID
	}
	return tracks, next, nil
}

// dialogs returns every dialog of the account. Telegram caps one
// messages.getDialogs response at dialogsPageSize and answers the rest with a
// slice, so the list is paged until the last page.
func (p *Provider) dialogs(ctx context.Context) ([]playlist.PlaylistInfo, error) {
	var out []playlist.PlaylistInfo
	offset := tg.InputPeerClass(&tg.InputPeerEmpty{})
	offsetID, offsetDate := 0, 0
	for range dialogsMaxPages {
		resp, err := p.getDialogs(ctx, offset, offsetID, offsetDate)
		if err != nil {
			return nil, fmt.Errorf("telegram: dialogs: %w", err)
		}
		out = append(out, dialogsAsPlaylists(resp)...)
		time.Sleep(dialogsPageDelay)

		slice, ok := resp.(*tg.MessagesDialogsSlice)
		if !ok || len(slice.Dialogs) < dialogsPageSize {
			return out, nil
		}
		last, ok := slice.Dialogs[len(slice.Dialogs)-1].(*tg.Dialog)
		if !ok {
			return out, nil
		}
		peer, ok := inputPeer(last.Peer, slice.Users, slice.Chats)
		if !ok {
			return out, nil
		}
		offset, offsetID, offsetDate = peer, last.TopMessage, topMessageDate(slice.Messages, last.Peer, last.TopMessage)
	}
	return out, nil
}

// getDialogs requests one page of dialogs. A FLOOD_WAIT is waited out while
// the wait is short: Telegram answers with one when a large account is paged,
// and failing the whole list for a few seconds of throttling is worse than
// waiting.
func (p *Provider) getDialogs(ctx context.Context, offset tg.InputPeerClass, offsetID, offsetDate int) (tg.MessagesDialogsClass, error) {
	return withFloodRetry(ctx, func() (tg.MessagesDialogsClass, error) {
		return p.api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
			Limit:      dialogsPageSize,
			OffsetPeer: offset,
			OffsetID:   offsetID,
			OffsetDate: offsetDate,
		})
	})
}

// withFloodRetry runs call and waits out a short FLOOD_WAIT. Telegram answers
// with one when a large account is paged or when several usernames are
// resolved in a row; failing the whole list for a few seconds of throttling is
// worse than waiting.
func withFloodRetry[T any](ctx context.Context, call func() (T, error)) (T, error) {
	var zero T
	for attempt := 0; ; attempt++ {
		resp, err := call()
		wait, flood := tgclient.AsFloodWait(err)
		if !flood {
			return resp, err
		}
		if attempt >= floodAttempts || wait > floodMaxWait {
			return zero, err
		}
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(wait + time.Second):
		}
	}
}

// dialogsAsPlaylists maps the dialogs of a getDialogs response to playlists.
// The dialogs list is the ordered set of chats the account has open; the users
// and chats vectors also carry entities that only the top messages reference,
// so walking them would list chats that do not exist.
func dialogsAsPlaylists(dialogs tg.MessagesDialogsClass) []playlist.PlaylistInfo {
	var (
		dialogList []tg.DialogClass
		users      []tg.UserClass
		chats      []tg.ChatClass
	)
	switch value := dialogs.(type) {
	case *tg.MessagesDialogs:
		dialogList, users, chats = value.Dialogs, value.Users, value.Chats
	case *tg.MessagesDialogsSlice:
		dialogList, users, chats = value.Dialogs, value.Users, value.Chats
	default:
		return nil
	}

	usersByID := make(map[int64]*tg.User, len(users))
	for _, entity := range users {
		if user, ok := entity.(*tg.User); ok {
			usersByID[user.ID] = user
		}
	}
	channelsByID := make(map[int64]*tg.Channel, len(chats))
	groupsByID := make(map[int64]*tg.Chat, len(chats))
	for _, entity := range chats {
		switch chat := entity.(type) {
		case *tg.Channel:
			channelsByID[chat.ID] = chat
		case *tg.Chat:
			groupsByID[chat.ID] = chat
		}
	}

	result := make([]playlist.PlaylistInfo, 0, len(dialogList))
	for _, entity := range dialogList {
		dialog, ok := entity.(*tg.Dialog)
		if !ok {
			continue
		}
		switch peer := dialog.Peer.(type) {
		case *tg.PeerUser:
			user, ok := usersByID[peer.UserID]
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
		case *tg.PeerChannel:
			channel, ok := channelsByID[peer.ChannelID]
			if !ok || channel.Title == "" {
				continue
			}
			result = append(result, playlist.PlaylistInfo{
				ID:      fmt.Sprintf("ch:%d:%d", channel.ID, channel.AccessHash),
				Name:    channel.Title,
				Section: "Channels",
			})
		case *tg.PeerChat:
			chat, ok := groupsByID[peer.ChatID]
			if !ok || chat.Title == "" {
				continue
			}
			result = append(result, playlist.PlaylistInfo{
				ID:      fmt.Sprintf("c:%d", chat.ID),
				Name:    chat.Title,
				Section: "Groups",
			})
		}
	}
	return result
}

// inputPeer resolves a peer into the input form, using the access hash of the
// entity that came with the dialogs page.
func inputPeer(peer tg.PeerClass, users []tg.UserClass, chats []tg.ChatClass) (tg.InputPeerClass, bool) {
	switch value := peer.(type) {
	case *tg.PeerUser:
		for _, entity := range users {
			if user, ok := entity.(*tg.User); ok && user.ID == value.UserID {
				return &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}, true
			}
		}
	case *tg.PeerChat:
		return &tg.InputPeerChat{ChatID: value.ChatID}, true
	case *tg.PeerChannel:
		for _, entity := range chats {
			if channel, ok := entity.(*tg.Channel); ok && channel.ID == value.ChannelID {
				return &tg.InputPeerChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash}, true
			}
		}
	}
	return nil, false
}

// topMessageDate returns the date of the top message of a dialog, which the
// next getDialogs page uses as its offset date. Message IDs are only unique
// inside a peer, so the peer has to match too: another dialog's message in the
// same page can carry the same ID.
func topMessageDate(messages []tg.MessageClass, peer tg.PeerClass, id int) int {
	for _, message := range messages {
		if message.GetID() != id {
			continue
		}
		owner, ok := messagePeer(message)
		if !ok || !samePeer(owner, peer) {
			continue
		}
		if date, ok := messageDate(message); ok {
			return date
		}
	}
	return 0
}

// messagePeer returns the peer a message belongs to.
func messagePeer(message tg.MessageClass) (tg.PeerClass, bool) {
	switch value := message.(type) {
	case *tg.Message:
		return value.PeerID, true
	case *tg.MessageService:
		return value.PeerID, true
	}
	return nil, false
}

// messageDate returns the date of a message. The top message of a dialog is a
// service message (a join, a pin) more often than not, and it carries a date
// too; reading only regular messages left the paging offset at zero and
// Telegram answered with the first page again.
func messageDate(message tg.MessageClass) (int, bool) {
	switch value := message.(type) {
	case *tg.Message:
		return value.Date, true
	case *tg.MessageService:
		return value.Date, true
	}
	return 0, false
}

// samePeer reports whether two peers point at the same chat.
func samePeer(a, b tg.PeerClass) bool {
	switch left := a.(type) {
	case *tg.PeerUser:
		right, ok := b.(*tg.PeerUser)
		return ok && left.UserID == right.UserID
	case *tg.PeerChat:
		right, ok := b.(*tg.PeerChat)
		return ok && left.ChatID == right.ChatID
	case *tg.PeerChannel:
		right, ok := b.(*tg.PeerChannel)
		return ok && left.ChannelID == right.ChannelID
	}
	return false
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

// usernameOf accepts "@name", "name" and a t.me link, and returns the
// username the resolve call takes.
func usernameOf(chat string) string {
	name := strings.TrimSpace(chat)
	name = strings.TrimPrefix(name, "https://t.me/")
	name = strings.TrimPrefix(name, "t.me/")
	return strings.TrimPrefix(name, "@")
}

// resolveChatCached resolves a configured chat once per process. Each lookup
// costs a request and Telegram rate-limits them, so a pane refresh must not
// pay for the same usernames again.
func (p *Provider) resolveChatCached(ctx context.Context, chat string) (playlist.PlaylistInfo, error) {
	key := strings.ToLower(usernameOf(chat))
	p.chatsMu.Lock()
	cached, ok := p.chatsCache[key]
	p.chatsMu.Unlock()
	if ok {
		return cached, nil
	}
	info, err := p.resolveChat(ctx, chat)
	if err != nil {
		return playlist.PlaylistInfo{}, err
	}
	p.chatsMu.Lock()
	if p.chatsCache == nil {
		p.chatsCache = make(map[string]playlist.PlaylistInfo)
	}
	p.chatsCache[key] = info
	p.chatsMu.Unlock()
	return info, nil
}

// resolveChat resolves one entry of [telegram] chats and describes it the way
// the dialogs list does: a stable id, the title and the section it belongs to.
// Username lookups are rate-limited like the dialog pages, so a short
// FLOOD_WAIT is waited out instead of failing the list.
func (p *Provider) resolveChat(ctx context.Context, chat string) (playlist.PlaylistInfo, error) {
	trimmed := usernameOf(chat)
	resolved, err := withFloodRetry(ctx, func() (*tg.ContactsResolvedPeer, error) {
		return p.api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: trimmed})
	})
	if err != nil {
		return playlist.PlaylistInfo{}, fmt.Errorf("telegram: cannot resolve %q: %w", trimmed, err)
	}
	switch peer := resolved.Peer.(type) {
	case *tg.PeerUser:
		for _, entity := range resolved.Users {
			user, ok := entity.(*tg.User)
			if !ok || user.ID != peer.UserID {
				continue
			}
			input := &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}
			name := strings.TrimSpace(user.FirstName + " " + user.LastName)
			if name == "" {
				name = user.Username
			}
			if name == "" {
				name = trimmed
			}
			section := "Chats"
			if user.Bot {
				section = "Bots"
			}
			return playlist.PlaylistInfo{ID: peerID(input), Name: name, Section: section}, nil
		}
	case *tg.PeerChat:
		input := &tg.InputPeerChat{ChatID: peer.ChatID}
		name := trimmed
		for _, entity := range resolved.Chats {
			if group, ok := entity.(*tg.Chat); ok && group.ID == peer.ChatID && group.Title != "" {
				name = group.Title
			}
		}
		return playlist.PlaylistInfo{ID: peerID(input), Name: name, Section: "Groups"}, nil
	case *tg.PeerChannel:
		for _, entity := range resolved.Chats {
			channel, ok := entity.(*tg.Channel)
			if !ok || channel.ID != peer.ChannelID {
				continue
			}
			input := &tg.InputPeerChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash}
			name := channel.Title
			if name == "" {
				name = trimmed
			}
			section := "Channels"
			if channel.Megagroup {
				section = "Groups"
			}
			return playlist.PlaylistInfo{ID: peerID(input), Name: name, Section: section}, nil
		}
	}
	return playlist.PlaylistInfo{}, fmt.Errorf("telegram: unsupported peer for %q", trimmed)
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
