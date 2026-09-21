package channelhub

import (
	"context"
	"io"
	"testing"

	foyachannel "github.com/freesoulcode/foya/internal/channel"
	"github.com/freesoulcode/foya/internal/conversation"
	"github.com/freesoulcode/foya/internal/interaction"
	"github.com/freesoulcode/foya/internal/testkit"
)

func TestManagerDispatchesBothChannelKinds(t *testing.T) {
	bindings := foyachannel.NewConversationBindingStore(testkit.OpenDatabase(t))
	runtime := &fakeRuntime{
		sessions: []*conversation.Session{{ID: "session-a"}},
	}
	manager, err := NewManager(
		context.Background(),
		t.TempDir(),
		runtime,
		bindings,
		nil,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	feishuState, err := manager.Create(UpdateInput{
		Kind: KindFeishu, Name: "Feishu", ApprovalMode: interaction.ModeAuto,
	})
	if err != nil {
		t.Fatal(err)
	}
	telegramState, err := manager.Create(UpdateInput{
		Kind: KindTelegram, Name: "Telegram", ApprovalMode: interaction.ModeAuto,
	})
	if err != nil {
		t.Fatal(err)
	}
	if feishuState.Kind != KindFeishu || telegramState.Kind != KindTelegram {
		t.Fatalf("states = %#v, %#v", feishuState, telegramState)
	}
	if feishuState.AllowedUsers == nil || telegramState.AllowedChats == nil {
		t.Fatalf("channel access lists must be JSON arrays: %#v, %#v", feishuState, telegramState)
	}
	if items := manager.List(); len(items) != 2 {
		t.Fatalf("channels = %#v", items)
	}

	pairing, err := manager.pairings.Create(telegramState.ID, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.pairings.Consume(
		context.Background(),
		telegramState.ID,
		pairing.Code,
		"chat:101",
		"private",
		"101",
		"Ada",
	); err != nil {
		t.Fatal(err)
	}
	items, err := bindings.List(context.Background(), telegramState.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ActiveSessionID != "session-a" {
		t.Fatalf("conversations = %#v", items)
	}
}

func TestManagerRejectsKindChanges(t *testing.T) {
	manager, err := NewManager(
		context.Background(),
		t.TempDir(),
		&fakeRuntime{},
		foyachannel.NewConversationBindingStore(testkit.OpenDatabase(t)),
		nil,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	state, err := manager.Create(UpdateInput{
		Kind: KindFeishu, Name: "Feishu", ApprovalMode: interaction.ModeAuto,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Update(state.ID, UpdateInput{
		Kind: KindTelegram, Name: "Telegram", ApprovalMode: interaction.ModeAuto,
	}); err == nil {
		t.Fatal("channel kind change succeeded")
	}
}

type fakeRuntime struct {
	sessions []*conversation.Session
}

func (*fakeRuntime) CreateSession(
	conversation.CreateOptions,
) (*conversation.Session, error) {
	return &conversation.Session{ID: "session-1"}, nil
}

func (r *fakeRuntime) ListSessions() []*conversation.Session {
	return r.sessions
}

func (*fakeRuntime) Subscribe(
	context.Context,
	string,
) <-chan conversation.Event {
	return make(chan conversation.Event)
}

func (*fakeRuntime) SubmitChatInput(
	context.Context,
	string,
	conversation.UserInput,
) error {
	return nil
}

func (*fakeRuntime) PutImage(
	context.Context,
	string,
	string,
	io.Reader,
) (conversation.AttachmentRef, error) {
	return conversation.AttachmentRef{}, nil
}

func (*fakeRuntime) CancelTurn(string)                    {}
func (*fakeRuntime) ResolveApproval(string, string) error { return nil }
func (*fakeRuntime) CancelQuestions(string, string) error { return nil }

var _ foyachannel.Runtime = (*fakeRuntime)(nil)
