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

func TestRewindKeepsSourceEventsAndProjectsOnlyActivePrefix(t *testing.T) {
	ctx := context.Background()
	log := newTestStore(t)
	sessionID := "session-1"

	firstUser := appendMessage(t, log, sessionID, message.Message{
		Role: message.RoleUser, Content: "first question",
	})
	appendMessage(t, log, sessionID, message.Message{
		Role: message.RoleAssistant, Content: "first answer",
	})
	target := appendMessage(t, log, sessionID, message.Message{
		Role: message.RoleUser, Content: "old second question",
	})
	appendMessage(t, log, sessionID, message.Message{
		Role: message.RoleAssistant,
		ToolCalls: []message.ToolCall{{
			ID:    "write-1",
			Name:  "write",
			Input: json.RawMessage(`{"path":"cmd/main.go","content":"new"}`),
		}},
	})
	appendMessage(t, log, sessionID, message.Message{
		Role:       message.RoleTool,
		ToolCallID: "write-1",
		Content:    "written",
		Diff:       "--- /workspace/cmd/main.go\n+++ /workspace/cmd/main.go\n@@ -0,0 +1,1 @@\n+new\n",
		FileChange: &message.FileChange{
			Path:            "/workspace/cmd/main.go",
			AfterMode:       0o644,
			AfterBlob:       fileBlobHash([]byte("new")),
			AfterContent:    []byte("new"),
			ContentCaptured: true,
		},
	})
	appendMessage(t, log, sessionID, message.Message{
		Role: message.RoleAssistant, Content: "old second answer",
	})

	active, err := log.Events(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	plan, ok := compaction.BuildPlanForPhase(
		active,
		nil,
		compaction.PhaseStandalone,
	)
	if !ok {
		t.Fatal("expected checkpoint plan")
	}
	checkpoint := checkpointForStorePlan(plan, sessionID, "old checkpoint")
	if _, err := log.RecordCheckpoint(ctx, checkpoint); err != nil {
		t.Fatal(err)
	}

	preview, err := log.Rewind(ctx, sessionID, target, false, 0, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Applied ||
		preview.Message.Content != "old second question" ||
		len(preview.FileChanges) != 1 {
		t.Fatalf("rewind preview = %#v", preview)
	}
	before, err := log.History(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 6 {
		t.Fatalf("preview changed active history: %#v", before)
	}

	committed, err := log.Rewind(ctx, sessionID, target, true, preview.HeadSeq, []event.RewindFileResult{{
		Path:   "/workspace/cmd/main.go",
		Action: "restored",
	}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !committed.Applied ||
		committed.Event.Kind != event.KindHistoryRewound ||
		committed.Message.Content != "old second question" {
		t.Fatalf("committed rewind = %#v", committed)
	}
	payload, ok := committed.Event.Payload.(event.HistoryRewound)
	if !ok || len(payload.Files) != 1 ||
		payload.Files[0].Path != "/workspace/cmd/main.go" ||
		payload.Files[0].Action != "restored" {
		t.Fatalf("rewind payload = %#v", committed.Event.Payload)
	}
	if _, ok, err := log.Checkpoint(ctx, sessionID); err != nil || ok {
		t.Fatalf("checkpoint after rewind: ok=%v err=%v", ok, err)
	}

	history, err := log.History(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 ||
		history[0].EventSeq != uint64(firstUser) ||
		history[0].Content != "first question" ||
		history[1].Content != "first answer" {
		t.Fatalf("active history = %#v", history)
	}

	raw, err := log.Read(ctx, sessionID, 0)
	if err != nil {
		t.Fatal(err)
	}
	foundSuperseded := false
	for _, ev := range raw {
		msg, ok := messageFromEvent(ev)
		if ok && msg.Content == "old second answer" {
			foundSuperseded = true
		}
	}
	if !foundSuperseded {
		t.Fatal("rewind removed an immutable source event")
	}

	_, err = log.Rewind(ctx, sessionID, target, false, 0, nil, "")
	if !errors.Is(err, ErrActiveUserMessageNotFound) {
		t.Fatalf("inactive rewind target error = %v", err)
	}
}

func TestRewindRejectsStaleConfirmation(t *testing.T) {
	ctx := context.Background()
	log := newTestStore(t)
	sessionID := "session-1"
	target := appendMessage(t, log, sessionID, message.Message{
		Role: message.RoleUser, Content: "old request",
	})
	appendMessage(t, log, sessionID, message.Message{
		Role: message.RoleAssistant, Content: "old answer",
	})

	preview, err := log.Rewind(ctx, sessionID, target, false, 0, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	appendMessage(t, log, sessionID, message.Message{
		Role: message.RoleAssistant, Content: "history changed",
	})

	_, err = log.Rewind(ctx, sessionID, target, true, preview.HeadSeq, nil, "")
	if !errors.Is(err, ErrHistoryChanged) {
		t.Fatalf("stale rewind confirmation error = %v", err)
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
