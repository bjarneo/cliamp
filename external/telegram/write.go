package telegram

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/gotd/td/tg"
)

// writeAPI is the part of *tg.Client that the write side uses. Tests inject a
// fake so the provider never needs a real account.
type writeAPI interface {
	MessagesSendMessage(ctx context.Context, request *tg.MessagesSendMessageRequest) (tg.UpdatesClass, error)
	MessagesGetHistory(ctx context.Context, request *tg.MessagesGetHistoryRequest) (tg.MessagesMessagesClass, error)
	UsersGetFullUser(ctx context.Context, id tg.InputUserClass) (*tg.UsersUserFull, error)
	MessagesGetBotCallbackAnswer(ctx context.Context, request *tg.MessagesGetBotCallbackAnswerRequest) (*tg.MessagesBotCallbackAnswer, error)
	MessagesGetInlineBotResults(ctx context.Context, request *tg.MessagesGetInlineBotResultsRequest) (*tg.MessagesBotResults, error)
	MessagesSendInlineBotResult(ctx context.Context, request *tg.MessagesSendInlineBotResultRequest) (tg.UpdatesClass, error)
}

// resolveChatID accepts a peer id or a username and returns the input peer
// plus the canonical peer id. The canonical id goes into track paths, so a
// profile that names a bot by username still produces a playable URI.
func (p *Provider) resolveChatID(ctx context.Context, chat string) (tg.InputPeerClass, string, error) {
	if peer, err := parsePeerID(chat); err == nil {
		return peer, chat, nil
	}
	info, err := p.resolveChatCached(ctx, chat)
	if err != nil {
		return nil, "", err
	}
	peer, err := parsePeerID(info.ID)
	if err != nil {
		return nil, "", err
	}
	return peer, info.ID, nil
}

// writer returns the API the write side calls. Production leaves write nil and
// uses the connected client; tests set write to a fake.
func (p *Provider) writer() writeAPI {
	if p.write != nil {
		return p.write
	}
	return p.api
}

// BotCommands returns the commands the bot of a user chat advertises. Groups,
// channels and users that are not bots return none.
func (p *Provider) BotCommands(ctx context.Context, chatID string) ([]provider.BotCommand, error) {
	if err := p.wait(); err != nil {
		return nil, err
	}
	peer, _, err := p.resolveChatID(ctx, chatID)
	if err != nil {
		return nil, err
	}
	user, ok := peer.(*tg.InputPeerUser)
	if !ok {
		return nil, nil
	}
	full, err := p.writer().UsersGetFullUser(ctx, &tg.InputUser{
		UserID:     user.UserID,
		AccessHash: user.AccessHash,
	})
	if err != nil {
		return nil, fmt.Errorf("telegram: commands of %q: %w", chatID, err)
	}
	info, ok := full.FullUser.GetBotInfo()
	if !ok {
		return nil, nil
	}
	out := make([]provider.BotCommand, 0, len(info.Commands))
	for _, command := range info.Commands {
		name := strings.TrimSpace(command.Command)
		if name == "" {
			continue
		}
		if !strings.HasPrefix(name, "/") {
			name = "/" + name
		}
		out = append(out, provider.BotCommand{Command: name, Description: command.Description})
	}
	return out, nil
}

// SendCommand sends text to chatID as the signed-in user and returns the id
// of the message Telegram created.
func (p *Provider) SendCommand(ctx context.Context, chatID, text string) (int, error) {
	if err := p.wait(); err != nil {
		return 0, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, errors.New("telegram: empty message")
	}
	peer, _, err := p.resolveChatID(ctx, chatID)
	if err != nil {
		return 0, err
	}
	randomID, err := randomMessageID()
	if err != nil {
		return 0, err
	}
	updates, err := p.writer().MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
		Peer:      peer,
		Message:   text,
		RandomID:  randomID,
		NoWebpage: true,
	})
	if err != nil {
		return 0, fmt.Errorf("telegram: send to %q: %w", chatID, err)
	}
	id := sentMessageID(updates)
	if id == 0 {
		return 0, errors.New("telegram: send returned no message id")
	}
	return id, nil
}

// BotCallback presses an inline button on a bot message.
func (p *Provider) BotCallback(ctx context.Context, chatID string, messageID int, data []byte) error {
	if err := p.wait(); err != nil {
		return err
	}
	peer, _, err := p.resolveChatID(ctx, chatID)
	if err != nil {
		return err
	}
	_, err = p.writer().MessagesGetBotCallbackAnswer(ctx, &tg.MessagesGetBotCallbackAnswerRequest{
		Peer:  peer,
		MsgID: messageID,
		Data:  data,
	})
	if err != nil {
		// DeezLoad starts the download after the callback and can take longer
		// than Telegram's callback answer window. The button was pressed, so
		// the caller waits for the audio instead of reporting a failure.
		if strings.Contains(err.Error(), "BOT_RESPONSE_TIMEOUT") {
			return nil
		}
		return fmt.Errorf("telegram: callback on %q: %w", chatID, err)
	}
	return nil
}

// Message reads one message by id. It is used after a callback that edits a
// menu in place: the id stays the same, so WaitReply cannot see the change.
func (p *Provider) Message(ctx context.Context, chatID string, messageID int) (provider.ChatReply, error) {
	if err := p.wait(); err != nil {
		return provider.ChatReply{}, err
	}
	peer, canonicalID, err := p.resolveChatID(ctx, chatID)
	if err != nil {
		return provider.ChatReply{}, err
	}
	history, err := p.writer().MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer:  peer,
		MinID: messageID - 1,
		MaxID: messageID + 1,
		Limit: 1,
	})
	if err != nil {
		return provider.ChatReply{}, fmt.Errorf("telegram: message %d of %q: %w", messageID, chatID, err)
	}
	for _, entity := range messagesOf(history) {
		msg, ok := entity.(*tg.Message)
		if !ok || msg.ID != messageID {
			continue
		}
		if reply, ok := replyFromMessage(canonicalID, msg); ok {
			return reply, nil
		}
	}
	return provider.ChatReply{Kind: "none"}, nil
}

// replyFromMessage turns one message into a ChatReply.
func replyFromMessage(chatID string, msg *tg.Message) (provider.ChatReply, bool) {
	if media, ok := msg.Media.(*tg.MessageMediaDocument); ok {
		if doc, ok := media.Document.(*tg.Document); ok {
			if info, ok := audioInfo(doc); ok {
				track := playlist.Track{
					Path:         fmt.Sprintf("%s%s:%d", uriPrefix, chatID, msg.ID),
					Title:        info.title,
					Artist:       info.performer,
					DurationSecs: info.duration,
					ProviderMeta: map[string]string{"telegram.id": fmt.Sprint(msg.ID)},
				}
				return provider.ChatReply{
					Kind:      "audio",
					Track:     track,
					From:      senderOf(msg),
					MessageID: msg.ID,
				}, true
			}
		}
	}
	buttons := buttonsOf(msg)
	if text := strings.TrimSpace(msg.Message); text != "" || len(buttons) > 0 {
		return provider.ChatReply{
			Kind:      "text",
			Text:      text,
			From:      senderOf(msg),
			MessageID: msg.ID,
			Buttons:   buttons,
			Links:     linksOf(msg),
		}, true
	}
	return provider.ChatReply{}, false
}

// WaitReply polls chatID until an audio message newer than afterID arrives or a
// text reply sits unanswered for replyGrace. It returns Kind "none" when the
// timeout ends with nothing new. The caller owns the timeout.
func (p *Provider) WaitReply(ctx context.Context, chatID string, afterID int, timeout time.Duration) (provider.ChatReply, error) {
	return p.waitReply(ctx, chatID, afterID, timeout, false)
}

// WaitAudio is WaitReply for a bot that first sends text and then downloads
// the audio. It ignores text and keyboards until an audio arrives or the
// timeout ends.
func (p *Provider) WaitAudio(ctx context.Context, chatID string, afterID int, timeout time.Duration) (provider.ChatReply, error) {
	return p.waitReply(ctx, chatID, afterID, timeout, true)
}

func (p *Provider) waitReply(ctx context.Context, chatID string, afterID int, timeout time.Duration, audioOnly bool) (provider.ChatReply, error) {
	if err := p.wait(); err != nil {
		return provider.ChatReply{}, err
	}
	peer, canonicalID, err := p.resolveChatID(ctx, chatID)
	if err != nil {
		return provider.ChatReply{}, err
	}
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	deadline := time.Now().Add(timeout)
	const replyGrace = 3 * time.Second
	var (
		pending    provider.ChatReply
		hasPending bool
		textUntil  time.Time
	)
	for {
		history, err := p.writer().MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:  peer,
			MinID: afterID,
			Limit: 20,
		})
		if err != nil {
			return provider.ChatReply{}, fmt.Errorf("telegram: reply of %q: %w", chatID, err)
		}
		if audio, ok := audioReply(canonicalID, afterID, messagesOf(history)); ok {
			return audio, nil
		}
		if !audioOnly {
			if keyboard, ok := keyboardReply(afterID, messagesOf(history)); ok {
				return keyboard, nil
			}
			if !hasPending {
				if value, ok := textReply(afterID, messagesOf(history)); ok {
					pending, hasPending = value, true
					textUntil = time.Now().Add(replyGrace)
				}
			}
		} else if !hasPending {
			// Keep the newest text as a fallback when the timeout ends, but
			// never return it while the bot may still send the audio.
			if value, ok := textReply(afterID, messagesOf(history)); ok {
				pending, hasPending = value, true
			}
		}
		now := time.Now()
		if !audioOnly && hasPending && now.After(textUntil) {
			return pending, nil
		}
		if now.After(deadline) {
			if hasPending {
				return pending, nil
			}
			return provider.ChatReply{Kind: "none"}, nil
		}
		wait := time.Until(deadline)
		if !audioOnly && hasPending {
			wait = min(wait, time.Until(textUntil))
		}
		wait = min(wait, time.Second)
		if wait < 50*time.Millisecond {
			wait = 50 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return provider.ChatReply{}, ctx.Err()
		case <-time.After(wait):
		}
	}
}

// audioReply returns the newest audio message newer than afterID. Telegram
// returns history newest first, so the first match is the newest.
func audioReply(chatID string, afterID int, messages []tg.MessageClass) (provider.ChatReply, bool) {
	for _, entity := range messages {
		msg, ok := entity.(*tg.Message)
		if !ok || msg.ID <= afterID || msg.Out {
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
			Path:         fmt.Sprintf("%s%s:%d", uriPrefix, chatID, msg.ID),
			Title:        info.title,
			Artist:       info.performer,
			DurationSecs: info.duration,
			ProviderMeta: map[string]string{"telegram.id": fmt.Sprint(msg.ID)},
		}
		return provider.ChatReply{Kind: "audio", Track: track, From: senderOf(msg), MessageID: msg.ID}, true
	}
	return provider.ChatReply{}, false
}

// keyboardReply returns the newest message with an inline or reply keyboard,
// even when it carries no text. Bots often send the options as a keyboard on
// a separate message.
func keyboardReply(afterID int, messages []tg.MessageClass) (provider.ChatReply, bool) {
	for _, entity := range messages {
		msg, ok := entity.(*tg.Message)
		if !ok || msg.ID <= afterID || msg.Out {
			continue
		}
		buttons := buttonsOf(msg)
		if len(buttons) == 0 {
			continue
		}
		return provider.ChatReply{
			Kind:      "text",
			Text:      strings.TrimSpace(msg.Message),
			From:      senderOf(msg),
			MessageID: msg.ID,
			Buttons:   buttons,
			Links:     linksOf(msg),
		}, true
	}
	return provider.ChatReply{}, false
}

// textReply returns the newest text message newer than afterID, with the
// inline buttons attached to it.
func textReply(afterID int, messages []tg.MessageClass) (provider.ChatReply, bool) {
	for _, entity := range messages {
		msg, ok := entity.(*tg.Message)
		if !ok || msg.ID <= afterID || msg.Out {
			continue
		}
		if text := strings.TrimSpace(msg.Message); text != "" {
			return provider.ChatReply{
				Kind:      "text",
				Text:      text,
				From:      senderOf(msg),
				MessageID: msg.ID,
				Buttons:   buttonsOf(msg),
				Links:     linksOf(msg),
			}, true
		}
	}
	return provider.ChatReply{}, false
}

// linksOf returns the URLs the message text points to, both explicit text
// links and bare URLs.
func linksOf(msg *tg.Message) []string {
	if len(msg.Entities) == 0 {
		return nil
	}
	runes := []rune(msg.Message)
	var out []string
	for _, entity := range msg.Entities {
		switch value := entity.(type) {
		case *tg.MessageEntityTextURL:
			if value.URL != "" {
				out = append(out, value.URL)
			}
		case *tg.MessageEntityURL:
			start, end := value.Offset, value.Offset+value.Length
			if start >= 0 && end <= len(runes) && start < end {
				out = append(out, string(runes[start:end]))
			}
		}
	}
	return out
}

// buttonsOf reads the inline keyboard of a message. Link buttons carry a URL;
// callback buttons carry the data the bot expects back.
func buttonsOf(msg *tg.Message) []provider.ChatButton {
	switch markup := msg.ReplyMarkup.(type) {
	case *tg.ReplyInlineMarkup:
		var out []provider.ChatButton
		for _, row := range markup.Rows {
			for _, button := range row.Buttons {
				item := provider.ChatButton{Text: button.Text}
				switch kind := button.Type.(type) {
				case *tg.InlineButtonTypeCallback:
					item.Kind = "callback"
					item.Data = kind.Data
					if utf8.Valid(kind.Data) {
						item.DataText = string(kind.Data)
					}
				case *tg.InlineButtonTypeURL:
					item.Kind = "url"
					item.URL = kind.URL
				case *tg.InlineButtonTypeWebView:
					item.Kind = "url"
					item.URL = kind.URL
				case *tg.InlineButtonTypeDisabled:
					item.Kind = "disabled"
				case *tg.InlineButtonTypeSwitchInline:
					item.Kind = "switch_inline"
					item.Query = kind.Query
				case *tg.InlineButtonTypeCopy:
					item.Kind = "copy"
				default:
					item.Kind = strings.TrimPrefix(fmt.Sprintf("%T", button.Type), "*tg.")
				}
				out = append(out, item)
			}
		}
		return out
	case *tg.ReplyKeyboardMarkup:
		var out []provider.ChatButton
		for _, row := range markup.Rows {
			for _, button := range row.Buttons {
				out = append(out, provider.ChatButton{Text: button.Text, Kind: "text", SendText: true})
			}
		}
		return out
	default:
		return nil
	}
}

// senderOf describes who sent a message, when Telegram reports it.
func senderOf(msg *tg.Message) string {
	switch peer := msg.FromID.(type) {
	case *tg.PeerUser:
		return fmt.Sprintf("u:%d", peer.UserID)
	case *tg.PeerChat:
		return fmt.Sprintf("c:%d", peer.ChatID)
	case *tg.PeerChannel:
		return fmt.Sprintf("ch:%d", peer.ChannelID)
	default:
		return ""
	}
}

// sentMessageID walks the updates Telegram returns for a send. The id may come
// as updateMessageID, updateNewMessage, updateNewChannelMessage or, for a
// short update, on the update itself.
func sentMessageID(updates tg.UpdatesClass) int {
	switch value := updates.(type) {
	case *tg.Updates:
		return idInUpdates(value.Updates)
	case *tg.UpdatesCombined:
		return idInUpdates(value.Updates)
	case *tg.UpdateShortSentMessage:
		return value.ID
	default:
		return 0
	}
}

func idInUpdates(updates []tg.UpdateClass) int {
	for _, entity := range updates {
		switch value := entity.(type) {
		case *tg.UpdateMessageID:
			return value.ID
		case *tg.UpdateNewMessage:
			return value.Message.GetID()
		case *tg.UpdateNewChannelMessage:
			return value.Message.GetID()
		}
	}
	return 0
}

func randomMessageID() (int64, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return 0, fmt.Errorf("telegram: random id: %w", err)
	}
	id := int64(binary.LittleEndian.Uint64(buf[:]) & (1<<63 - 1))
	if id == 0 {
		id = 1
	}
	return id, nil
}

// ResolveChat resolves a username or t.me link into the playlist description
// the dialog list uses. It also creates the peer without sending a message.
func (p *Provider) ResolveChat(ctx context.Context, username string) (playlist.PlaylistInfo, error) {
	if err := p.wait(); err != nil {
		return playlist.PlaylistInfo{}, err
	}
	return p.resolveChatCached(ctx, username)
}

// InlineResults asks a bot for the inline results of query. This is the flow
// behind a switch_inline button: the user picks one result, and
// SendInlineResult sends it as a normal message.
func (p *Provider) InlineResults(ctx context.Context, chatID, botID, query, offset string) (provider.InlineResults, error) {
	if err := p.wait(); err != nil {
		return provider.InlineResults{}, err
	}
	peer, _, err := p.resolveChatID(ctx, chatID)
	if err != nil {
		return provider.InlineResults{}, err
	}
	botPeer, _, err := p.resolveChatID(ctx, botID)
	if err != nil {
		return provider.InlineResults{}, err
	}
	botUser, ok := botPeer.(*tg.InputPeerUser)
	if !ok {
		return provider.InlineResults{}, fmt.Errorf("telegram: %q is not a bot", botID)
	}
	response, err := p.writer().MessagesGetInlineBotResults(ctx, &tg.MessagesGetInlineBotResultsRequest{
		Bot: &tg.InputUser{
			UserID:     botUser.UserID,
			AccessHash: botUser.AccessHash,
		},
		Peer:   peer,
		Query:  query,
		Offset: offset,
	})
	if err != nil {
		return provider.InlineResults{}, fmt.Errorf("telegram: inline results of %q: %w", botID, err)
	}
	out := provider.InlineResults{
		QueryID:    response.QueryID,
		NextOffset: response.NextOffset,
	}
	for _, entity := range response.Results {
		switch result := entity.(type) {
		case *tg.BotInlineResult:
			out.Results = append(out.Results, provider.InlineResult{
				ID:          result.ID,
				Type:        result.Type,
				Kind:        inlineMessageKind(result.SendMessage),
				Title:       result.Title,
				Description: result.Description,
				URL:         result.URL,
				ThumbURL:    webDocumentURL(result.Thumb),
			})
		case *tg.BotInlineMediaResult:
			kind := inlineMessageKind(result.SendMessage)
			if doc, ok := result.Document.(*tg.Document); ok {
				if _, audio := audioInfo(doc); audio {
					kind = "audio"
				}
			}
			out.Results = append(out.Results, provider.InlineResult{
				ID:          result.ID,
				Type:        result.Type,
				Kind:        kind,
				Title:       result.Title,
				Description: result.Description,
			})
		}
	}
	return out, nil
}

// webDocumentURL returns the public URL of an inline result thumbnail.
func webDocumentURL(document tg.WebDocumentClass) string {
	switch value := document.(type) {
	case *tg.WebDocument:
		return value.URL
	case *tg.WebDocumentNoProxy:
		return value.URL
	default:
		return ""
	}
}

// inlineMessageKind says what sending the result creates. A media auto
// message is usually an audio or a document; the plugin can prefer it over an
// article.
func inlineMessageKind(message tg.BotInlineMessageClass) string {
	switch value := message.(type) {
	case *tg.BotInlineMessageMediaAuto:
		if value.Message != "" {
			return "media"
		}
		return "media"
	case *tg.BotInlineMessageText:
		return "text"
	case *tg.BotInlineMessageMediaContact:
		return "contact"
	case *tg.BotInlineMessageMediaGeo, *tg.BotInlineMessageMediaVenue:
		return "geo"
	case *tg.BotInlineMessageMediaWebPage:
		return "webpage"
	default:
		return "text"
	}
}

// SendInlineResult sends one result of an inline query to the chat.
func (p *Provider) SendInlineResult(ctx context.Context, chatID, botID string, queryID int64, resultID string) (int, error) {
	if err := p.wait(); err != nil {
		return 0, err
	}
	peer, _, err := p.resolveChatID(ctx, chatID)
	if err != nil {
		return 0, err
	}
	// Resolve the bot so a bad id fails before Telegram creates the message.
	if _, _, err := p.resolveChatID(ctx, botID); err != nil {
		return 0, err
	}
	randomID, err := randomMessageID()
	if err != nil {
		return 0, err
	}
	updates, err := p.writer().MessagesSendInlineBotResult(ctx, &tg.MessagesSendInlineBotResultRequest{
		Peer:     peer,
		QueryID:  queryID,
		ID:       resultID,
		RandomID: randomID,
	})
	if err != nil {
		return 0, fmt.Errorf("telegram: send inline result: %w", err)
	}
	id := sentMessageID(updates)
	if id == 0 {
		return 0, errors.New("telegram: send inline result returned no message id")
	}
	return id, nil
}
