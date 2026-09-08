package conversation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	model "github.com/freesoulcode/foya/internal/model"
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

	messageSeq := appendStoreMessage(t, store, "session-1", Message{
		Role: RoleUser, Content: "persist me",
	})
	usage := model.Usage{
		Model:        "model-a",
		InputTokens:  120,
		OutputTokens: 30,
		TotalTokens:  150,
		CachedTokens: 20,
	}
	usageSeq, err := store.Append(ctx, Event{
		Kind: KindUsageUpdated, Session: "session-1",
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
	restoredUsage, ok := events[0].Payload.(model.Usage)
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
	ref := AttachmentRef{
		ID: "artifact-1", Name: "screen.png", Kind: "image",
		MediaType: "image/png", Bytes: 123, Width: 10, Height: 8,
		SHA256: "checksum",
	}
	appendStoreMessage(t, store, "session-1", Message{
		Role: RoleUser, Content: "describe",
		Attachments: []AttachmentRef{ref},
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
	change := FileChange{
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

	sourceSeq := appendStoreMessage(t, store, "session-1", Message{
		Role:       RoleTool,
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
	target := appendStoreEvent(t, store, Event{
		Kind:    KindMessageEnd,
		Session: sessionID,
		Time:    oldTime,
		Payload: Message{Role: RoleUser, Content: "old request"},
	})
	before := []byte("before\n")
	after := []byte("after\n")
	appendStoreEvent(t, store, Event{
		Kind:    KindMessageEnd,
		Session: sessionID,
		Time:    oldTime,
		Payload: Message{
			Role: RoleTool,
			FileChange: &FileChange{
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
		appendStoreMessage(t, store, sessionID, Message{
			Role:    RoleUser,
			Content: fmt.Sprintf("request-%d", index),
		})
		appendStoreMessage(t, store, sessionID, Message{
			Role: RoleTool,
			FileChange: &FileChange{
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

	appendStoreMessage(t, store, sessionID, Message{
		Role: RoleUser, Content: "first",
	})
	appendStoreMessage(t, store, sessionID, Message{
		Role: RoleAssistant, Content: "answer",
	})
	target := appendStoreMessage(t, store, sessionID, Message{
		Role: RoleUser, Content: "old question",
	})
	appendStoreMessage(t, store, sessionID, Message{
		Role: RoleAssistant, Content: "old answer",
	})

	active, err := store.Events(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	plan, ok := BuildPlanForPhase(
		active,
		nil,
		PhaseStandalone,
	)
	if !ok {
		t.Fatal("expected compaction plan")
	}
	checkpoint := checkpointForStorePlan(plan, sessionID, "summary")
	if _, err := store.RecordCheckpoint(ctx, checkpoint); err != nil {
		t.Fatal(err)
	}
	modelHistory, err := store.ModelHistory(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(modelHistory) != 1 || modelHistory[0].Role != RoleSystem {
		t.Fatalf("model history = %#v", modelHistory)
	}
	boundary := AcceptedBoundary{
		SessionID:    sessionID,
		Route:        "connection\x00provider\x00model",
		ThroughSeq:   active[len(active)-1].Seq,
		InputTokens:  100,
		OutputTokens: 10,
		PayloadUnits: 400,
		CreatedAt:    time.Now(),
	}
	if _, err := store.Append(ctx, Event{
		Kind: KindContextRequestAccepted, Session: sessionID,
		Payload: boundary, Time: boundary.CreatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := store.AcceptedBoundary(
		ctx,
		sessionID,
		boundary.Route,
	); err != nil || !ok || got.ThroughSeq != boundary.ThroughSeq ||
		got.PayloadUnits != boundary.PayloadUnits {
		t.Fatalf("accepted boundary before rewind: %#v ok=%v err=%v", got, ok, err)
	}
	secondRoute := boundary
	secondRoute.Route = "connection-2\x00provider\x00model"
	if _, err := store.Append(ctx, Event{
		Kind: KindContextRequestAccepted, Session: sessionID,
		Payload: secondRoute, Time: secondRoute.CreatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.AcceptedBoundary(
		ctx,
		sessionID,
		boundary.Route,
	); err != nil || !ok {
		t.Fatalf("first route boundary was overwritten: ok=%v err=%v", ok, err)
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
	if _, ok, err := store.AcceptedBoundary(
		ctx,
		sessionID,
		boundary.Route,
	); err != nil || ok {
		t.Fatalf("accepted boundary after rewind: ok=%v err=%v", ok, err)
	}
	if _, ok, err := store.AcceptedBoundary(
		ctx,
		sessionID,
		secondRoute.Route,
	); err != nil || ok {
		t.Fatalf("second accepted boundary after rewind: ok=%v err=%v", ok, err)
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

func TestStoreRecoversCorruptCheckpointProjectionFromEvent(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := NewStore(db)
	ctx := context.Background()
	sessionID := "session-1"

	appendStoreMessage(t, store, sessionID, Message{
		Role: RoleUser, Content: "question",
	})
	appendStoreMessage(t, store, sessionID, Message{
		Role: RoleAssistant, Content: "answer",
	})
	active, err := store.Events(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	plan, ok := BuildPlanForPhase(
		active,
		nil,
		PhaseStandalone,
	)
	if !ok {
		t.Fatal("expected compaction plan")
	}
	checkpoint := Checkpoint{
		SchemaVersion:        CheckpointSchemaVersion,
		SourcePolicyVersion:  SourcePolicyVersion,
		SummaryFormatVersion: SummaryFormatVersion,
		PromptVersion:        PromptVersion,
		SessionID:            sessionID,
		Phase:                PhaseStandalone,
		ThroughSeq:           plan.ThroughSeq,
		SourceDigest:         plan.SourceDigest,
		ProjectionKind:       ProjectionText,
		Level:                CheckpointLevelSegmented,
		Segments:             ConsolidatedSegment(plan, "recovered summary"),
		Summary:              "recovered summary",
		Model:                "test-model",
		CreatedAt:            time.Now(),
	}
	checkpoint.CheckpointID = ComputeCheckpointID(checkpoint)
	if _, err := store.RecordCheckpoint(ctx, checkpoint); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		UPDATE compaction_checkpoints
		SET checkpoint_json = '{'
		WHERE session_id = ?
	`, sessionID); err != nil {
		t.Fatal(err)
	}

	history, err := store.ModelHistory(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 ||
		!bytes.Contains([]byte(history[0].Content), []byte("recovered summary")) {
		t.Fatalf("recovered model history = %#v", history)
	}
	var repaired []byte
	if err := db.QueryRow(`
		SELECT checkpoint_json
		FROM compaction_checkpoints
		WHERE session_id = ?
	`, sessionID).Scan(&repaired); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(repaired) {
		t.Fatalf("checkpoint projection was not repaired: %q", repaired)
	}
}

func TestStoreRejectsStaleCheckpointLineage(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := NewStore(db)
	ctx := context.Background()
	sessionID := "session-1"

	appendStoreMessage(t, store, sessionID, Message{
		Role: RoleUser, Content: "first question",
	})
	appendStoreMessage(t, store, sessionID, Message{
		Role: RoleAssistant, Content: "first answer",
	})
	active, err := store.Events(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	firstPlan, ok := BuildPlanForPhase(
		active,
		nil,
		PhaseStandalone,
	)
	if !ok {
		t.Fatal("expected first plan")
	}
	first := Checkpoint{
		SchemaVersion:        CheckpointSchemaVersion,
		SourcePolicyVersion:  SourcePolicyVersion,
		SummaryFormatVersion: SummaryFormatVersion,
		PromptVersion:        PromptVersion,
		SessionID:            sessionID,
		Phase:                PhaseStandalone,
		ThroughSeq:           firstPlan.ThroughSeq,
		SourceDigest:         firstPlan.SourceDigest,
		ProjectionKind:       ProjectionText,
		Level:                CheckpointLevelSegmented,
		Segments:             ConsolidatedSegment(firstPlan, "first summary"),
		Summary:              "first summary",
		Model:                "test-model",
		CreatedAt:            time.Now(),
	}
	first.CheckpointID = ComputeCheckpointID(first)
	if _, err := store.RecordCheckpoint(ctx, first); err != nil {
		t.Fatal(err)
	}

	appendStoreMessage(t, store, sessionID, Message{
		Role: RoleUser, Content: "second question",
	})
	appendStoreMessage(t, store, sessionID, Message{
		Role: RoleAssistant, Content: "second answer",
	})
	active, err = store.Events(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	secondPlan, ok := BuildPlanForPhase(
		active,
		&first,
		PhaseStandalone,
	)
	if !ok {
		t.Fatal("expected successor plan")
	}
	stale := Checkpoint{
		SchemaVersion:         CheckpointSchemaVersion,
		SourcePolicyVersion:   SourcePolicyVersion,
		SummaryFormatVersion:  SummaryFormatVersion,
		PromptVersion:         PromptVersion,
		PreviousCheckpointID:  "stale-parent",
		SessionID:             sessionID,
		Phase:                 PhaseStandalone,
		ThroughSeq:            secondPlan.ThroughSeq,
		SourceDigest:          secondPlan.SourceDigest,
		ProjectionKind:        ProjectionText,
		Level:                 CheckpointLevelSession,
		Segments:              ConsolidatedSegment(secondPlan, "stale summary"),
		Summary:               "stale summary",
		Model:                 "test-model",
		EstimatedTokensBefore: secondPlan.EstimatedTokens,
		CreatedAt:             time.Now(),
	}
	stale.CheckpointID = ComputeCheckpointID(stale)
	if _, err := store.RecordCheckpoint(ctx, stale); err == nil {
		t.Fatal("expected stale checkpoint lineage to be rejected")
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

	first := appendStoreMessage(t, store, "deleted", Message{
		Role: RoleUser, Content: "remove",
	})
	if err := store.Delete(ctx, "deleted"); err != nil {
		t.Fatal(err)
	}
	late, err := store.Append(ctx, Event{
		Kind: KindError, Session: "deleted",
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
	next, err := store.Append(ctx, Event{
		Kind: KindError, Session: "kept",
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

	appendStoreEvent(t, store, Event{
		Kind: KindMessageEnd, Session: "session-1", Time: now,
		Payload: Message{Role: RoleUser, Content: "hello"},
	})
	appendStoreEvent(t, store, Event{
		Kind: KindUsageUpdated, Session: "session-1", Time: now,
		Payload: model.Usage{
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
	messages := []Message{
		{Role: RoleUser, Content: "question", EventSeq: 10},
		{Role: RoleAssistant, Content: "answer", EventSeq: 11},
	}

	events, err := store.ImportMessages(ctx, "fork", "source", 11, messages)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 ||
		events[0].Kind != KindSessionForked ||
		events[1].Kind != KindMessageImported ||
		events[2].Kind != KindMessageImported {
		t.Fatalf("import events = %#v", events)
	}
	meta, ok := events[0].Payload.(SessionForked)
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

	_, err = store.Append(context.Background(), Event{
		Kind: KindMessageEnd, Session: "session-1",
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

	target := appendStoreMessage(t, store, "session-1", Message{
		Role: RoleUser, Content: "question",
	})
	appendStoreMessage(t, store, "session-1", Message{
		Role: RoleAssistant, Content: "answer",
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
	ev Event,
) Seq {
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
	item Message,
) Seq {
	t.Helper()
	seq, err := store.Append(context.Background(), Event{
		Kind: KindMessageEnd, Session: sessionID,
		Time: time.Now(), Payload: item,
	})
	if err != nil {
		t.Fatal(err)
	}
	return seq
}

func checkpointForStorePlan(
	plan Plan,
	sessionID, summary string,
) Checkpoint {
	checkpoint := Checkpoint{
		SchemaVersion:        CheckpointSchemaVersion,
		SourcePolicyVersion:  SourcePolicyVersion,
		SummaryFormatVersion: SummaryFormatVersion,
		PromptVersion:        PromptVersion,
		SessionID:            sessionID,
		Phase:                plan.Phase,
		HeadAnchorSeq:        plan.HeadAnchorSeq,
		ThroughSeq:           plan.ThroughSeq,
		SourceDigest:         plan.SourceDigest,
		ProjectionKind:       ProjectionText,
		Level:                CheckpointLevelSegmented,
		Segments:             ConsolidatedSegment(plan, summary),
		Summary:              summary,
		Model:                "test-model",
		CreatedAt:            time.Now(),
	}
	checkpoint.CheckpointID = ComputeCheckpointID(checkpoint)
	return checkpoint
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
