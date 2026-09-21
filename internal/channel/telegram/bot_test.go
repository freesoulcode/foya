package telegram

import (
	"context"
	"io"
	"log"
	"strconv"
	"sync"
	"testing"

	foyachannel "github.com/freesoulcode/foya/internal/channel"
	"github.com/freesoulcode/foya/internal/conversation"
	"github.com/freesoulcode/foya/internal/interaction"
	"github.com/freesoulcode/foya/internal/testkit"
)

func TestProcessMessageReturnsResponseAndReusesSession(t *testing.T) {
	runtime := newFakeRuntime()
	api := &fakeAPI{}
	bot := newTestBot(t, runtime, api)
	observeChat(t, bot, "101")
	bindChatToNewSession(t, bot, runtime, "101")
	message := Message{
		ID:   1,
		Chat: Chat{ID: 101, Type: "private", FirstName: "Ada"},
		From: &User{ID: 201, Username: "ada"},
		Text: "hello",
	}

	if err := bot.processMessage(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if err := bot.processMessage(context.Background(), message); err != nil {
		t.Fatal(err)
	}

	if runtime.createCount != 1 {
		t.Fatalf("created %d sessions, want 1", runtime.createCount)
	}
	if len(runtime.inputs) != 2 || runtime.inputs[0].Text != "hello" {
		t.Fatalf("submitted inputs = %#v", runtime.inputs)
	}
	if len(api.sent) != 2 || api.sent[0].text != "hello" {
		t.Fatalf("sent messages = %#v", api.sent)
	}
}

func TestNewCommandReplacesCurrentChatSession(t *testing.T) {
	runtime := newFakeRuntime()
	api := &fakeAPI{}
	bot := newTestBot(t, runtime, api)
	observeChat(t, bot, "101")
	first, err := bot.createSession(context.Background(), "101")
	if err != nil {
		t.Fatal(err)
	}

	if err := bot.processMessage(context.Background(), Message{
		ID: 2, Chat: Chat{ID: 101, Type: "private"}, Text: "/new",
	}); err != nil {
		t.Fatal(err)
	}

	second, err := bot.bindings.Get(
		context.Background(),
		bot.config.ChannelID,
		conversationKey("101"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || runtime.createCount != 2 {
		t.Fatalf("sessions = %q then %q, creates = %d", first, second, runtime.createCount)
	}
	if oldBinding, err := bot.bindings.ForSession(
		context.Background(),
		first,
	); err != nil || oldBinding.ActiveSessionID != "" {
		t.Fatalf("old session binding = %#v, err = %v", oldBinding, err)
	}
	if len(api.sent) != 1 ||
		api.sent[0].text != localizedMessage(bot.config.Locale, "new_chat") {
		t.Fatalf("sent messages = %#v", api.sent)
	}
}

func TestOneBotStartsIndependentSessionsFromPrivateAndThreeGroupChats(t *testing.T) {
	runtime := newFakeRuntime()
	bot := newTestBot(t, runtime, &fakeAPI{})
	chats := []Chat{
		{ID: 101, Type: "private", FirstName: "Ada"},
		{ID: -201, Type: "group", Title: "Group 1"},
		{ID: -202, Type: "group", Title: "Group 2"},
		{ID: -203, Type: "supergroup", Title: "Group 3"},
	}
	sessionIDs := make(map[string]struct{}, len(chats))
	for index, chat := range chats {
		if err := bot.processMessage(context.Background(), Message{
			ID: int64(index + 1), Chat: chat, Text: "/new",
		}); err != nil {
			t.Fatal(err)
		}
		chatID := strconv.FormatInt(chat.ID, 10)
		sessionID, err := bot.bindings.Get(
			context.Background(),
			bot.config.ChannelID,
			conversationKey(chatID),
		)
		if err != nil {
			t.Fatal(err)
		}
		if sessionID == "" {
			t.Fatalf("chat %q has no session", chatID)
		}
		sessionIDs[sessionID] = struct{}{}
	}
	if len(sessionIDs) != 4 || runtime.createCount != 4 {
		t.Fatalf("session IDs = %#v, creates = %d", sessionIDs, runtime.createCount)
	}
}

func TestAllowedAndAddressedMessages(t *testing.T) {
	bot := &Bot{
		config: Config{
			AllowedUsers: []string{"201", "ada"},
			AllowedChats: []string{"301"},
		},
		username: "foya_bot",
	}
	tests := []struct {
		name      string
		message   Message
		allowed   bool
		addressed bool
	}{
		{
			name: "allowed private user",
			message: Message{
				Chat: Chat{ID: 101, Type: "private"},
				From: &User{ID: 201},
			},
			allowed: true, addressed: true,
		},
		{
			name: "allowed username",
			message: Message{
				Chat: Chat{ID: 102, Type: "private"},
				From: &User{ID: 999, Username: "Ada"},
			},
			allowed: true, addressed: true,
		},
		{
			name: "denied private user",
			message: Message{
				Chat: Chat{ID: 103, Type: "private"},
				From: &User{ID: 999},
			},
			addressed: true,
		},
		{
			name: "mentioned in allowed group",
			message: Message{
				Chat: Chat{ID: 301, Type: "group"},
				From: &User{ID: 201},
				Text: "@foya_bot hello",
			},
			allowed: true, addressed: true,
		},
		{
			name: "not mentioned in allowed group",
			message: Message{
				Chat: Chat{ID: 301, Type: "group"},
				From: &User{ID: 201},
				Text: "hello",
			},
			allowed: true,
		},
		{
			name: "allowed user in new group",
			message: Message{
				Chat: Chat{ID: 302, Type: "group"},
				From: &User{ID: 201},
				Text: "/new",
			},
			allowed: true, addressed: true,
		},
		{
			name: "command addressed to another bot",
			message: Message{
				Chat: Chat{ID: 301, Type: "group"},
				Text: "/new@other_bot",
			},
			allowed: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := bot.allowed(test.message); got != test.allowed {
				t.Fatalf("allowed() = %v, want %v", got, test.allowed)
			}
			if got := bot.addressed(test.message); got != test.addressed {
				t.Fatalf("addressed() = %v, want %v", got, test.addressed)
			}
		})
	}
}

func TestUnboundMessageDoesNotDiscoverConversation(t *testing.T) {
	runtime := newFakeRuntime()
	api := &fakeAPI{}
	bot := newTestBot(t, runtime, api)
	bot.config.AllowAll = false
	bot.config.AllowedUsers = []string{"201"}

	if err := bot.processMessage(context.Background(), Message{
		ID:   3,
		Chat: Chat{ID: 101, Type: "private", FirstName: "Ada", LastName: "Lovelace"},
		From: &User{ID: 201},
		Text: "hello",
	}); err != nil {
		t.Fatal(err)
	}

	items, err := bot.bindings.List(context.Background(), bot.config.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("discovered conversations = %#v", items)
	}
	if runtime.createCount != 0 {
		t.Fatalf("created %d sessions for an unbound message", runtime.createCount)
	}
	if len(api.sent) != 1 ||
		api.sent[0].text != localizedMessage(bot.config.Locale, "conversation_unbound") {
		t.Fatalf("sent messages = %#v", api.sent)
	}
}

func TestPairingCommandBindsExistingSession(t *testing.T) {
	runtime := newFakeRuntime()
	runtime.sessions = append(runtime.sessions, &conversation.Session{ID: "session-a"})
	api := &fakeAPI{}
	bot := newTestBot(t, runtime, api)
	pairing, err := bot.pairings.Create(bot.config.ChannelID, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := bot.processMessage(context.Background(), Message{
		ID:   4,
		Chat: Chat{ID: 301, Type: "group", Title: "Release"},
		From: &User{ID: 999},
		Text: "/bind " + pairing.Code,
	}); err != nil {
		t.Fatal(err)
	}
	binding, err := bot.bindings.ForSession(context.Background(), "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if binding.ExternalID != "301" || binding.DisplayName != "Release" {
		t.Fatalf("binding = %#v", binding)
	}
	if len(api.sent) != 1 ||
		api.sent[0].text != localizedMessage(bot.config.Locale, "pairing_complete") {
		t.Fatalf("sent messages = %#v", api.sent)
	}
}

func TestNewSupportsPairingOnlyAccess(t *testing.T) {
	runtime := newFakeRuntime()
	bindings := foyachannel.NewConversationBindingStore(testkit.OpenDatabase(t))
	_, err := New(
		Config{ChannelID: "telegram-1", ApprovalMode: interaction.ModeAuto},
		runtime,
		bindings,
		foyachannel.NewConversationPairingStore(bindings, runtime),
		&fakeAPI{},
		log.Default(),
	)
	if err != nil {
		t.Fatalf("New failed for pairing-only access: %v", err)
	}
}

type fakeRuntime struct {
	mu          sync.Mutex
	createCount int
	sessions    []*conversation.Session
	inputs      []conversation.UserInput
	subscribers map[string]chan conversation.Event
	cancelled   []string
}

func newFakeRuntime() *fakeRuntime {
	return &fakeRuntime{subscribers: make(map[string]chan conversation.Event)}
}

func (r *fakeRuntime) CreateSession(
	options conversation.CreateOptions,
) (*conversation.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.createCount++
	item := &conversation.Session{
		ID:           "session-" + strconv.Itoa(r.createCount),
		ConnectionID: options.ConnectionID,
		Model:        options.Model,
		ProjectID:    options.ProjectID,
		ApprovalMode: options.ApprovalMode,
	}
	r.sessions = append(r.sessions, item)
	return item, nil
}

func (r *fakeRuntime) ListSessions() []*conversation.Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*conversation.Session(nil), r.sessions...)
}

func (r *fakeRuntime) Subscribe(
	_ context.Context,
	sessionID string,
) <-chan conversation.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	events := make(chan conversation.Event, 2)
	r.subscribers[sessionID] = events
	return events
}

func (r *fakeRuntime) SubmitChatInput(
	_ context.Context,
	sessionID string,
	input conversation.UserInput,
) error {
	r.mu.Lock()
	r.inputs = append(r.inputs, input)
	events := r.subscribers[sessionID]
	r.mu.Unlock()
	events <- conversation.Event{
		Kind: conversation.KindMessageEnd,
		Payload: conversation.Message{
			Role: conversation.RoleAssistant, Content: "hello",
		},
	}
	events <- conversation.Event{Kind: conversation.KindTurnComplete}
	return nil
}

func (r *fakeRuntime) PutImage(
	context.Context,
	string,
	string,
	io.Reader,
) (conversation.AttachmentRef, error) {
	return conversation.AttachmentRef{}, nil
}

func (r *fakeRuntime) CancelTurn(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancelled = append(r.cancelled, sessionID)
}

func (r *fakeRuntime) ResolveApproval(string, string) error { return nil }
func (r *fakeRuntime) CancelQuestions(string, string) error { return nil }

type sentMessage struct {
	chatID int64
	text   string
}

type fakeAPI struct {
	sent []sentMessage
}

func (a *fakeAPI) GetMe(context.Context) (User, error) {
	return User{ID: 1, IsBot: true, Username: "foya_bot"}, nil
}

func (a *fakeAPI) GetUpdates(ctx context.Context, _ int64) ([]Update, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (a *fakeAPI) SendMessage(_ context.Context, chatID int64, text string) error {
	a.sent = append(a.sent, sentMessage{chatID: chatID, text: text})
	return nil
}

func newTestBot(t *testing.T, runtime foyachannel.Runtime, api API) *Bot {
	t.Helper()
	bindings := foyachannel.NewConversationBindingStore(testkit.OpenDatabase(t))
	bot, err := New(
		Config{
			ChannelID:    "telegram-1",
			ApprovalMode: interaction.ModeAuto,
			Locale:       "zh-CN",
			AllowAll:     true,
		},
		runtime,
		bindings,
		foyachannel.NewConversationPairingStore(bindings, runtime),
		api,
		log.Default(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return bot
}

func observeChat(t *testing.T, bot *Bot, chatID string) {
	t.Helper()
	if err := bot.bindings.Observe(
		context.Background(),
		bot.config.ChannelID,
		conversationKey(chatID),
		"private",
		chatID,
		chatID,
	); err != nil {
		t.Fatal(err)
	}
}

func bindChatToNewSession(
	t *testing.T,
	bot *Bot,
	runtime *fakeRuntime,
	chatID string,
) {
	t.Helper()
	session, err := runtime.CreateSession(conversation.CreateOptions{
		ApprovalMode: string(interaction.ModeAuto),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := bot.bindings.Bind(
		context.Background(),
		bot.config.ChannelID,
		conversationKey(chatID),
		session.ID,
	); err != nil {
		t.Fatal(err)
	}
}

var _ foyachannel.Runtime = (*fakeRuntime)(nil)
var _ API = (*fakeAPI)(nil)
