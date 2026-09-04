package state

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/freesoulcode/foya/internal/compaction"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/storage"
)

func newSQLiteTestStore(t testing.TB) *SQLiteStore {
	t.Helper()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	return NewSQLiteStore(db)
}

func TestSQLiteStorePersistsTypedEventsAndUsageProjection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "foya.db")
	db, err := storage.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	store := NewSQLiteStore(db)
	ctx := context.Background()

	messageSeq := appendStoreMessage(t, store, "session-1", message.Message{
		Role: message.RoleUser, Content: "persist me",
	})
	usage := provider.Usage{
		Model:        "model-a",
		InputTokens:  120,
		OutputTokens: 30,
		TotalTokens:  150,
		CachedTokens: 20,
	}
	usageSeq, err := store.Append(ctx, event.Event{
		Kind: event.KindUsageUpdated, Session: "session-1",
		Time: time.Now(), Payload: usage,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = storage.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store = NewSQLiteStore(db)

	history, err := store.History(ctx, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Content != "persist me" ||
		history[0].EventSeq != uint64(messageSeq) {
		t.Fatalf("history = %#v", history)
	}
	events, err := store.Read(ctx, "session-1", messageSeq)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Seq != usageSeq {
		t.Fatalf("events = %#v", events)
	}
	restoredUsage, ok := events[0].Payload.(provider.Usage)
	if !ok || restoredUsage != usage {
		t.Fatalf("usage payload = %#v", events[0].Payload)
	}

	var (
		model string
		total int64
	)
	if err := db.QueryRow(`
		SELECT model, total_tokens FROM usage_records WHERE event_seq = ?
	`, int64(usageSeq)).Scan(&model, &total); err != nil {
		t.Fatal(err)
	}
	if model != usage.Model || total != usage.TotalTokens {
		t.Fatalf("usage projection = %q/%d", model, total)
	}
}

func TestSQLiteStorePersistsOnlyAttachmentReferences(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := NewSQLiteStore(db)
	ref := message.AttachmentRef{
		ID: "artifact-1", Name: "screen.png", Kind: "image",
		MediaType: "image/png", Bytes: 123, Width: 10, Height: 8,
		SHA256: "checksum",
	}
	appendStoreMessage(t, store, "session-1", message.Message{
		Role: message.RoleUser, Content: "describe",
		Attachments: []message.AttachmentRef{ref},
	})

	var payload []byte
	if err := db.QueryRow(`
		SELECT payload_json FROM events WHERE session_id = ?
	`, "session-1").Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte("data:image/")) ||
		bytes.Contains(payload, []byte(`"data"`)) {
		t.Fatalf("event payload contains inline image data: %s", payload)
	}
	history, err := store.History(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || len(history[0].Attachments) != 1 ||
		history[0].Attachments[0] != ref {
		t.Fatalf("restored attachments = %#v", history)
	}
}

func TestSQLiteStoreBranchesAndInvalidatesCheckpoint(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := NewSQLiteStore(db)
	ctx := context.Background()
	sessionID := "session-1"

	appendStoreMessage(t, store, sessionID, message.Message{
		Role: message.RoleUser, Content: "first",
	})
	appendStoreMessage(t, store, sessionID, message.Message{
		Role: message.RoleAssistant, Content: "answer",
	})
	target := appendStoreMessage(t, store, sessionID, message.Message{
		Role: message.RoleUser, Content: "old question",
	})
	appendStoreMessage(t, store, sessionID, message.Message{
		Role: message.RoleAssistant, Content: "old answer",
	})

	active, err := store.Events(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	plan, ok := compaction.BuildPlan(active, nil, false)
	if !ok {
		t.Fatal("expected compaction plan")
	}
	if _, err := store.RecordCheckpoint(ctx, compaction.Checkpoint{
		SessionID: sessionID, ThroughSeq: plan.ThroughSeq,
		SourceDigest: plan.SourceDigest, Summary: "summary",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	modelHistory, err := store.ModelHistory(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(modelHistory) != 1 || modelHistory[0].Role != message.RoleSystem {
		t.Fatalf("model history = %#v", modelHistory)
	}

	result, err := store.Branch(ctx, sessionID, target, "new question", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied {
		t.Fatalf("branch = %#v", result)
	}
	if _, ok, err := store.Checkpoint(ctx, sessionID); err != nil || ok {
		t.Fatalf("checkpoint after branch: ok=%v err=%v", ok, err)
	}
	appendStoreMessage(t, store, sessionID, message.Message{
		Role: message.RoleUser, Content: "new question",
	})

	history, err := store.History(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 || history[2].Content != "new question" {
		t.Fatalf("active history = %#v", history)
	}
	raw, err := store.Read(ctx, sessionID, 0)
	if err != nil {
		t.Fatal(err)
	}
	foundOldAnswer := false
	for _, ev := range raw {
		item, ok := messageFromEvent(ev)
		foundOldAnswer = foundOldAnswer ||
			(ok && item.Content == "old answer")
	}
	if !foundOldAnswer {
		t.Fatal("branch removed an immutable source event")
	}
}

func TestSQLiteStoreDeleteRejectsLateEventsWithoutReusingSequence(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := NewSQLiteStore(db)
	ctx := context.Background()

	first := appendStoreMessage(t, store, "deleted", message.Message{
		Role: message.RoleUser, Content: "remove",
	})
	if err := store.Delete(ctx, "deleted"); err != nil {
		t.Fatal(err)
	}
	late, err := store.Append(ctx, event.Event{
		Kind: event.KindError, Session: "deleted",
		Time: time.Now(), Payload: "late",
	})
	if err != nil {
		t.Fatal(err)
	}
	if late != first {
		t.Fatalf("late event sequence = %d, want %d", late, first)
	}
	events, err := store.Read(ctx, "deleted", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("deleted events = %#v", events)
	}
	next, err := store.Append(ctx, event.Event{
		Kind: event.KindError, Session: "kept",
		Time: time.Now(), Payload: "next",
	})
	if err != nil {
		t.Fatal(err)
	}
	if next <= first {
		t.Fatalf("sequence was reused: first=%d next=%d", first, next)
	}
}

func TestSQLiteStoreRollsBackInvalidProjection(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := NewSQLiteStore(db)

	_, err = store.Append(context.Background(), event.Event{
		Kind: event.KindMessageEnd, Session: "session-1",
		Time: time.Now(), Payload: "not a message",
	})
	if err == nil {
		t.Fatal("invalid message payload was accepted")
	}
	events, readErr := store.Read(context.Background(), "session-1", 0)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(events) != 0 {
		t.Fatalf("rolled back events = %#v", events)
	}
}

func TestSQLiteStoreRejectsInactiveBranchTarget(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := NewSQLiteStore(db)
	ctx := context.Background()

	target := appendStoreMessage(t, store, "session-1", message.Message{
		Role: message.RoleUser, Content: "question",
	})
	appendStoreMessage(t, store, "session-1", message.Message{
		Role: message.RoleAssistant, Content: "answer",
	})
	if _, err := store.Branch(
		ctx, "session-1", target, "edited", false, 0,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Branch(
		ctx, "session-1", target, "edited again", false, 0,
	); !errors.Is(err, ErrActiveUserMessageNotFound) {
		t.Fatalf("inactive branch target error = %v", err)
	}
}

func appendStoreMessage(
	t *testing.T,
	store Store,
	sessionID string,
	item message.Message,
) event.Seq {
	t.Helper()
	seq, err := store.Append(context.Background(), event.Event{
		Kind: event.KindMessageEnd, Session: sessionID,
		Time: time.Now(), Payload: item,
	})
	if err != nil {
		t.Fatal(err)
	}
	return seq
}
