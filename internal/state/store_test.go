package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/freesoulcode/foya/internal/compaction"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/storage"
)

func newTestStore(t testing.TB) Store {
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
	return NewStore(db)
}

func TestStorePersistsTypedEventsAndUsageProjection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "foya.db")
	db, err := storage.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(db)
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
	store = NewStore(db)

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
	summary, err := store.UsageSummary(ctx, UsageQuery{
		RangeStart:    time.Now().AddDate(0, 0, -1).Format(time.DateOnly),
		RangeEnd:      time.Now().AddDate(0, 0, 1).Format(time.DateOnly),
		ActivityStart: time.Now().AddDate(0, 0, -1).Format(time.DateOnly),
		Today:         time.Now().Format(time.DateOnly),
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalTokens != usage.TotalTokens ||
		summary.InputTokens != usage.InputTokens ||
		summary.OutputTokens != usage.OutputTokens ||
		summary.CachedTokens != usage.CachedTokens ||
		summary.MessageCount != 1 ||
		summary.SessionCount != 1 ||
		len(summary.ModelUsage) != 1 ||
		summary.ModelUsage[0].Model != usage.Model ||
		summary.ModelUsage[0].RequestCount != 1 {
		t.Fatalf("usage ledger summary = %#v", summary)
	}
}

func TestStorePersistsOnlyAttachmentReferences(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := NewStore(db)
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

func TestStoreDeduplicatesAndCollectsFileBlobs(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := NewStore(db)
	before := []byte("before\n")
	after := []byte("after\n")
	change := message.FileChange{
		Path:            "/workspace/file.txt",
		BeforeExists:    true,
		BeforeMode:      0o644,
		AfterMode:       0o644,
		BeforeBlob:      fileBlobHash(before),
		AfterBlob:       fileBlobHash(after),
		BeforeContent:   before,
		AfterContent:    after,
		ContentCaptured: true,
	}

	sourceSeq := appendStoreMessage(t, store, "session-1", message.Message{
		Role:       message.RoleTool,
		ToolCallID: "write-1",
		Content:    "written",
		Diff:       "display diff",
		FileChange: &change,
	})
	history, err := store.History(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ImportMessages(
		context.Background(),
		"session-2",
		"session-1",
		sourceSeq,
		history,
	); err != nil {
		t.Fatal(err)
	}

	var blobCount, changeCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM file_blobs`).Scan(&blobCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM file_changes`).Scan(&changeCount); err != nil {
		t.Fatal(err)
	}
	if blobCount != 2 || changeCount != 2 {
		t.Fatalf("blob/change counts = %d/%d, want 2/2", blobCount, changeCount)
	}
	restored, err := store.FileBlob(context.Background(), change.BeforeBlob)
	if err != nil || !bytes.Equal(restored, before) {
		t.Fatalf("restored blob = %q, err = %v", restored, err)
	}

	var payload []byte
	if err := db.QueryRow(`
		SELECT payload_json
		FROM events
		WHERE session_id = ?
	`, "session-1").Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, before) ||
		bytes.Contains(payload, []byte("before_content")) ||
		bytes.Contains(payload, []byte("after_content")) {
		t.Fatalf("event contains inline file snapshot: %s", payload)
	}

	if err := store.Delete(context.Background(), "session-1"); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM file_blobs`).Scan(&blobCount); err != nil {
		t.Fatal(err)
	}
	if blobCount != 2 {
		t.Fatalf("shared blobs were collected early: %d", blobCount)
	}
	if err := store.Delete(context.Background(), "session-2"); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM file_blobs`).Scan(&blobCount); err != nil {
		t.Fatal(err)
	}
	if blobCount != 0 {
		t.Fatalf("unreferenced blobs remain: %d", blobCount)
	}
}

func TestStorePrunesExpiredFileCheckpointData(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := NewStore(db)
	ctx := context.Background()
	sessionID := "session-1"
	oldTime := time.Now().Add(-fileCheckpointRetention - time.Hour)
	target := appendStoreEvent(t, store, event.Event{
		Kind:    event.KindMessageEnd,
		Session: sessionID,
		Time:    oldTime,
		Payload: message.Message{Role: message.RoleUser, Content: "old request"},
	})
	before := []byte("before\n")
	after := []byte("after\n")
	appendStoreEvent(t, store, event.Event{
		Kind:    event.KindMessageEnd,
		Session: sessionID,
		Time:    oldTime,
		Payload: message.Message{
			Role: message.RoleTool,
			FileChange: &message.FileChange{
				Path:            "/workspace/file.txt",
				BeforeExists:    true,
				BeforeMode:      0o644,
				AfterMode:       0o644,
				BeforeBlob:      fileBlobHash(before),
				AfterBlob:       fileBlobHash(after),
				BeforeContent:   before,
				AfterContent:    after,
				ContentCaptured: true,
			},
		},
	})

	var blobCount, changeCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM file_blobs`).Scan(&blobCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM file_changes`).Scan(&changeCount); err != nil {
		t.Fatal(err)
	}
	if blobCount != 0 || changeCount != 0 {
		t.Fatalf("expired blob/change counts = %d/%d, want 0/0", blobCount, changeCount)
	}
	preview, err := store.Rewind(ctx, sessionID, target, false, 0, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.FileChanges) != 0 {
		t.Fatalf("expired file changes = %#v", preview.FileChanges)
	}
}

func TestStoreKeepsLatestHundredFileCheckpoints(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := NewStore(db)
	sessionID := "session-1"
	path := "/workspace/file.txt"

	for index := 0; index < fileCheckpointLimit+1; index++ {
		before := []byte(fmt.Sprintf("version-%d\n", index))
		after := []byte(fmt.Sprintf("version-%d\n", index+1))
		appendStoreMessage(t, store, sessionID, message.Message{
			Role:    message.RoleUser,
			Content: fmt.Sprintf("request-%d", index),
		})
		appendStoreMessage(t, store, sessionID, message.Message{
			Role: message.RoleTool,
			FileChange: &message.FileChange{
				Path:            path,
				BeforeExists:    true,
				BeforeMode:      0o644,
				AfterMode:       0o644,
				BeforeBlob:      fileBlobHash(before),
				AfterBlob:       fileBlobHash(after),
				BeforeContent:   before,
				AfterContent:    after,
				ContentCaptured: true,
			},
		})
	}

	var changeCount int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM file_changes WHERE session_id = ?
	`, sessionID).Scan(&changeCount); err != nil {
		t.Fatal(err)
	}
	if changeCount != fileCheckpointLimit {
		t.Fatalf("retained file changes = %d, want %d", changeCount, fileCheckpointLimit)
	}
}

func TestStoreRewindsAndInvalidatesCheckpoint(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := NewStore(db)
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

	preview, err := store.Rewind(ctx, sessionID, target, false, 0, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Applied || preview.Message.Content != "old question" {
		t.Fatalf("rewind preview = %#v", preview)
	}
	result, err := store.Rewind(ctx, sessionID, target, true, preview.HeadSeq, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied {
		t.Fatalf("rewind = %#v", result)
	}
	if _, ok, err := store.Checkpoint(ctx, sessionID); err != nil || ok {
		t.Fatalf("checkpoint after rewind: ok=%v err=%v", ok, err)
	}

	history, err := store.History(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 ||
		history[0].Content != "first" ||
		history[1].Content != "answer" {
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
		t.Fatal("rewind removed an immutable source event")
	}
}

func TestStoreDeleteRejectsLateEventsWithoutReusingSequence(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := NewStore(db)
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

func TestStoreDeletePreservesHistoricalUsageLedger(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := NewStore(db)
	ctx := context.Background()
	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.Local)

	appendStoreEvent(t, store, event.Event{
		Kind: event.KindMessageEnd, Session: "session-1", Time: now,
		Payload: message.Message{Role: message.RoleUser, Content: "hello"},
	})
	appendStoreEvent(t, store, event.Event{
		Kind: event.KindUsageUpdated, Session: "session-1", Time: now,
		Payload: provider.Usage{
			Model: "model-a", InputTokens: 75, OutputTokens: 25, TotalTokens: 100,
		},
	})
	before := loadUsageSummaryForTest(t, store, now)
	if err := store.Delete(ctx, "session-1"); err != nil {
		t.Fatal(err)
	}
	after := loadUsageSummaryForTest(t, store, now)
	if after.TotalTokens != before.TotalTokens ||
		after.MessageCount != before.MessageCount ||
		after.SessionCount != before.SessionCount ||
		len(after.ModelUsage) != 1 ||
		after.ModelUsage[0] != before.ModelUsage[0] {
		t.Fatalf("usage ledger changed after deletion: before=%#v after=%#v", before, after)
	}

	var usageRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM usage_records`).Scan(&usageRows); err != nil {
		t.Fatal(err)
	}
	if usageRows != 0 {
		t.Fatalf("usage_records rows after deletion = %d, want 0", usageRows)
	}
}

func TestStoreImportMessagesProjectsWithoutUsageLedger(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := NewStore(db)
	ctx := context.Background()
	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.Local)
	messages := []message.Message{
		{Role: message.RoleUser, Content: "question", EventSeq: 10},
		{Role: message.RoleAssistant, Content: "answer", EventSeq: 11},
	}

	events, err := store.ImportMessages(ctx, "fork", "source", 11, messages)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 ||
		events[0].Kind != event.KindSessionForked ||
		events[1].Kind != event.KindMessageImported ||
		events[2].Kind != event.KindMessageImported {
		t.Fatalf("import events = %#v", events)
	}
	meta, ok := events[0].Payload.(event.SessionForked)
	if !ok || meta.SourceSessionID != "source" || meta.ThroughSeq != 11 || meta.MessageCount != 2 {
		t.Fatalf("fork metadata = %#v", events[0].Payload)
	}

	history, err := store.History(ctx, "fork")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 ||
		history[0].Content != "question" ||
		history[1].Content != "answer" ||
		history[0].EventSeq != uint64(events[1].Seq) ||
		history[1].EventSeq != uint64(events[2].Seq) {
		t.Fatalf("imported history = %#v", history)
	}

	summary, err := store.UsageSummary(ctx, UsageQuery{
		RangeStart:    now.AddDate(0, 0, -6).Format(time.DateOnly),
		RangeEnd:      now.AddDate(0, 0, 1).Format(time.DateOnly),
		ActivityStart: now.AddDate(0, 0, -6).Format(time.DateOnly),
		Today:         now.Format(time.DateOnly),
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.MessageCount != 0 || summary.SessionCount != 0 || summary.TotalTokens != 0 {
		t.Fatalf("import changed usage summary = %#v", summary)
	}
}

func TestStoreRollsBackInvalidProjection(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := NewStore(db)

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

func TestStoreRejectsInactiveRewindTarget(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := NewStore(db)
	ctx := context.Background()

	target := appendStoreMessage(t, store, "session-1", message.Message{
		Role: message.RoleUser, Content: "question",
	})
	appendStoreMessage(t, store, "session-1", message.Message{
		Role: message.RoleAssistant, Content: "answer",
	})
	preview, err := store.Rewind(ctx, "session-1", target, false, 0, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Rewind(ctx, "session-1", target, true, preview.HeadSeq, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Rewind(ctx, "session-1", target, false, 0, nil, ""); !errors.Is(err, ErrActiveUserMessageNotFound) {
		t.Fatalf("inactive rewind target error = %v", err)
	}
}

func appendStoreEvent(
	t *testing.T,
	store Store,
	ev event.Event,
) event.Seq {
	t.Helper()
	seq, err := store.Append(context.Background(), ev)
	if err != nil {
		t.Fatal(err)
	}
	return seq
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

func loadUsageSummaryForTest(
	t *testing.T,
	store Store,
	now time.Time,
) UsageSummary {
	t.Helper()
	summary, err := store.UsageSummary(context.Background(), UsageQuery{
		RangeStart:    now.AddDate(0, 0, -6).Format(time.DateOnly),
		RangeEnd:      now.AddDate(0, 0, 1).Format(time.DateOnly),
		ActivityStart: now.AddDate(0, 0, -6).Format(time.DateOnly),
		Today:         now.Format(time.DateOnly),
	})
	if err != nil {
		t.Fatal(err)
	}
	return summary
}
