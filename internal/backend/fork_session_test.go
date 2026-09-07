package backend

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
)

func TestForkSessionCopiesActiveHistoryAndArtifactsWithoutUsage(t *testing.T) {
	ctx := context.Background()
	be, sourceID, _ := newQueueTestBackend(t)
	imageData, err := base64.StdEncoding.DecodeString(
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
	)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := be.PutImage(ctx, sourceID, "pixel.png", bytes.NewReader(imageData))
	if err != nil {
		t.Fatal(err)
	}
	refs, err := be.resolveAttachments(ctx, sourceID, []message.AttachmentRef{{ID: ref.ID}})
	if err != nil {
		t.Fatal(err)
	}

	firstSeq := appendHistoryMessage(t, be, sourceID, message.Message{
		Role: message.RoleUser, Content: "first", Attachments: refs,
	})
	appendHistoryMessage(t, be, sourceID, message.Message{
		Role: message.RoleAssistant, Content: "answer",
	})
	target := appendHistoryMessage(t, be, sourceID, message.Message{
		Role: message.RoleUser, Content: "old route",
	})
	appendHistoryMessage(t, be, sourceID, message.Message{
		Role: message.RoleAssistant, Content: "old answer",
	})
	preview, err := be.log.Rewind(ctx, sourceID, target, false, 0, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := be.log.Rewind(ctx, sourceID, target, true, preview.HeadSeq, nil, ""); err != nil {
		t.Fatal(err)
	}
	appendHistoryMessage(t, be, sourceID, message.Message{
		Role: message.RoleUser, Content: "new route",
	})

	before, err := be.UsageStatistics(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	forked, err := be.ForkSession(ctx, sourceID, ForkSessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if forked.ID == sourceID {
		t.Fatal("fork reused source session id")
	}
	if forked.Title != "New chat copy" {
		t.Fatalf("fork title = %q", forked.Title)
	}
	if forked.ConnectionID == "" || forked.Model != "test-model" || forked.ApprovalMode != "manual" {
		t.Fatalf("forked session did not inherit runtime config: %#v", forked)
	}

	history, err := be.History(ctx, forked.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := messageContents(history); len(got) != 3 ||
		got[0] != "first" ||
		got[1] != "answer" ||
		got[2] != "new route" {
		t.Fatalf("forked history = %#v", history)
	}
	if history[0].EventSeq == uint64(firstSeq) {
		t.Fatalf("forked history reused source event sequence: %#v", history[0])
	}
	if len(history[0].Attachments) != 1 {
		t.Fatalf("forked attachments = %#v", history[0].Attachments)
	}
	if history[0].Attachments[0].ID == ref.ID {
		t.Fatalf("forked attachment reused source artifact id %q", ref.ID)
	}
	forkedData, _, err := be.ReadArtifact(ctx, forked.ID, history[0].Attachments[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	sourceData, _, err := be.ReadArtifact(ctx, sourceID, ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(forkedData, sourceData) {
		t.Fatal("forked artifact bytes differ from source")
	}

	events, err := be.log.Read(ctx, forked.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 || events[0].Kind != event.KindSessionForked {
		t.Fatalf("forked events = %#v", eventKinds(events))
	}
	meta, ok := events[0].Payload.(event.SessionForked)
	if !ok {
		t.Fatalf("fork metadata payload = %#v", events[0].Payload)
	}
	if meta.SourceSessionID != sourceID || meta.MessageCount != 3 || meta.ThroughSeq == 0 {
		t.Fatalf("fork metadata = %#v", meta)
	}
	for _, ev := range events[1:] {
		if ev.Kind != event.KindMessageImported {
			t.Fatalf("imported event kind = %s", ev.Kind)
		}
	}

	after, err := be.UsageStatistics(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if after.MessageCount != before.MessageCount ||
		after.SessionCount != before.SessionCount ||
		after.TotalTokens != before.TotalTokens {
		t.Fatalf("fork changed usage statistics: before=%#v after=%#v", before, after)
	}
}

func TestForkSessionRejectsBusySession(t *testing.T) {
	ctx := context.Background()
	be, sourceID, prov := newQueueTestBackend(t)
	if _, err := be.SubmitTurn(ctx, sourceID, "slow"); err != nil {
		t.Fatal(err)
	}
	awaitStarted(t, prov, "slow")

	if _, err := be.ForkSession(ctx, sourceID, ForkSessionOptions{}); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("busy fork error = %v", err)
	}
	be.CancelTurn(sourceID)
	awaitEventSnapshot(t, be, sourceID, func(events []event.Event) bool {
		return lastEvent(events, event.KindTurnComplete) != nil
	})
}

func TestForkSessionThroughMiddleMessage(t *testing.T) {
	ctx := context.Background()
	be, sourceID, _ := newQueueTestBackend(t)
	appendHistoryMessage(t, be, sourceID, message.Message{
		Role: message.RoleUser, Content: "round 1 user",
	})
	through := appendHistoryMessage(t, be, sourceID, message.Message{
		Role: message.RoleAssistant, Content: "round 1 assistant",
	})
	appendHistoryMessage(t, be, sourceID, message.Message{
		Role: message.RoleUser, Content: "round 2 user",
	})
	appendHistoryMessage(t, be, sourceID, message.Message{
		Role: message.RoleAssistant, Content: "round 2 assistant",
	})

	forked, err := be.ForkSession(ctx, sourceID, ForkSessionOptions{
		ThroughSeq: through,
	})
	if err != nil {
		t.Fatal(err)
	}
	history, err := be.History(ctx, forked.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := messageContents(history); len(got) != 2 ||
		got[0] != "round 1 user" ||
		got[1] != "round 1 assistant" {
		t.Fatalf("forked middle history = %#v", history)
	}

	events, err := be.log.Read(ctx, forked.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := events[0].Payload.(event.SessionForked)
	if !ok || meta.ThroughSeq != through || meta.MessageCount != 2 {
		t.Fatalf("fork metadata = %#v", events[0].Payload)
	}
}

func TestForkSessionRejectsUserThroughSeq(t *testing.T) {
	ctx := context.Background()
	be, sourceID, _ := newQueueTestBackend(t)
	through := appendHistoryMessage(t, be, sourceID, message.Message{
		Role: message.RoleUser, Content: "round 1 user",
	})
	appendHistoryMessage(t, be, sourceID, message.Message{
		Role: message.RoleAssistant, Content: "round 1 assistant",
	})

	if _, err := be.ForkSession(ctx, sourceID, ForkSessionOptions{
		ThroughSeq: through,
	}); !errors.Is(err, ErrInvalidForkBoundary) {
		t.Fatalf("user message fork error = %v", err)
	}
}

func TestForkSessionRejectsInactiveThroughSeq(t *testing.T) {
	ctx := context.Background()
	be, sourceID, _ := newQueueTestBackend(t)
	target := appendHistoryMessage(t, be, sourceID, message.Message{
		Role: message.RoleUser, Content: "old route",
	})
	preview, err := be.log.Rewind(ctx, sourceID, target, false, 0, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := be.log.Rewind(ctx, sourceID, target, true, preview.HeadSeq, nil, ""); err != nil {
		t.Fatal(err)
	}

	if _, err := be.ForkSession(ctx, sourceID, ForkSessionOptions{
		ThroughSeq: target,
	}); !errors.Is(err, ErrActiveMessageNotFound) {
		t.Fatalf("inactive through seq error = %v", err)
	}
}

func TestForkSessionCopiesCustomTitle(t *testing.T) {
	ctx := context.Background()
	be, sourceID, _ := newQueueTestBackend(t)
	if _, err := be.RenameSession(ctx, sourceID, "源会话"); err != nil {
		t.Fatal(err)
	}

	forked, err := be.ForkSession(ctx, sourceID, ForkSessionOptions{Title: "实验路线"})
	if err != nil {
		t.Fatal(err)
	}
	if forked.Title != "实验路线" || !forked.TitleIsManual {
		t.Fatalf("fork title = %#v", forked)
	}
}

func messageContents(messages []message.Message) []string {
	out := make([]string, 0, len(messages))
	for _, item := range messages {
		out = append(out, item.Content)
	}
	return out
}

func TestForkSessionRejectsQueuedSession(t *testing.T) {
	be, sourceID, _ := newQueueTestBackend(t)
	if _, err := be.EnqueueMessage(context.Background(), sourceID, "later"); err != nil {
		t.Fatal(err)
	}
	if _, err := be.ForkSession(context.Background(), sourceID, ForkSessionOptions{}); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("queued fork error = %v", err)
	}
}
