package channel

import (
	"context"
	"errors"
	"testing"

	"github.com/freesoulcode/foya/internal/testkit"
)

func TestConversationBindingStoreObservesAndBindsOneToOne(t *testing.T) {
	db := testkit.OpenDatabase(t)
	store := NewConversationBindingStore(db)
	ctx := context.Background()

	if err := store.Observe(
		ctx, "feishu-1", "chat:oc_1", "group", "oc_1", "Release group",
	); err != nil {
		t.Fatal(err)
	}
	if err := store.Observe(
		ctx, "feishu-1", "chat:oc_2", "p2p", "oc_2", "ou_2",
	); err != nil {
		t.Fatal(err)
	}
	if err := store.Bind(ctx, "feishu-1", "chat:oc_1", "session-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.Bind(ctx, "feishu-1", "chat:oc_2", "session-1"); err != nil {
		t.Fatal(err)
	}
	first, err := store.Get(ctx, "feishu-1", "chat:oc_1")
	if err != nil {
		t.Fatal(err)
	}
	if first != "" {
		t.Fatalf("first conversation still bound to %q", first)
	}
	second, err := store.Get(ctx, "feishu-1", "chat:oc_2")
	if err != nil {
		t.Fatal(err)
	}
	if second != "session-1" {
		t.Fatalf("second conversation session = %q", second)
	}
	binding, err := store.ForSession(ctx, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if binding.ConversationKey != "chat:oc_2" || binding.DisplayName != "ou_2" {
		t.Fatalf("session binding = %#v", binding)
	}
	reloaded := NewConversationBindingStore(db)
	persisted, err := reloaded.Get(ctx, "feishu-1", "chat:oc_2")
	if err != nil || persisted != "session-1" {
		t.Fatalf("persisted binding = %q, err = %v", persisted, err)
	}
	items, err := store.List(ctx, "feishu-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("conversations = %#v", items)
	}
}

func TestConversationBindingStoreRejectsUnknownConversation(t *testing.T) {
	store := NewConversationBindingStore(testkit.OpenDatabase(t))
	err := store.Bind(context.Background(), "feishu-1", "chat:missing", "session-1")
	if !errors.Is(err, ErrConversationNotFound) {
		t.Fatalf("bind error = %v", err)
	}
}

func TestConversationBindingStoreUnbindsSessionAndDeletesChannel(t *testing.T) {
	store := NewConversationBindingStore(testkit.OpenDatabase(t))
	ctx := context.Background()
	if err := store.Observe(
		ctx, "feishu-1", "chat:oc_1", "group", "oc_1", "",
	); err != nil {
		t.Fatal(err)
	}
	if err := store.Bind(ctx, "feishu-1", "chat:oc_1", "session-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.UnbindSession(ctx, "session-1"); err != nil {
		t.Fatal(err)
	}
	active, err := store.Get(ctx, "feishu-1", "chat:oc_1")
	if err != nil || active != "" {
		t.Fatalf("active session = %q, err = %v", active, err)
	}
	if err := store.DeleteChannel(ctx, "feishu-1"); err != nil {
		t.Fatal(err)
	}
	items, err := store.List(ctx, "feishu-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("conversations after delete = %#v", items)
	}
}
