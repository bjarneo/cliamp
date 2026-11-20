package telegram

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gotd/td/tg"
)

type fakeWriteAPI struct {
	sent     *tg.MessagesSendMessageRequest
	sendResp tg.UpdatesClass
	sendErr  error

	history   []tg.MessagesMessagesClass
	histErr   error
	histReqs  []*tg.MessagesGetHistoryRequest
	histTimes []time.Time

	full    *tg.UsersUserFull
	fullErr error
	fullID  tg.InputUserClass

	callback    *tg.MessagesGetBotCallbackAnswerRequest
	callbackErr error

	inline         *tg.MessagesBotResults
	inlineErr      error
	inlineReq      *tg.MessagesGetInlineBotResultsRequest
	sendInline     *tg.MessagesSendInlineBotResultRequest
	sendInlineResp tg.UpdatesClass
	sendInlineErr  error
}

func (f *fakeWriteAPI) MessagesSendMessage(_ context.Context, request *tg.MessagesSendMessageRequest) (tg.UpdatesClass, error) {
	f.sent = request
	return f.sendResp, f.sendErr
}

func (f *fakeWriteAPI) MessagesGetHistory(_ context.Context, request *tg.MessagesGetHistoryRequest) (tg.MessagesMessagesClass, error) {
	f.histReqs = append(f.histReqs, request)
	f.histTimes = append(f.histTimes, time.Now())
	if f.histErr != nil {
		return nil, f.histErr
	}
	if len(f.history) == 0 {
		return &tg.MessagesMessages{}, nil
	}
	next := f.history[0]
	if len(f.history) > 1 {
		f.history = f.history[1:]
	}
	return next, nil
}

func (f *fakeWriteAPI) UsersGetFullUser(_ context.Context, id tg.InputUserClass) (*tg.UsersUserFull, error) {
	f.fullID = id
	return f.full, f.fullErr
}

func (f *fakeWriteAPI) MessagesGetBotCallbackAnswer(_ context.Context, request *tg.MessagesGetBotCallbackAnswerRequest) (*tg.MessagesBotCallbackAnswer, error) {
	f.callback = request
	return &tg.MessagesBotCallbackAnswer{}, f.callbackErr
}

func (f *fakeWriteAPI) MessagesGetInlineBotResults(_ context.Context, request *tg.MessagesGetInlineBotResultsRequest) (*tg.MessagesBotResults, error) {
	f.inlineReq = request
	return f.inline, f.inlineErr
}

func (f *fakeWriteAPI) MessagesSendInlineBotResult(_ context.Context, request *tg.MessagesSendInlineBotResultRequest) (tg.UpdatesClass, error) {
	f.sendInline = request
	return f.sendInlineResp, f.sendInlineErr
}

func readyProvider(api writeAPI) *Provider {
	p := &Provider{write: api}
	p.state = provReady
	return p
}

func TestSendCommand(t *testing.T) {
	api := &fakeWriteAPI{sendResp: &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateMessageID{ID: 42}}}}
	p := readyProvider(api)

	id, err := p.SendCommand(context.Background(), "u:100:200", "  /start  ")
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}
	if id != 42 {
		t.Fatalf("id = %d, want 42", id)
	}
	if api.sent == nil {
		t.Fatal("no message sent")
	}
	if api.sent.Message != "/start" {
		t.Errorf("message = %q, want %q", api.sent.Message, "/start")
	}
	if api.sent.RandomID == 0 {
		t.Error("RandomID = 0")
	}
	peer, ok := api.sent.Peer.(*tg.InputPeerUser)
	if !ok || peer.UserID != 100 || peer.AccessHash != 200 {
		t.Errorf("peer = %#v, want user 100/200", api.sent.Peer)
	}
}

func TestSendCommandShortUpdate(t *testing.T) {
	api := &fakeWriteAPI{sendResp: &tg.UpdateShortSentMessage{ID: 7}}
	p := readyProvider(api)

	id, err := p.SendCommand(context.Background(), "ch:1:2", "/play")
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}
	if id != 7 {
		t.Fatalf("id = %d, want 7", id)
	}
}

func TestSendCommandRejectsEmpty(t *testing.T) {
	p := readyProvider(&fakeWriteAPI{})
	if _, err := p.SendCommand(context.Background(), "u:1:2", "   "); err == nil {
		t.Fatal("expected an error for an empty message")
	}
}

func TestSendCommandPropagatesError(t *testing.T) {
	api := &fakeWriteAPI{sendErr: errors.New("FLOOD_WAIT_30")}
	p := readyProvider(api)
	if _, err := p.SendCommand(context.Background(), "u:1:2", "/start"); err == nil {
		t.Fatal("expected the send error")
	}
}

func TestBotCommands(t *testing.T) {
	full := &tg.UserFull{}
	full.SetBotInfo(tg.BotInfo{Commands: []tg.BotCommand{
		{Command: "search", Description: "Find a track"},
		{Command: "/start", Description: "Start"},
		{Command: "", Description: "ignored"},
	}})
	api := &fakeWriteAPI{full: &tg.UsersUserFull{FullUser: *full}}
	p := readyProvider(api)

	commands, err := p.BotCommands(context.Background(), "u:100:200")
	if err != nil {
		t.Fatalf("BotCommands: %v", err)
	}
	if len(commands) != 2 {
		t.Fatalf("commands = %#v, want 2", commands)
	}
	if commands[0].Command != "/search" || commands[0].Description != "Find a track" {
		t.Errorf("command 0 = %#v", commands[0])
	}
	if commands[1].Command != "/start" {
		t.Errorf("command 1 = %#v", commands[1])
	}
	user, ok := api.fullID.(*tg.InputUser)
	if !ok || user.UserID != 100 || user.AccessHash != 200 {
		t.Errorf("full user id = %#v", api.fullID)
	}
}

func TestBotCommandsGroupIsEmpty(t *testing.T) {
	p := readyProvider(&fakeWriteAPI{})
	commands, err := p.BotCommands(context.Background(), "c:123")
	if err != nil {
		t.Fatalf("BotCommands: %v", err)
	}
	if len(commands) != 0 {
		t.Fatalf("commands = %#v, want none", commands)
	}
}

func TestWaitReplyAudio(t *testing.T) {
	msg := &tg.Message{
		ID: 10,
		Media: &tg.MessageMediaDocument{Document: &tg.Document{
			MimeType: "audio/flac",
			Attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeAudio{Title: "Walk", Performer: "Foo Fighters", Duration: 260},
			},
		}},
	}
	api := &fakeWriteAPI{history: []tg.MessagesMessagesClass{
		&tg.MessagesMessages{Messages: []tg.MessageClass{msg}},
	}}
	p := readyProvider(api)

	reply, err := p.WaitReply(context.Background(), "u:1:2", 5, time.Second)
	if err != nil {
		t.Fatalf("WaitReply: %v", err)
	}
	if reply.Kind != "audio" {
		t.Fatalf("kind = %q, want audio", reply.Kind)
	}
	if reply.Track.Title != "Walk" || reply.Track.Artist != "Foo Fighters" {
		t.Errorf("track = %#v", reply.Track)
	}
	if reply.Track.Path != "telegram:u:1:2:10" {
		t.Errorf("path = %q", reply.Track.Path)
	}
	if len(api.histReqs) != 1 || api.histReqs[0].MinID != 5 {
		t.Errorf("history requests = %#v", api.histReqs)
	}
}

func TestWaitReplyText(t *testing.T) {
	msg := &tg.Message{ID: 8, Message: "Track not found"}
	api := &fakeWriteAPI{history: []tg.MessagesMessagesClass{
		&tg.MessagesMessages{Messages: []tg.MessageClass{msg}},
	}}
	p := readyProvider(api)

	reply, err := p.WaitReply(context.Background(), "u:1:2", 5, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitReply: %v", err)
	}
	if reply.Kind != "text" || reply.Text != "Track not found" {
		t.Fatalf("reply = %#v", reply)
	}
}

func TestWaitReplyNone(t *testing.T) {
	p := readyProvider(&fakeWriteAPI{})
	reply, err := p.WaitReply(context.Background(), "u:1:2", 5, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitReply: %v", err)
	}
	if reply.Kind != "none" {
		t.Fatalf("reply = %#v, want none", reply)
	}
}

func TestWaitReplyIgnoresOldAndOutgoing(t *testing.T) {
	api := &fakeWriteAPI{history: []tg.MessagesMessagesClass{
		&tg.MessagesMessages{Messages: []tg.MessageClass{
			&tg.Message{ID: 4, Message: "old"},
			&tg.Message{ID: 6, Out: true, Message: "mine"},
		}},
	}}
	p := readyProvider(api)
	reply, err := p.WaitReply(context.Background(), "u:1:2", 5, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitReply: %v", err)
	}
	if reply.Kind != "none" {
		t.Fatalf("reply = %#v, want none", reply)
	}
}

func TestBotCallback(t *testing.T) {
	api := &fakeWriteAPI{}
	p := readyProvider(api)
	if err := p.BotCallback(context.Background(), "u:1:2", 77, []byte("data")); err != nil {
		t.Fatalf("BotCallback: %v", err)
	}
	if api.callback == nil || api.callback.MsgID != 77 || string(api.callback.Data) != "data" {
		t.Fatalf("callback = %#v", api.callback)
	}
}

func TestInlineResults(t *testing.T) {
	api := &fakeWriteAPI{inline: &tg.MessagesBotResults{
		QueryID: 9,
		Results: []tg.BotInlineResultClass{
			&tg.BotInlineResult{ID: "1", Type: "audio", Title: "Get Lucky", Description: "Daft Punk"},
		},
	}}
	p := readyProvider(api)
	results, err := p.InlineResults(context.Background(), "u:1:2", "u:1:2", "get lucky", "")
	if err != nil {
		t.Fatalf("InlineResults: %v", err)
	}
	if results.QueryID != 9 || len(results.Results) != 1 {
		t.Fatalf("results = %#v", results)
	}
	if results.Results[0].Title != "Get Lucky" || results.Results[0].Description != "Daft Punk" {
		t.Errorf("result = %#v", results.Results[0])
	}
	if api.inlineReq == nil || api.inlineReq.Query != "get lucky" {
		t.Errorf("request = %#v", api.inlineReq)
	}
}

func TestSendInlineResult(t *testing.T) {
	api := &fakeWriteAPI{sendInlineResp: &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateMessageID{ID: 88}}}}
	p := readyProvider(api)
	id, err := p.SendInlineResult(context.Background(), "u:1:2", "u:1:2", 9, "1")
	if err != nil {
		t.Fatalf("SendInlineResult: %v", err)
	}
	if id != 88 {
		t.Fatalf("id = %d", id)
	}
	if api.sendInline == nil || api.sendInline.QueryID != 9 || api.sendInline.ID != "1" {
		t.Errorf("request = %#v", api.sendInline)
	}
}

func TestSentMessageIDFromNewMessage(t *testing.T) {
	updates := &tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateNewMessage{Message: &tg.Message{ID: 33}},
	}}
	if got := sentMessageID(updates); got != 33 {
		t.Fatalf("sentMessageID = %d, want 33", got)
	}
}

func TestMessageReadsEditedMenu(t *testing.T) {
	msg := &tg.Message{
		ID:      7,
		Message: "Settings",
		ReplyMarkup: &tg.ReplyInlineMarkup{Rows: []tg.KeyboardInlineButtonRow{{
			Buttons: []tg.KeyboardInlineButton{{
				Text: "FLAC",
				Type: &tg.InlineButtonTypeCallback{Data: []byte("flac")},
			}},
		}}},
	}
	api := &fakeWriteAPI{history: []tg.MessagesMessagesClass{
		&tg.MessagesMessages{Messages: []tg.MessageClass{msg}},
	}}
	p := readyProvider(api)

	reply, err := p.Message(context.Background(), "u:1:2", 7)
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if reply.Kind != "text" || reply.Text != "Settings" {
		t.Fatalf("reply = %#v", reply)
	}
	if len(reply.Buttons) != 1 || reply.Buttons[0].Text != "FLAC" || string(reply.Buttons[0].Data) != "flac" {
		t.Fatalf("buttons = %#v", reply.Buttons)
	}
}

func TestLinksOfUsesUTF16Offsets(t *testing.T) {
	msg := &tg.Message{
		Message: "😀 https://example.com",
		Entities: []tg.MessageEntityClass{
			&tg.MessageEntityURL{Offset: 3, Length: 19},
		},
	}
	links := linksOf(msg)
	if len(links) != 1 || links[0] != "https://example.com" {
		t.Fatalf("links = %#v, want the URL after the emoji", links)
	}
}

func TestAudioReplyKeepsButtonsAndCaption(t *testing.T) {
	msg := &tg.Message{
		ID:      9,
		Message: "Here is the track",
		Media: &tg.MessageMediaDocument{Document: &tg.Document{
			MimeType: "audio/flac",
			Attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeAudio{Title: "Walk", Performer: "Foo Fighters", Duration: 260},
			},
		}},
		ReplyMarkup: &tg.ReplyInlineMarkup{Rows: []tg.KeyboardInlineButtonRow{{
			Buttons: []tg.KeyboardInlineButton{{
				Text: "FLAC",
				Type: &tg.InlineButtonTypeCallback{Data: []byte("flac")},
			}},
		}}},
	}
	reply, ok := replyFromMessage("u:1:2", msg)
	if !ok {
		t.Fatal("replyFromMessage returned false")
	}
	if reply.Kind != "audio" || reply.Text != "Here is the track" {
		t.Fatalf("reply = %#v", reply)
	}
	if len(reply.Buttons) != 1 || reply.Buttons[0].Text != "FLAC" {
		t.Fatalf("buttons = %#v", reply.Buttons)
	}
}

func TestWaitContextHonorsCancellation(t *testing.T) {
	p := &Provider{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.waitContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitContext = %v, want context.Canceled", err)
	}
}

func TestWaitAudioKeepsTheLatestText(t *testing.T) {
	first := &tg.Message{ID: 6, Message: "Searching"}
	second := &tg.Message{ID: 7, Message: "Track not found"}
	api := &fakeWriteAPI{history: []tg.MessagesMessagesClass{
		&tg.MessagesMessages{Messages: []tg.MessageClass{first}},
		&tg.MessagesMessages{Messages: []tg.MessageClass{second}},
	}}
	p := readyProvider(api)

	reply, err := p.WaitAudio(context.Background(), "u:1:2", 5, 1300*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitAudio: %v", err)
	}
	if reply.Kind != "text" || reply.Text != "Track not found" {
		t.Fatalf("reply = %#v, want the latest text", reply)
	}
}

func TestMessageWindowStartsAtTheRequestedID(t *testing.T) {
	msg := &tg.Message{ID: 7, Message: "Settings"}
	api := &fakeWriteAPI{history: []tg.MessagesMessagesClass{
		&tg.MessagesMessages{Messages: []tg.MessageClass{msg}},
	}}
	p := readyProvider(api)

	if _, err := p.Message(context.Background(), "u:1:2", 7); err != nil {
		t.Fatalf("Message: %v", err)
	}
	if len(api.histReqs) != 1 {
		t.Fatalf("history requests = %d, want 1", len(api.histReqs))
	}
	if got := api.histReqs[0].OffsetID; got != 8 {
		t.Fatalf("OffsetID = %d, want 8", got)
	}
}

func TestWaitReplyRestartsGraceForANewMessageWithTheSameText(t *testing.T) {
	oldGrace := replyGrace
	replyGrace = 50 * time.Millisecond
	t.Cleanup(func() { replyGrace = oldGrace })

	first := &tg.Message{ID: 6, Message: "Searching"}
	second := &tg.Message{ID: 7, Message: "Searching"}
	api := &fakeWriteAPI{history: []tg.MessagesMessagesClass{
		&tg.MessagesMessages{Messages: []tg.MessageClass{first}},
		&tg.MessagesMessages{Messages: []tg.MessageClass{second}},
	}}
	p := readyProvider(api)

	reply, err := p.WaitReply(context.Background(), "u:1:2", 5, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitReply: %v", err)
	}
	if reply.MessageID != 7 {
		t.Fatalf("message id = %d, want the newer message", reply.MessageID)
	}
	if len(api.histTimes) < 2 {
		t.Fatalf("history calls = %d, want at least 2", len(api.histTimes))
	}
	if since := time.Since(api.histTimes[1]); since < 40*time.Millisecond {
		t.Fatalf("returned %v after the newer message, want a restarted grace period", since)
	}
}
