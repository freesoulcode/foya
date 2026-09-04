package state

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/freesoulcode/foya/internal/compaction"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
)

func TestBranchKeepsSourceEventsAndProjectsOnlyActiveHistory(t *testing.T) {
	ctx := context.Background()
	log := newSQLiteTestStore(t)
	sessionID := "session-1"

	firstUser := appendMessage(t, log, sessionID, message.Message{
		Role: message.RoleUser, Content: "first question",
	})
	appendMessage(t, log, sessionID, message.Message{
		Role: message.RoleAssistant, Content: "first answer",
	})
	secondUser := appendMessage(t, log, sessionID, message.Message{
		Role: message.RoleUser, Content: "old second question",
	})
	appendMessage(t, log, sessionID, message.Message{
		Role: message.RoleAssistant,
		ToolCalls: []message.ToolCall{{
			ID:    "edit-1",
			Name:  "edit",
			Input: json.RawMessage(`{"path":"internal/example.go","edits":[]}`),
		}},
	})
	appendMessage(t, log, sessionID, message.Message{
		Role:       message.RoleTool,
		ToolCallID: "edit-1",
		Content:    "edited",
		Diff:       "@@ -1 +1 @@\n-old\n+new",
	})
	appendMessage(t, log, sessionID, message.Message{
		Role: message.RoleAssistant, Content: "old second answer",
	})

	active, err := log.Events(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	plan, ok := compaction.BuildPlan(active, nil, false)
	if !ok {
		t.Fatal("expected checkpoint plan")
	}
	_, err = log.RecordCheckpoint(ctx, compaction.Checkpoint{
		SessionID:    sessionID,
		ThroughSeq:   plan.ThroughSeq,
		SourceDigest: plan.SourceDigest,
		Summary:      "old checkpoint",
		CreatedAt:    time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	preview, err := log.Branch(
		ctx,
		sessionID,
		secondUser,
		"edited second question",
		false,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Applied || len(preview.Effects) != 1 {
		t.Fatalf("branch preview = %#v", preview)
	}
	if preview.Effects[0].Tool != "edit" ||
		preview.Effects[0].Detail != "internal/example.go" {
		t.Fatalf("branch effects = %#v", preview.Effects)
	}
	before, _ := log.History(ctx, sessionID)
	if len(before) != 6 {
		t.Fatalf("preview changed active history: %#v", before)
	}

	committed, err := log.Branch(
		ctx,
		sessionID,
		secondUser,
		"edited second question",
		true,
		preview.HeadSeq,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !committed.Applied || committed.Event.Kind != event.KindHistoryBranched {
		t.Fatalf("committed branch = %#v", committed)
	}
	if _, ok, err := log.Checkpoint(ctx, sessionID); err != nil || ok {
		t.Fatal("branch did not invalidate the old checkpoint")
	}

	editedUser := appendMessage(t, log, sessionID, message.Message{
		Role: message.RoleUser, Content: "edited second question",
	})
	editedAssistant := appendMessage(t, log, sessionID, message.Message{
		Role: message.RoleAssistant, Content: "new second answer",
	})

	history, err := log.History(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 4 {
		t.Fatalf("active history length = %d, want 4: %#v", len(history), history)
	}
	if history[0].EventSeq != uint64(firstUser) ||
		history[2].EventSeq != uint64(editedUser) ||
		history[3].EventSeq != uint64(editedAssistant) {
		t.Fatalf("history event sequences = %#v", history)
	}
	if history[2].Content != "edited second question" ||
		history[3].Content != "new second answer" {
		t.Fatalf("active branch = %#v", history)
	}

	active, err = log.Events(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	plan, ok = compaction.BuildPlan(active, nil, false)
	if !ok {
		t.Fatal("expected post-branch checkpoint plan")
	}
	if _, err := log.RecordCheckpoint(ctx, compaction.Checkpoint{
		SessionID:    sessionID,
		ThroughSeq:   plan.ThroughSeq,
		SourceDigest: plan.SourceDigest,
		Summary:      "new checkpoint",
		CreatedAt:    time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	raw, err := log.Read(ctx, sessionID, 0)
	if err != nil {
		t.Fatal(err)
	}
	foundOld := false
	for _, ev := range raw {
		msg, ok := messageFromEvent(ev)
		if ok && msg.Content == "old second answer" {
			foundOld = true
		}
	}
	if !foundOld {
		t.Fatal("superseded branch was removed from the source log")
	}

	_, err = log.Branch(ctx, sessionID, secondUser, "another edit", true, 0)
	if !errors.Is(err, ErrActiveUserMessageNotFound) {
		t.Fatalf("inactive target error = %v", err)
	}
}

func TestBranchWithoutSideEffectsAppliesWithoutConfirmation(t *testing.T) {
	ctx := context.Background()
	log := newSQLiteTestStore(t)
	sessionID := "session-1"
	target := appendMessage(t, log, sessionID, message.Message{
		Role: message.RoleUser, Content: "question",
	})
	appendMessage(t, log, sessionID, message.Message{
		Role: message.RoleAssistant, Content: "answer",
	})

	result, err := log.Branch(ctx, sessionID, target, "edited question", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied || len(result.Effects) != 0 {
		t.Fatalf("branch = %#v", result)
	}
}

func TestBranchRejectsStaleEffectConfirmation(t *testing.T) {
	ctx := context.Background()
	log := newSQLiteTestStore(t)
	sessionID := "session-1"
	target := appendMessage(t, log, sessionID, message.Message{
		Role: message.RoleUser, Content: "run a command",
	})
	appendMessage(t, log, sessionID, message.Message{
		Role: message.RoleAssistant,
		ToolCalls: []message.ToolCall{{
			ID:    "bash-1",
			Name:  "bash",
			Input: json.RawMessage(`{"command":"touch marker"}`),
		}},
	})
	appendMessage(t, log, sessionID, message.Message{
		Role: message.RoleTool, ToolCallID: "bash-1", Content: "done",
	})

	preview, err := log.Branch(ctx, sessionID, target, "run another command", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	appendMessage(t, log, sessionID, message.Message{
		Role: message.RoleAssistant, Content: "history changed",
	})

	_, err = log.Branch(
		ctx,
		sessionID,
		target,
		"run another command",
		true,
		preview.HeadSeq,
	)
	if !errors.Is(err, ErrBranchChanged) {
		t.Fatalf("stale confirmation error = %v", err)
	}
}

func appendMessage(
	t *testing.T,
	log Store,
	sessionID string,
	msg message.Message,
) event.Seq {
	t.Helper()
	seq, err := log.Append(context.Background(), event.Event{
		Kind:    event.KindMessageEnd,
		Session: sessionID,
		Payload: msg,
		Time:    time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return seq
}
