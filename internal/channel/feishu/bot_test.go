package feishu

import (
	"context"
	"errors"
	"io"
	"log"
	"strconv"
	"sync"
	"testing"

	foyachannel "github.com/freesoulcode/foya/internal/channel"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	interaction "github.com/freesoulcode/foya/internal/interaction"
	"github.com/freesoulcode/foya/internal/testkit"

	"github.com/larksuite/oapi-sdk-go/v3/channel/types"
)

func TestProcessMessageReturnsCompleteResponseAndReusesSession(t *testing.T) {
	runtime := newFakeBackend()
	transport := &fakeChannel{}
	bot := newTestBot(t, runtime, transport)
	bindChatToNewSession(t, bot, runtime, "oc_1")
	incoming := &types.NormalizedMessage{
		MessageID: "om_1",
		ChatID:    "oc_1",
		ChatType:  "group",
		UserID:    "ou_1",
		Content:   "@_user_1 hello",
		Mentions: []types.Mention{{
			Key: "@_user_1", IsBot: true,
		}},
	}

	if err := bot.processMessage(context.Background(), incoming); err != nil {
		t.Fatal(err)
	}
	if err := bot.processMessage(context.Background(), incoming); err != nil {
		t.Fatal(err)
	}

	if runtime.createCount != 1 {
		t.Fatalf("created %d sessions, want 1", runtime.createCount)
	}
	if len(runtime.inputs) != 2 || runtime.inputs[0].Text != "hello" {
		t.Fatalf("submitted inputs = %#v", runtime.inputs)
	}
	if len(transport.sent) != 2 {
		t.Fatalf("sent %d responses, want 2", len(transport.sent))
	}
	for _, sent := range transport.sent {
		if sent.Markdown != "hello" {
			t.Fatalf("response markdown = %q, want hello", sent.Markdown)
		}
	}
}

func TestProcessMessageImportsImage(t *testing.T) {
	runtime := newFakeBackend()
	transport := &fakeChannel{downloads: map[string][]byte{"img_1": []byte("image")}}
	bot := newTestBot(t, runtime, transport)
	bindChatToNewSession(t, bot, runtime, "oc_image")
	incoming := &types.NormalizedMessage{
		MessageID: "om_image",
		ChatID:    "oc_image",
		ChatType:  "p2p",
		UserID:    "ou_allowed",
		Content:   "describe\n![image](img_1)",
		Resources: []types.Resource{{Type: "image", FileKey: "img_1"}},
	}

	if err := bot.processMessage(context.Background(), incoming); err != nil {
		t.Fatal(err)
	}

	if len(runtime.inputs) != 1 {
		t.Fatalf("submitted inputs = %#v", runtime.inputs)
	}
	input := runtime.inputs[0]
	if input.Text != "describe" {
		t.Fatalf("input text = %q", input.Text)
	}
	if len(input.Attachments) != 1 || input.Attachments[0].ID != "artifact-1" {
		t.Fatalf("attachments = %#v", input.Attachments)
	}
	if got := string(runtime.images[0]); got != "image" {
		t.Fatalf("uploaded image = %q", got)
	}
}

func TestNewCommandReplacesChatSession(t *testing.T) {
	runtime := newFakeBackend()
	transport := &fakeChannel{}
	bot := newTestBot(t, runtime, transport)
	incoming := &types.NormalizedMessage{
		MessageID: "om_new",
		ChatID:    "oc_1",
		ChatType:  "p2p",
		UserID:    "ou_allowed",
		Content:   "/new",
	}
	if err := bot.processMessage(context.Background(), incoming); err != nil {
		t.Fatal(err)
	}
	first, err := bot.store.Get(
		context.Background(),
		bot.config.ChannelID,
		conversationKey("oc_1"),
	)
	if err != nil {
		t.Fatal(err)
	}

	incoming.MessageID = "om_new_2"
	if err := bot.processMessage(context.Background(), incoming); err != nil {
		t.Fatal(err)
	}

	second, err := bot.store.Get(context.Background(), bot.config.ChannelID, conversationKey("oc_1"))
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("session was not replaced: %q", first)
	}
	if oldBinding, err := bot.store.ForSession(
		context.Background(),
		first,
	); err != nil || oldBinding.ActiveSessionID != "" {
		t.Fatalf("old session binding = %#v, err = %v", oldBinding, err)
	}
	if runtime.createCount != 2 {
		t.Fatalf("created %d sessions, want 2", runtime.createCount)
	}
	if len(transport.sent) != 2 ||
		transport.sent[1].Text != localizedMessage(bot.config.Locale, "new_chat") {
		t.Fatalf("sent messages = %#v", transport.sent)
	}
}

func TestOneBotStartsIndependentSessionsFromPrivateAndThreeGroupChats(t *testing.T) {
	runtime := newFakeBackend()
	bot := newTestBot(t, runtime, &fakeChannel{})
	ctx := context.Background()
	chats := []struct {
		id   string
		kind string
	}{
		{id: "ou_private", kind: "p2p"},
		{id: "oc_1", kind: "group"},
		{id: "oc_2", kind: "group"},
		{id: "oc_3", kind: "group"},
	}
	sessionIDs := make(map[string]struct{}, len(chats))
	for index, chat := range chats {
		if err := bot.processMessage(ctx, &types.NormalizedMessage{
			MessageID: "om_new_" + strconv.Itoa(index),
			ChatID:    chat.id,
			ChatType:  chat.kind,
			UserID:    "ou_allowed",
			Content:   "/new",
		}); err != nil {
			t.Fatal(err)
		}
		sessionID, err := bot.store.Get(
			ctx,
			bot.config.ChannelID,
			conversationKey(chat.id),
		)
		if err != nil {
			t.Fatal(err)
		}
		if sessionID == "" {
			t.Fatalf("chat %q has no session", chat.id)
		}
		sessionIDs[sessionID] = struct{}{}
	}
	if len(sessionIDs) != 4 || runtime.createCount != 4 {
		t.Fatalf("session IDs = %#v, creates = %d", sessionIDs, runtime.createCount)
	}
}

func TestAccessPolicy(t *testing.T) {
	bot := &Bot{config: Config{
		AllowedUsers: []string{"ou_allowed"},
		AllowedChats: []string{"oc_allowed"},
	}}
	tests := []struct {
		name string
		msg  *types.NormalizedMessage
		want bool
	}{
		{"allowed dm", &types.NormalizedMessage{ChatType: "p2p", UserID: "ou_allowed"}, true},
		{"denied dm", &types.NormalizedMessage{ChatType: "p2p", UserID: "ou_other"}, false},
		{"allowed group", &types.NormalizedMessage{ChatType: "group", ChatID: "oc_allowed"}, true},
		{"allowed group user", &types.NormalizedMessage{ChatType: "group", ChatID: "oc_other", UserID: "ou_allowed"}, true},
		{"denied group", &types.NormalizedMessage{ChatType: "group", ChatID: "oc_other"}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := bot.allowed(test.msg); got != test.want {
				t.Fatalf("allowed() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestUnboundMessageDoesNotDiscoverExternalConversation(t *testing.T) {
	runtime := newFakeBackend()
	transport := &fakeChannel{}
	bot := newTestBot(t, runtime, transport)
	if err := bot.processMessage(context.Background(), &types.NormalizedMessage{
		MessageID: "om_discover",
		ChatID:    "oc_discover",
		ChatType:  "p2p",
		UserID:    "ou_allowed",
		Content:   "hello",
	}); err != nil {
		t.Fatal(err)
	}
	items, err := bot.store.List(context.Background(), bot.config.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ExternalID == "oc_discover" {
			t.Fatalf("unbound conversation was discovered: %#v", item)
		}
	}
	if runtime.createCount != 0 {
		t.Fatalf("created %d sessions for an unbound message", runtime.createCount)
	}
	if len(transport.sent) != 1 ||
		transport.sent[0].Text != localizedMessage(bot.config.Locale, "conversation_unbound") {
		t.Fatalf("sent messages = %#v", transport.sent)
	}
}

func TestPairingCommandBindsExistingSession(t *testing.T) {
	runtime := newFakeBackend()
	runtime.sessions = append(runtime.sessions, &conversation.Session{ID: "session-a"})
	transport := &fakeChannel{}
	bot := newTestBot(t, runtime, transport)
	pairing, err := bot.pairings.Create(bot.config.ChannelID, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := bot.processMessage(context.Background(), &types.NormalizedMessage{
		MessageID: "om_bind",
		ChatID:    "oc_new",
		ChatType:  "group",
		UserID:    "ou_unknown",
		Content:   "/bind " + pairing.Code,
	}); err != nil {
		t.Fatal(err)
	}
	binding, err := bot.store.ForSession(context.Background(), "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if binding.ExternalID != "oc_new" || binding.Kind != "group" {
		t.Fatalf("binding = %#v", binding)
	}
	if len(transport.sent) != 1 ||
		transport.sent[0].Text != localizedMessage(bot.config.Locale, "pairing_complete") {
		t.Fatalf("sent messages = %#v", transport.sent)
	}
}

func TestStopCommandCancelsCurrentSession(t *testing.T) {
	runtime := newFakeBackend()
	transport := &fakeChannel{}
	bot := newTestBot(t, runtime, transport)
	if err := bot.store.Bind(
		context.Background(),
		bot.config.ChannelID,
		conversationKey("oc_1"),
		"session-1",
	); err != nil {
		t.Fatal(err)
	}

	if err := bot.enqueue(context.Background(), &types.NormalizedMessage{
		MessageID: "om_stop",
		ChatID:    "oc_1",
		ChatType:  "group",
		UserID:    "ou_1",
		Content:   "/stop",
	}); err != nil {
		t.Fatal(err)
	}
	bot.wg.Wait()

	if len(runtime.cancelled) != 1 || runtime.cancelled[0] != "session-1" {
		t.Fatalf("cancelled sessions = %#v", runtime.cancelled)
	}
	if len(transport.sent) != 1 ||
		transport.sent[0].Text != localizedMessage(bot.config.Locale, "task_stopped") {
		t.Fatalf("sent messages = %#v", transport.sent)
	}
	if len(transport.reactions) != 1 ||
		transport.reactions[0].messageID != "om_stop" ||
		transport.reactions[0].emojiType != acknowledgementReaction {
		t.Fatalf("reactions = %#v", transport.reactions)
	}
}

func TestNewSupportsPairingOnlyAccess(t *testing.T) {
	runtime := newFakeBackend()
	bindings := foyachannel.NewConversationBindingStore(testkit.OpenDatabase(t))
	_, err := New(
		Config{ChannelID: "channel-1", ApprovalMode: interaction.ModeAuto},
		runtime,
		bindings,
		foyachannel.NewConversationPairingStore(bindings, runtime),
		&fakeChannel{},
		log.Default(),
	)
	if err != nil {
		t.Fatalf("New failed for pairing-only access: %v", err)
	}
}

type fakeBackend struct {
	mu          sync.Mutex
	createCount int
	sessions    []*conversation.Session
	inputs      []conversation.UserInput
	images      [][]byte
	cancelled   []string
	subscribers map[string]chan conversation.Event
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		subscribers: make(map[string]chan conversation.Event),
	}
}

func (b *fakeBackend) CreateSession(options conversation.CreateOptions) (*conversation.Session, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.createCount++
	created := &conversation.Session{
		ID:           "session-" + strconv.Itoa(b.createCount),
		ConnectionID: options.ConnectionID,
		Model:        options.Model,
		ProjectID:    options.ProjectID,
		ApprovalMode: options.ApprovalMode,
	}
	b.sessions = append(b.sessions, created)
	return created, nil
}

func (b *fakeBackend) ListSessions() []*conversation.Session {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]*conversation.Session(nil), b.sessions...)
}

func (b *fakeBackend) Subscribe(_ context.Context, sessionID string) <-chan conversation.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	events := make(chan conversation.Event, 4)
	b.subscribers[sessionID] = events
	return events
}

func (b *fakeBackend) SubmitChatInput(_ context.Context, sessionID string, input conversation.UserInput) error {
	b.mu.Lock()
	b.inputs = append(b.inputs, input)
	events := b.subscribers[sessionID]
	b.mu.Unlock()
	events <- conversation.Event{Kind: conversation.KindMessageDelta, Payload: "hel"}
	events <- conversation.Event{
		Kind: conversation.KindMessageEnd,
		Payload: conversation.Message{
			Role:    conversation.RoleAssistant,
			Content: "hello",
		},
	}
	events <- conversation.Event{Kind: conversation.KindTurnComplete}
	return nil
}

func (b *fakeBackend) PutImage(_ context.Context, _ string, _ string, source io.Reader) (conversation.AttachmentRef, error) {
	data, err := io.ReadAll(source)
	if err != nil {
		return conversation.AttachmentRef{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.images = append(b.images, data)
	return conversation.AttachmentRef{ID: "artifact-1", Kind: "image"}, nil
}

func (b *fakeBackend) CancelTurn(sessionID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cancelled = append(b.cancelled, sessionID)
}

func (b *fakeBackend) ResolveApproval(string, string) error {
	return nil
}

func (b *fakeBackend) CancelQuestions(string, string) error {
	return nil
}

type fakeChannel struct {
	mu        sync.Mutex
	sent      []*types.SendInput
	streams   []*fakeStream
	downloads map[string][]byte
	reactions []fakeReaction
}

func (c *fakeChannel) Send(_ context.Context, input *types.SendInput) (*types.SendResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, input)
	return &types.SendResult{MessageID: "om_reply", ChatID: input.ChatID}, nil
}

func (c *fakeChannel) Stream(_ context.Context, input *types.SendInput) (types.StreamController, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	stream := &fakeStream{}
	c.streams = append(c.streams, stream)
	return stream, nil
}

func (c *fakeChannel) DownloadFile(_ context.Context, key string, _ string) ([]byte, error) {
	data, ok := c.downloads[key]
	if !ok {
		return nil, errors.New("missing download")
	}
	return data, nil
}

func (c *fakeChannel) React(_ context.Context, messageID, emojiType string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reactions = append(c.reactions, fakeReaction{messageID: messageID, emojiType: emojiType})
	return nil
}

func (c *fakeChannel) OnMessage(func(context.Context, *types.NormalizedMessage) error) {}
func (c *fakeChannel) OnReaction(func(context.Context, *types.ReactionEvent) error)    {}
func (c *fakeChannel) OnComment(func(context.Context, *types.CommentEvent) error)      {}
func (c *fakeChannel) OnBotAdded(func(context.Context, *types.BotAddedEvent) error)    {}
func (c *fakeChannel) OnCardAction(func(context.Context, *types.CardActionEvent) error) {
}
func (c *fakeChannel) OnReject(func(context.Context, *types.RejectEvent) error) {}
func (c *fakeChannel) OnReady(func())                                           {}
func (c *fakeChannel) OnError(func(error))                                      {}
func (c *fakeChannel) OnReconnecting(func())                                    {}
func (c *fakeChannel) OnReconnected(func())                                     {}
func (c *fakeChannel) OnDisconnected(func())                                    {}
func (c *fakeChannel) Start(context.Context) error                              { return nil }
func (c *fakeChannel) Stop(context.Context) error                               { return nil }
func (c *fakeChannel) UpdatePolicy(types.PolicyConfig)                          {}
func (c *fakeChannel) GetPolicy() types.PolicyConfig                            { return types.PolicyConfig{} }
func (c *fakeChannel) GetBotIdentity(context.Context) *types.BotIdentity        { return nil }

type fakeStream struct {
	mu      sync.Mutex
	chunks  []string
	closed  bool
	flushes int
}

func (s *fakeStream) Append(_ context.Context, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chunks = append(s.chunks, text)
	return nil
}

func (s *fakeStream) UpdateCard(context.Context, string) error {
	return errors.New("unexpected card update")
}

func (s *fakeStream) Flush(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushes++
	return nil
}

func (s *fakeStream) Close(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *fakeStream) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result string
	for _, chunk := range s.chunks {
		result += chunk
	}
	return result
}

type fakeReaction struct {
	messageID string
	emojiType string
}

func newTestBot(t *testing.T, runtime foyachannel.Runtime, transport Channel) *Bot {
	t.Helper()
	bindings := foyachannel.NewConversationBindingStore(testkit.OpenDatabase(t))
	bot, err := New(Config{
		ChannelID:    "channel-1",
		ApprovalMode: interaction.ModeAuto,
		AllowedUsers: []string{
			"ou_allowed",
		},
		AllowedChats: []string{
			"oc_1",
			"oc_image",
		},
	}, runtime, bindings, foyachannel.NewConversationPairingStore(bindings, runtime), transport, log.Default())
	if err != nil {
		t.Fatal(err)
	}
	for _, chatID := range []string{"oc_1", "oc_image"} {
		if err := bot.store.Observe(
			context.Background(),
			bot.config.ChannelID,
			conversationKey(chatID),
			"group",
			chatID,
			chatID,
		); err != nil {
			t.Fatal(err)
		}
	}
	return bot
}

func bindChatToNewSession(
	t *testing.T,
	bot *Bot,
	runtime *fakeBackend,
	chatID string,
) {
	t.Helper()
	session, err := runtime.CreateSession(conversation.CreateOptions{
		ApprovalMode: string(interaction.ModeAuto),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := bot.store.Bind(
		context.Background(),
		bot.config.ChannelID,
		conversationKey(chatID),
		session.ID,
	); err != nil {
		t.Fatal(err)
	}
}

var _ foyachannel.Runtime = (*fakeBackend)(nil)
var _ Channel = (*fakeChannel)(nil)
