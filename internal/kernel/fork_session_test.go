package kernel

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freesoulcode/foya/internal/artifact"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	interaction "github.com/freesoulcode/foya/internal/interaction"
	"github.com/freesoulcode/foya/internal/sandbox"
	"github.com/freesoulcode/foya/internal/tool"
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
	refs, err := be.resolveAttachments(ctx, sourceID, []conversation.AttachmentRef{{ID: ref.ID}})
	if err != nil {
		t.Fatal(err)
	}

	firstSeq := appendHistoryMessage(t, be, sourceID, conversation.Message{
		Role: conversation.RoleUser, Content: "first", Attachments: refs,
	})
	appendHistoryMessage(t, be, sourceID, conversation.Message{
		Role: conversation.RoleAssistant, Content: "answer",
	})
	target := appendHistoryMessage(t, be, sourceID, conversation.Message{
		Role: conversation.RoleUser, Content: "old route",
	})
	appendHistoryMessage(t, be, sourceID, conversation.Message{
		Role: conversation.RoleAssistant, Content: "old answer",
	})
	preview, err := be.log.Rewind(ctx, sourceID, target, false, 0, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := be.log.Rewind(ctx, sourceID, target, true, preview.HeadSeq, nil, ""); err != nil {
		t.Fatal(err)
	}
	appendHistoryMessage(t, be, sourceID, conversation.Message{
		Role: conversation.RoleUser, Content: "new route",
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
	if len(events) != 4 || events[0].Kind != conversation.KindSessionForked {
		t.Fatalf("forked events = %#v", eventKinds(events))
	}
	meta, ok := events[0].Payload.(conversation.SessionForked)
	if !ok {
		t.Fatalf("fork metadata payload = %#v", events[0].Payload)
	}
	if meta.SourceSessionID != sourceID || meta.MessageCount != 3 || meta.ThroughSeq == 0 {
		t.Fatalf("fork metadata = %#v", meta)
	}
	for _, ev := range events[1:] {
		if ev.Kind != conversation.KindMessageImported {
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

func TestForkSessionCopiesFileArtifacts(t *testing.T) {
	ctx := context.Background()
	be, sourceID, _ := newQueueTestBackend(t)
	be.mu.RLock()
	store := be.artifacts
	be.mu.RUnlock()
	ref, err := store.PutFile(ctx, sourceID, "report.html", "", bytes.NewReader([]byte("<h1>Report</h1>")))
	if err != nil {
		t.Fatal(err)
	}
	refs, err := be.resolveAttachments(ctx, sourceID, []conversation.AttachmentRef{{ID: ref.ID}})
	if err != nil {
		t.Fatal(err)
	}
	appendHistoryMessage(t, be, sourceID, conversation.Message{
		Role: conversation.RoleUser, Content: "file", Attachments: refs,
	})
	appendHistoryMessage(t, be, sourceID, conversation.Message{
		Role: conversation.RoleAssistant, Content: "answer",
	})

	forked, err := be.ForkSession(ctx, sourceID, ForkSessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	history, err := be.History(ctx, forked.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || len(history[0].Attachments) != 1 {
		t.Fatalf("forked history = %#v", history)
	}
	copied := history[0].Attachments[0]
	if copied.ID == ref.ID || copied.Kind != "file" ||
		copied.Name != "report.html" ||
		copied.MediaType != "text/html; charset=utf-8" {
		t.Fatalf("forked file artifact = %#v, source=%#v", copied, ref)
	}
	data, _, err := be.ReadArtifact(ctx, forked.ID, copied.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "<h1>Report</h1>" {
		t.Fatalf("forked file data = %q", data)
	}
	if err := be.DeleteArtifact(ctx, forked.ID, copied.ID); !errors.Is(err, artifact.ErrCommitted) {
		t.Fatalf("delete forked file artifact error = %v", err)
	}
}

func TestForkSessionCopiesManagedWorkspace(t *testing.T) {
	ctx := context.Background()
	be, sourceID, _ := newQueueTestBackend(t)
	be.mu.RLock()
	store := be.artifacts
	be.mu.RUnlock()
	sourceWorkspace, err := store.WorkspaceDir(ctx, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sourceWorkspace, "notes"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceWorkspace, "notes", "report.md"), []byte("# Report"), 0o600); err != nil {
		t.Fatal(err)
	}
	appendHistoryMessage(t, be, sourceID, conversation.Message{
		Role: conversation.RoleUser, Content: "write report",
	})
	appendHistoryMessage(t, be, sourceID, conversation.Message{
		Role: conversation.RoleAssistant, Content: "done",
	})

	forked, err := be.ForkSession(ctx, sourceID, ForkSessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	targetWorkspace, err := store.WorkspaceDir(ctx, forked.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(targetWorkspace, "notes", "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# Report" {
		t.Fatalf("forked workspace data = %q", data)
	}
	readCtx := tool.WithCWD(context.Background(), targetWorkspace)
	readResult, err := tool.NewReadTool(be.approval).Run(readCtx, tool.Call{
		Input: []byte(`{"path":"notes/report.md"}`),
	})
	if err != nil || readResult.IsError || !strings.Contains(readResult.Content[0].Text, "# Report") {
		t.Fatalf("forked workspace read result = %#v, err = %v", readResult, err)
	}

	editCtx := context.Background()
	editCtx = tool.WithCWD(editCtx, targetWorkspace)
	editCtx = tool.WithManagedWorkspace(editCtx, true)
	editCtx = tool.WithSessionID(editCtx, forked.ID)
	editCtx = interaction.WithMode(editCtx, interaction.ModeFullAccess)
	editResult, err := tool.NewEditTool(be.approval, directWriteRunner{}, store).Run(editCtx, tool.Call{
		Input: []byte(`{"path":"notes/report.md","edits":[{"old_text":"# Report","new_text":"# Forked Report"}]}`),
	})
	if err != nil || editResult.IsError {
		t.Fatalf("forked workspace edit result = %#v, err = %v", editResult, err)
	}
	data, err = os.ReadFile(filepath.Join(targetWorkspace, "notes", "report.md"))
	if err != nil || string(data) != "# Forked Report" {
		t.Fatalf("edited forked workspace data = %q, err = %v", data, err)
	}
}

func TestForkSessionThroughMiddleCopiesCurrentManagedWorkspace(t *testing.T) {
	ctx := context.Background()
	be, sourceID, _ := newQueueTestBackend(t)
	be.mu.RLock()
	store := be.artifacts
	be.mu.RUnlock()
	sourceWorkspace, err := store.WorkspaceDir(ctx, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	appendHistoryMessage(t, be, sourceID, conversation.Message{
		Role: conversation.RoleUser, Content: "make report",
	})
	appendHistoryMessage(t, be, sourceID, conversation.Message{
		Role: conversation.RoleAssistant,
		ToolCalls: []conversation.ToolCall{{
			ID:    "bash-1",
			Name:  "bash",
			Input: json.RawMessage(`{"command":"printf '# Draft' > notes/report.md"}`),
		}},
	})
	appendHistoryMessage(t, be, sourceID, conversation.Message{
		Role:       conversation.RoleTool,
		ToolCallID: "bash-1",
		Content:    "ok",
	})
	through := appendHistoryMessage(t, be, sourceID, conversation.Message{
		Role: conversation.RoleAssistant, Content: "done",
	})
	if err := os.MkdirAll(filepath.Join(sourceWorkspace, "notes"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceWorkspace, "notes", "report.md"), []byte("# Future"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceWorkspace, "notes", "bash.txt"), []byte("created by bash"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceWorkspace, "notes", "deleted.md"), []byte("current file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceWorkspace, "future.md"), []byte("future"), 0o600); err != nil {
		t.Fatal(err)
	}
	appendHistoryMessage(t, be, sourceID, conversation.Message{
		Role: conversation.RoleUser, Content: "later request",
	})
	appendHistoryMessage(t, be, sourceID, conversation.Message{
		Role: conversation.RoleAssistant, Content: "later answer",
	})

	forked, err := be.ForkSession(ctx, sourceID, ForkSessionOptions{ThroughSeq: through})
	if err != nil {
		t.Fatal(err)
	}
	history, err := be.History(ctx, forked.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := messageContents(history); len(got) != 4 ||
		got[0] != "make report" ||
		got[1] != "" ||
		got[2] != "ok" ||
		got[3] != "done" {
		t.Fatalf("forked middle history = %#v", history)
	}
	targetWorkspace, err := store.WorkspaceDir(ctx, forked.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(targetWorkspace, "notes", "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# Future" {
		t.Fatalf("partial fork copied report = %q", data)
	}
	data, err = os.ReadFile(filepath.Join(targetWorkspace, "notes", "bash.txt"))
	if err != nil || string(data) != "created by bash" {
		t.Fatalf("partial fork copied bash side effect = %q, err = %v", data, err)
	}
	data, err = os.ReadFile(filepath.Join(targetWorkspace, "notes", "deleted.md"))
	if err != nil || string(data) != "current file" {
		t.Fatalf("partial fork copied current deleted.md state = %q, err = %v", data, err)
	}
	data, err = os.ReadFile(filepath.Join(targetWorkspace, "future.md"))
	if err != nil || string(data) != "future" {
		t.Fatalf("partial fork copied future workspace file = %q, err = %v", data, err)
	}
}

type directWriteRunner struct{}

func (directWriteRunner) Run(
	_ context.Context,
	request sandbox.ExecRequest,
	_ sandbox.Profile,
) (sandbox.ExecResult, error) {
	if len(request.Argv) == 0 {
		return sandbox.ExecResult{}, errors.New("missing target")
	}
	target := request.Argv[len(request.Argv)-1]
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return sandbox.ExecResult{}, err
	}
	return sandbox.ExecResult{}, os.WriteFile(target, request.Stdin, 0o644)
}

func (directWriteRunner) Kind() sandbox.Kind { return sandbox.KindNone }

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
	awaitEventSnapshot(t, be, sourceID, func(events []conversation.Event) bool {
		return lastEvent(events, conversation.KindTurnComplete) != nil
	})
}

func TestForkSessionRejectsUnknownArtifactKind(t *testing.T) {
	ctx := context.Background()
	be, sourceID, _ := newQueueTestBackend(t)
	be.mu.RLock()
	store := be.artifacts
	be.mu.RUnlock()
	ref, err := store.PutFile(ctx, sourceID, "custom.bin", "application/octet-stream", bytes.NewReader([]byte("custom")))
	if err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(be.dataDir, "artifacts", sourceID, ref.ID+".json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatal(err)
	}
	meta["kind"] = "custom"
	updated, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metaPath, updated, 0o600); err != nil {
		t.Fatal(err)
	}
	appendHistoryMessage(t, be, sourceID, conversation.Message{
		Role:        conversation.RoleUser,
		Content:     "custom artifact",
		Attachments: []conversation.AttachmentRef{ref},
	})

	_, err = be.ForkSession(ctx, sourceID, ForkSessionOptions{})
	if err == nil || !strings.Contains(err.Error(), "unsupported fork artifact kind") {
		t.Fatalf("fork unknown artifact error = %v", err)
	}
}

func TestForkSessionThroughMiddleMessage(t *testing.T) {
	ctx := context.Background()
	be, sourceID, _ := newQueueTestBackend(t)
	appendHistoryMessage(t, be, sourceID, conversation.Message{
		Role: conversation.RoleUser, Content: "round 1 user",
	})
	through := appendHistoryMessage(t, be, sourceID, conversation.Message{
		Role: conversation.RoleAssistant, Content: "round 1 assistant",
	})
	appendHistoryMessage(t, be, sourceID, conversation.Message{
		Role: conversation.RoleUser, Content: "round 2 user",
	})
	appendHistoryMessage(t, be, sourceID, conversation.Message{
		Role: conversation.RoleAssistant, Content: "round 2 assistant",
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
	meta, ok := events[0].Payload.(conversation.SessionForked)
	if !ok || meta.ThroughSeq != through || meta.MessageCount != 2 {
		t.Fatalf("fork metadata = %#v", events[0].Payload)
	}
}

func TestForkSessionRejectsUserThroughSeq(t *testing.T) {
	ctx := context.Background()
	be, sourceID, _ := newQueueTestBackend(t)
	through := appendHistoryMessage(t, be, sourceID, conversation.Message{
		Role: conversation.RoleUser, Content: "round 1 user",
	})
	appendHistoryMessage(t, be, sourceID, conversation.Message{
		Role: conversation.RoleAssistant, Content: "round 1 assistant",
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
	target := appendHistoryMessage(t, be, sourceID, conversation.Message{
		Role: conversation.RoleUser, Content: "old route",
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

func TestForkSessionSideChatCreatesTemporarySession(t *testing.T) {
	ctx := context.Background()
	be, sourceID, _ := newQueueTestBackend(t)
	appendHistoryMessage(t, be, sourceID, conversation.Message{
		Role: conversation.RoleUser, Content: "inspect state",
	})
	appendHistoryMessage(t, be, sourceID, conversation.Message{
		Role: conversation.RoleAssistant, Content: "done",
	})

	side, err := be.ForkSession(ctx, sourceID, ForkSessionOptions{SideChat: true})
	if err != nil {
		t.Fatal(err)
	}
	if !side.Temporary || side.ParentID != "" {
		t.Fatalf("side chat session metadata = %#v", side)
	}
	if side.Title != "Side chat" {
		t.Fatalf("side chat title = %q", side.Title)
	}
	roots := be.ListSessions()
	if len(roots) != 1 || roots[0].ID != sourceID {
		t.Fatalf("root sessions = %#v", roots)
	}
	history, err := be.History(ctx, side.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 0 {
		t.Fatalf("temporary side chat history = %#v", history)
	}
}

func TestForkSessionSideChatAllowsBusySource(t *testing.T) {
	ctx := context.Background()
	be, sourceID, prov := newQueueTestBackend(t)
	if _, err := be.SubmitTurn(ctx, sourceID, "slow"); err != nil {
		t.Fatal(err)
	}
	awaitStarted(t, prov, "slow")

	side, err := be.ForkSession(ctx, sourceID, ForkSessionOptions{SideChat: true})
	if err != nil {
		t.Fatal(err)
	}
	if !side.Temporary || side.ParentID != "" {
		t.Fatalf("side chat session metadata = %#v", side)
	}
	history, err := be.History(ctx, side.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 0 {
		t.Fatalf("temporary side chat history = %#v", history)
	}

	be.CancelTurn(sourceID)
	awaitEventSnapshot(t, be, sourceID, func(events []conversation.Event) bool {
		return lastEvent(events, conversation.KindTurnComplete) != nil
	})
}

func messageContents(messages []conversation.Message) []string {
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
