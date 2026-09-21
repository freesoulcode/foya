package channel

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/freesoulcode/foya/internal/conversation"
	"github.com/freesoulcode/foya/internal/testkit"
)

func TestConversationPairingBindsAndRebindsOneToOne(t *testing.T) {
	ctx := context.Background()
	bindings := NewConversationBindingStore(testkit.OpenDatabase(t))
	runtime := &pairingRuntime{sessions: []*conversation.Session{
		{ID: "session-a"},
		{ID: "session-b"},
	}}
	pairings := NewConversationPairingStore(bindings, runtime)

	first, err := pairings.Create("telegram-1", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	completed, err := pairings.Consume(
		ctx,
		"telegram-1",
		first.Code,
		"chat:101",
		"private",
		"101",
		"Ada",
	)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != PairingCompleted ||
		completed.Conversation == nil ||
		completed.Conversation.ActiveSessionID != "session-a" {
		t.Fatalf("completed pairing = %#v", completed)
	}
	if _, err := pairings.Consume(
		ctx,
		"telegram-1",
		first.Code,
		"chat:101",
		"private",
		"101",
		"Ada",
	); err != nil {
		t.Fatalf("idempotent consume: %v", err)
	}

	second, err := pairings.Create("telegram-1", "session-b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pairings.Consume(
		ctx,
		"telegram-1",
		second.Code,
		"chat:101",
		"private",
		"101",
		"Ada",
	); err != nil {
		t.Fatal(err)
	}
	if binding, err := bindings.ForSession(ctx, "session-a"); err != nil ||
		binding.ActiveSessionID != "" {
		t.Fatalf("old session binding = %#v, err = %v", binding, err)
	}
	if binding, err := bindings.ForSession(ctx, "session-b"); err != nil ||
		binding.ExternalID != "101" {
		t.Fatalf("new session binding = %#v, err = %v", binding, err)
	}
}

func TestOneChannelPairsPrivateChatAndThreeGroups(t *testing.T) {
	ctx := context.Background()
	bindings := NewConversationBindingStore(testkit.OpenDatabase(t))
	runtime := &pairingRuntime{sessions: []*conversation.Session{
		{ID: "session-a"},
		{ID: "session-b"},
		{ID: "session-c"},
		{ID: "session-d"},
	}}
	pairings := NewConversationPairingStore(bindings, runtime)
	conversations := []struct {
		sessionID string
		key       string
		kind      string
		external  string
	}{
		{sessionID: "session-a", key: "chat:user-1", kind: "p2p", external: "user-1"},
		{sessionID: "session-b", key: "chat:group-1", kind: "group", external: "group-1"},
		{sessionID: "session-c", key: "chat:group-2", kind: "group", external: "group-2"},
		{sessionID: "session-d", key: "chat:group-3", kind: "group", external: "group-3"},
	}
	for _, conversation := range conversations {
		pairing, err := pairings.Create("feishu-1", conversation.sessionID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pairings.Consume(
			ctx,
			"feishu-1",
			pairing.Code,
			conversation.key,
			conversation.kind,
			conversation.external,
			conversation.external,
		); err != nil {
			t.Fatal(err)
		}
	}
	items, err := bindings.List(ctx, "feishu-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 {
		t.Fatalf("bindings = %#v", items)
	}
	for _, item := range items {
		if item.ActiveSessionID == "" {
			t.Fatalf("unbound conversation = %#v", item)
		}
	}
}

func TestConversationPairingRejectsWrongChannelAndExpiredCode(t *testing.T) {
	bindings := NewConversationBindingStore(testkit.OpenDatabase(t))
	runtime := &pairingRuntime{
		sessions: []*conversation.Session{{ID: "session-a"}},
	}
	pairings := NewConversationPairingStore(bindings, runtime)
	item, err := pairings.Create("feishu-1", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pairings.Consume(
		context.Background(),
		"telegram-1",
		item.Code,
		"chat:101",
		"private",
		"101",
		"",
	); !errors.Is(err, ErrPairingChannelMismatch) {
		t.Fatalf("wrong-channel error = %v", err)
	}

	pairings.ttl = -time.Second
	expired, err := pairings.Create("feishu-1", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := pairings.Get(expired.ID); !ok || got.Status != PairingExpired {
		t.Fatalf("expired pairing = %#v, ok = %v", got, ok)
	}
	if _, err := pairings.Consume(
		context.Background(),
		"feishu-1",
		expired.Code,
		"chat:expired",
		"group",
		"expired",
		"",
	); !errors.Is(err, ErrPairingExpired) {
		t.Fatalf("expired-code error = %v", err)
	}
}

func TestConversationPairingSupersedesPendingCode(t *testing.T) {
	bindings := NewConversationBindingStore(testkit.OpenDatabase(t))
	runtime := &pairingRuntime{
		sessions: []*conversation.Session{{ID: "session-a"}},
	}
	pairings := NewConversationPairingStore(bindings, runtime)
	first, err := pairings.Create("feishu-1", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := pairings.Create("telegram-1", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := pairings.Get(first.ID); got.Status != PairingCancelled {
		t.Fatalf("first pairing = %#v", got)
	}
	if second.Status != PairingPending ||
		len(second.Code) != 8 ||
		second.Command != "/bind "+second.Code {
		t.Fatalf("second pairing = %#v", second)
	}
}

func TestConversationPairingRejectsMissingSession(t *testing.T) {
	bindings := NewConversationBindingStore(testkit.OpenDatabase(t))
	pairings := NewConversationPairingStore(bindings, &pairingRuntime{})
	if _, err := pairings.Create(
		"feishu-1",
		"missing",
	); !errors.Is(err, ErrPairingSessionNotFound) {
		t.Fatalf("missing-session error = %v", err)
	}
}

func TestParsePairingCommand(t *testing.T) {
	tests := []struct {
		input string
		code  string
		ok    bool
	}{
		{input: "/bind A2BC34", code: "A2BC34", ok: true},
		{input: "/BIND@foya_bot a2bc34", code: "A2BC34", ok: true},
		{input: "/bind", ok: true},
		{input: "hello"},
	}
	for _, test := range tests {
		code, ok := ParsePairingCommand(test.input)
		if code != test.code || ok != test.ok {
			t.Fatalf("ParsePairingCommand(%q) = %q, %v", test.input, code, ok)
		}
	}
}

type pairingRuntime struct {
	sessions []*conversation.Session
}

func (*pairingRuntime) CreateSession(
	conversation.CreateOptions,
) (*conversation.Session, error) {
	return nil, nil
}

func (r *pairingRuntime) ListSessions() []*conversation.Session {
	return r.sessions
}

func (*pairingRuntime) Subscribe(
	context.Context,
	string,
) <-chan conversation.Event {
	return make(chan conversation.Event)
}

func (*pairingRuntime) SubmitChatInput(
	context.Context,
	string,
	conversation.UserInput,
) error {
	return nil
}

func (*pairingRuntime) PutImage(
	context.Context,
	string,
	string,
	io.Reader,
) (conversation.AttachmentRef, error) {
	return conversation.AttachmentRef{}, nil
}

func (*pairingRuntime) CancelTurn(string)                    {}
func (*pairingRuntime) ResolveApproval(string, string) error { return nil }
func (*pairingRuntime) CancelQuestions(string, string) error { return nil }

var _ Runtime = (*pairingRuntime)(nil)
