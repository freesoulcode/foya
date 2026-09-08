package kernel

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/artifact"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/config"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	interaction "github.com/freesoulcode/foya/internal/interaction"

	model "github.com/freesoulcode/foya/internal/model"

	"github.com/freesoulcode/foya/internal/skill"
	"github.com/freesoulcode/foya/internal/terminal"
	"github.com/freesoulcode/foya/internal/tool"
)

type controlledProvider struct {
	started  chan string
	releases chan struct{}
}

func newControlledProvider() *controlledProvider {
	return &controlledProvider{
		started:  make(chan string, 8),
		releases: make(chan struct{}, 8),
	}
}

func (p *controlledProvider) Name() string { return "controlled" }

func (p *controlledProvider) Stream(ctx context.Context, req model.Request) (<-chan model.StreamEvent, error) {
	var text string
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == conversation.RoleUser {
			for _, part := range req.Messages[i].Parts {
				if part.Type == "text" {
					text += part.Text
				}
			}
			break
		}
	}
	p.started <- text

	out := make(chan model.StreamEvent, 1)
	go func() {
		defer close(out)
		select {
		case <-ctx.Done():
		case <-p.releases:
			out <- model.StreamEvent{Type: "done", FinishReason: "stop"}
		}
	}()
	return out, nil
}

func newQueueTestBackend(t *testing.T) (*Service, string, *controlledProvider) {
	t.Helper()
	sessions := newTestSessionManager(t)
	log := newTestStore(t)
	bus := broker.New[conversation.Event]()
	gateway := interaction.NewGateway(bus, log)
	prov := newControlledProvider()
	engine := agent.NewEngine(log, bus, sessions, prov, "test-model", tool.NewRegistry(), gateway)
	dataDir := t.TempDir()
	be := NewService(
		sessions,
		log,
		bus,
		engine,
		gateway,
		terminal.NewManager(),
		nil,
		config.Provider{},
		dataDir,
	)
	artifactStore, err := artifact.NewFileStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	be.SetArtifactStore(artifactStore)
	be.SetConnections([]config.Connection{{
		ID:       "test-connection",
		Name:     "Test",
		Type:     config.ConnectionTypeLanguage,
		Kind:     "openai",
		AuthKind: "api_key",
	}})
	sess, err := be.CreateSession(conversation.CreateOptions{
		ConnectionID: "test-connection",
		Model:        "test-model",
		ApprovalMode: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	return be, sess.ID, prov
}

func TestEnqueueInputPreservesCanonicalAttachment(t *testing.T) {
	be, sessionID, _ := newQueueTestBackend(t)
	imageData, err := base64.StdEncoding.DecodeString(
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
	)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := be.PutImage(context.Background(), sessionID, "pixel.png", bytes.NewReader(imageData))
	if err != nil {
		t.Fatal(err)
	}
	item, err := be.EnqueueInput(context.Background(), sessionID, conversation.UserInput{
		Attachments: []conversation.AttachmentRef{{ID: ref.ID, Name: "spoofed.jpg"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if item.Text != "" || len(item.Attachments) != 1 || item.Attachments[0] != ref {
		t.Fatalf("queued image input = %#v", item)
	}
	if err := be.DeleteArtifact(context.Background(), sessionID, ref.ID); !errors.Is(err, artifact.ErrCommitted) {
		t.Fatalf("delete queued attachment error = %v", err)
	}
}

func TestEnqueueInputPreservesCanonicalSelectedSkill(t *testing.T) {
	be, sessionID, _ := newQueueTestBackend(t)
	skills, err := skill.NewManager(t.TempDir(), t.TempDir(), []skill.Skill{{
		Name: "writer",
		Body: "Write clearly.",
	}})
	if err != nil {
		t.Fatal(err)
	}
	be.SetCapabilityManagers(skills, nil, nil)

	item, err := be.EnqueueInput(context.Background(), sessionID, conversation.UserInput{
		Text:     "Draft the release notes.",
		SkillRef: "writer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if item.SkillRef != "builtin:writer" {
		t.Fatalf("selected skill ref = %q, want builtin:writer", item.SkillRef)
	}
	if _, err := be.EnqueueInput(context.Background(), sessionID, conversation.UserInput{
		Text:     "Draft the release notes.",
		SkillRef: "missing",
	}); err == nil {
		t.Fatal("missing selected skill should be rejected")
	}
}

func TestImageInputRequiresConnectionDeclaration(t *testing.T) {
	be, sessionID, _ := newQueueTestBackend(t)
	be.SetConnections([]config.Connection{{
		ID:       "test-connection",
		Name:     "Test",
		Type:     config.ConnectionTypeLanguage,
		Kind:     "openai",
		AuthKind: "api_key",
		ModelSettings: map[string]config.ModelSettings{
			"test-model": {
				ImageInput: false,
			},
		},
	}})
	_, err := be.EnqueueInput(context.Background(), sessionID, conversation.UserInput{
		Attachments: []conversation.AttachmentRef{{
			ID:   "image-attachment",
			Kind: "image",
		}},
	})
	if !errors.Is(err, ErrImageInputUnsupported) {
		t.Fatalf("enqueue image error = %v, want %v", err, ErrImageInputUnsupported)
	}
	_, err = be.SubmitInput(context.Background(), sessionID, conversation.UserInput{
		Attachments: []conversation.AttachmentRef{{
			ID:   "image-attachment",
			Kind: "image",
		}},
	})
	if !errors.Is(err, ErrImageInputUnsupported) {
		t.Fatalf("submit image error = %v, want %v", err, ErrImageInputUnsupported)
	}
}

func TestEnqueueInputPreservesBrowserElement(t *testing.T) {
	be, sessionID, _ := newQueueTestBackend(t)
	element := conversation.BrowserElement{
		PageURL:   "https://example.com/settings",
		PageTitle: "Settings",
		Tag:       "button",
		Selector:  "#save",
		Text:      "Save",
		HTML:      `<button id="save">Save</button>`,
	}
	item, err := be.EnqueueInput(context.Background(), sessionID, conversation.UserInput{
		BrowserElements: []conversation.BrowserElement{element},
	})
	if err != nil {
		t.Fatal(err)
	}
	if item.Text != "" || len(item.BrowserElements) != 1 ||
		item.BrowserElements[0] != element {
		t.Fatalf("queued browser input = %#v", item)
	}
}

func TestEnqueueInputRejectsInvalidBrowserElementURL(t *testing.T) {
	be, sessionID, _ := newQueueTestBackend(t)
	_, err := be.EnqueueInput(context.Background(), sessionID, conversation.UserInput{
		BrowserElements: []conversation.BrowserElement{{
			PageURL:  "file:///tmp/private",
			Tag:      "div",
			Selector: "#secret",
		}},
	})
	if err == nil {
		t.Fatal("expected invalid browser element URL to be rejected")
	}
}

func awaitStarted(t *testing.T, p *controlledProvider, want string) {
	t.Helper()
	select {
	case got := <-p.started:
		if got != want {
			t.Fatalf("started message = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %q to start", want)
	}
}

func awaitEventSnapshot(
	t *testing.T,
	be *Service,
	sessionID string,
	matches func([]conversation.Event) bool,
) []conversation.Event {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		events, err := be.log.Read(context.Background(), sessionID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if matches(events) {
			return events
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for event snapshot; saw %#v", eventKinds(events))
		}
		time.Sleep(time.Millisecond)
	}
}

func lastEvent(events []conversation.Event, kind conversation.Kind) *conversation.Event {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind == kind {
			return &events[i]
		}
	}
	return nil
}

func eventKinds(events []conversation.Event) []conversation.Kind {
	kinds := make([]conversation.Kind, 0, len(events))
	for _, ev := range events {
		kinds = append(kinds, ev.Kind)
	}
	return kinds
}

func payloadString(payload any, key string) string {
	fields, ok := payload.(map[string]any)
	if !ok {
		return ""
	}
	value, _ := fields[key].(string)
	return value
}

func TestSubmitTurnDrainsQueueInFIFOOrder(t *testing.T) {
	be, sessionID, prov := newQueueTestBackend(t)

	first, err := be.SubmitTurn(context.Background(), sessionID, "first")
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != SubmissionStarted {
		t.Fatalf("first status = %q, want %q", first.Status, SubmissionStarted)
	}
	awaitStarted(t, prov, "first")

	second, err := be.SubmitTurn(context.Background(), sessionID, "second")
	if err != nil {
		t.Fatal(err)
	}
	third, err := be.SubmitTurn(context.Background(), sessionID, "third")
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != SubmissionQueued || third.Status != SubmissionQueued {
		t.Fatalf("queued statuses = %q, %q", second.Status, third.Status)
	}

	prov.releases <- struct{}{}
	awaitStarted(t, prov, "second")
	items, err := be.ListQueuedMessages(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Text != "third" || items[0].Position != 0 {
		t.Fatalf("queue after first turn = %#v", items)
	}

	prov.releases <- struct{}{}
	awaitStarted(t, prov, "third")
	prov.releases <- struct{}{}
}

func TestDispatchQueuedMessageCancelsAndPrioritizes(t *testing.T) {
	be, sessionID, prov := newQueueTestBackend(t)

	if _, err := be.SubmitTurn(context.Background(), sessionID, "current"); err != nil {
		t.Fatal(err)
	}
	awaitStarted(t, prov, "current")
	queuedA, err := be.SubmitTurn(context.Background(), sessionID, "queued-a")
	if err != nil {
		t.Fatal(err)
	}
	queuedB, err := be.SubmitTurn(context.Background(), sessionID, "queued-b")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := be.DispatchQueuedMessage(context.Background(), sessionID, queuedB.Queued.ID); err != nil {
		t.Fatal(err)
	}
	awaitStarted(t, prov, "queued-b")
	events := awaitEventSnapshot(t, be, sessionID, func(events []conversation.Event) bool {
		return lastEvent(events, conversation.KindTurnComplete) != nil
	})
	if ev := lastEvent(events, conversation.KindTurnCancelRequested); ev != nil {
		t.Fatalf("queue dispatch should not record user stop request: %#v", ev)
	}
	complete := lastEvent(events, conversation.KindTurnComplete)
	if got := payloadString(complete.Payload, "reason"); got != string(agent.TurnCancelReasonQueueDispatch) {
		t.Fatalf("dispatch cancellation reason = %q, want %q", got, agent.TurnCancelReasonQueueDispatch)
	}

	items, err := be.ListQueuedMessages(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != queuedA.Queued.ID || items[0].Position != 0 {
		t.Fatalf("queue after dispatch = %#v", items)
	}
	prov.releases <- struct{}{}
	awaitStarted(t, prov, "queued-a")
	prov.releases <- struct{}{}
}

func TestCancelTurnPreservesAndPausesQueue(t *testing.T) {
	be, sessionID, prov := newQueueTestBackend(t)

	if _, err := be.SubmitTurn(context.Background(), sessionID, "current"); err != nil {
		t.Fatal(err)
	}
	awaitStarted(t, prov, "current")
	queued, err := be.SubmitTurn(context.Background(), sessionID, "later")
	if err != nil {
		t.Fatal(err)
	}

	be.CancelTurn(sessionID)
	select {
	case got := <-prov.started:
		t.Fatalf("unexpected queued turn start after cancel: %q", got)
	case <-time.After(50 * time.Millisecond):
	}

	items, err := be.ListQueuedMessages(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != queued.Queued.ID {
		t.Fatalf("queue after cancel = %#v", items)
	}

	events := awaitEventSnapshot(t, be, sessionID, func(events []conversation.Event) bool {
		return lastEvent(events, conversation.KindTurnComplete) != nil
	})
	cancelRequested := lastEvent(events, conversation.KindTurnCancelRequested)
	if cancelRequested == nil {
		t.Fatalf("turn_cancel_requested was not recorded: %#v", eventKinds(events))
	}
	if got := payloadString(cancelRequested.Payload, "reason"); got != string(agent.TurnCancelReasonUserStop) {
		t.Fatalf("cancel request reason = %q, want %q", got, agent.TurnCancelReasonUserStop)
	}
	complete := lastEvent(events, conversation.KindTurnComplete)
	if cancelRequested.RunID == "" || complete.RunID != cancelRequested.RunID {
		t.Fatalf("cancel run_id = %q, complete run_id = %q", cancelRequested.RunID, complete.RunID)
	}
	if got := payloadString(complete.Payload, "status"); got != "cancelled" {
		t.Fatalf("turn_complete status = %q, want cancelled", got)
	}
	if got := payloadString(complete.Payload, "reason"); got != string(agent.TurnCancelReasonUserStop) {
		t.Fatalf("turn_complete reason = %q, want %q", got, agent.TurnCancelReasonUserStop)
	}

	history, err := be.History(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) < 2 {
		t.Fatalf("history after cancel = %#v", history)
	}
	finalAssistant := history[len(history)-1]
	if finalAssistant.Role != conversation.RoleAssistant ||
		finalAssistant.TurnStatus != "cancelled" ||
		finalAssistant.TurnReason != string(agent.TurnCancelReasonUserStop) ||
		finalAssistant.TurnStartedAt == nil ||
		finalAssistant.TurnCompletedAt == nil {
		t.Fatalf("cancelled assistant terminal state = %#v", finalAssistant)
	}
}

func TestQueueSnapshotIsBroadcastToEverySubscriber(t *testing.T) {
	be, sessionID, _ := newQueueTestBackend(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := be.Subscribe(ctx, sessionID)
	second := be.Subscribe(ctx, sessionID)

	item, err := be.EnqueueMessage(context.Background(), sessionID, "shared")
	if err != nil {
		t.Fatal(err)
	}

	for i, ch := range []<-chan conversation.Event{first, second} {
		select {
		case ev := <-ch:
			if ev.Kind != conversation.KindQueueUpdated {
				t.Fatalf("subscriber %d event kind = %q", i, ev.Kind)
			}
			snapshot, ok := ev.Payload.(conversation.QueueSnapshot)
			if !ok {
				t.Fatalf("subscriber %d payload type = %T", i, ev.Payload)
			}
			if len(snapshot.Items) != 1 || snapshot.Items[0].ID != item.ID {
				t.Fatalf("subscriber %d snapshot = %#v", i, snapshot.Items)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d timed out", i)
		}
	}
}

func TestChildSessionsStayOutOfRootListAndDeleteWithParent(t *testing.T) {
	be, parentID, _ := newQueueTestBackend(t)
	child, err := be.sessions.Create(conversation.CreateOptions{ParentID: parentID, Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	grandchild, err := be.sessions.Create(conversation.CreateOptions{ParentID: child.ID, Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	roots := be.ListSessions()
	if len(roots) != 1 || roots[0].ID != parentID {
		t.Fatalf("root sessions = %#v", roots)
	}
	children, err := be.ChildSessions(parentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 1 || children[0].ID != child.ID {
		t.Fatalf("child sessions = %#v", children)
	}
	if err := be.DeleteSession(context.Background(), parentID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{parentID, child.ID, grandchild.ID} {
		if _, ok := be.sessions.Get(id); ok {
			t.Fatalf("session %s survived parent deletion", id)
		}
	}
}

func TestRewindTurnReturnsMessageAndDoesNotStartProvider(t *testing.T) {
	be, sessionID, prov := newQueueTestBackend(t)
	ctx := context.Background()
	filePath := filepath.Join(t.TempDir(), "main.go")
	beforeContent := []byte("old\n")
	afterContent := []byte("new\n")
	if err := os.WriteFile(filePath, afterContent, 0o644); err != nil {
		t.Fatal(err)
	}
	appendHistoryMessage(t, be, sessionID, conversation.Message{
		Role: conversation.RoleUser, Content: "first",
	})
	appendHistoryMessage(t, be, sessionID, conversation.Message{
		Role: conversation.RoleAssistant, Content: "first answer",
	})
	target := appendHistoryMessage(t, be, sessionID, conversation.Message{
		Role: conversation.RoleUser, Content: "old second",
	})
	appendHistoryMessage(t, be, sessionID, conversation.Message{
		Role: conversation.RoleAssistant,
		ToolCalls: []conversation.ToolCall{
			{
				ID:    "bash-1",
				Name:  "bash",
				Input: json.RawMessage(`{"command":"go test ./..."}`),
			},
			{
				ID:    "write-1",
				Name:  "write",
				Input: json.RawMessage(`{"path":"main.go","content":"new"}`),
			},
		},
	})
	appendHistoryMessage(t, be, sessionID, conversation.Message{
		Role:       conversation.RoleTool,
		ToolCallID: "bash-1",
		Content:    "ok",
	})
	appendHistoryMessage(t, be, sessionID, conversation.Message{
		Role:       conversation.RoleTool,
		ToolCallID: "write-1",
		Content:    "written",
		Diff:       "--- " + filePath + "\n+++ " + filePath + "\n@@ -1,1 +1,1 @@\n-old\n+new\n",
		FileChange: capturedFileChange(filePath, true, string(beforeContent), string(afterContent)),
	})

	preview, err := be.RewindTurn(ctx, sessionID, target, false, 0, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Status != RewindConfirmationNeeded ||
		preview.Message != "old second" ||
		len(preview.Files) != 1 ||
		preview.Files[0].Path != filepath.ToSlash(filePath) ||
		preview.Files[0].Status != RewindFileReady ||
		preview.FileStateToken == "" {
		t.Fatalf("rewind preview = %#v", preview)
	}
	history, err := be.History(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 6 {
		t.Fatalf("preview changed active history: %#v", history)
	}

	applied, err := be.RewindTurn(
		ctx,
		sessionID,
		target,
		true,
		preview.HeadSeq,
		preview.FileStateToken,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Status != RewindApplied || applied.Message != "old second" {
		t.Fatalf("rewind applied = %#v", applied)
	}
	restored, err := os.ReadFile(filePath)
	if err != nil || !bytes.Equal(restored, beforeContent) {
		t.Fatalf("restored file = %q, err = %v", restored, err)
	}
	select {
	case started := <-prov.started:
		t.Fatalf("rewind started provider: %q", started)
	case <-time.After(25 * time.Millisecond):
	}
	history, err = be.History(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 ||
		history[0].Content != "first" ||
		history[1].Content != "first answer" {
		t.Fatalf("active history = %#v", history)
	}
	events, err := be.log.Read(ctx, sessionID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if lastEvent(events, conversation.KindHistoryRewound) == nil {
		t.Fatalf("missing history_rewound event: %#v", eventKinds(events))
	}
}

func TestRewindTurnRejectsUserMessageWithContext(t *testing.T) {
	be, sessionID, _ := newQueueTestBackend(t)
	target := appendHistoryMessage(t, be, sessionID, conversation.Message{
		Role:    conversation.RoleUser,
		Content: "describe",
		Attachments: []conversation.AttachmentRef{{
			ID: "artifact-1", Name: "screen.png", Kind: "image", MediaType: "image/png",
		}},
	})

	_, err := be.RewindTurn(context.Background(), sessionID, target, false, 0, "", nil)
	if !errors.Is(err, ErrRewindContextUnsupported) {
		t.Fatalf("rewind attachment message error = %v", err)
	}
}

func TestRewindTurnRestoresReadyFilesAndKeepsModifiedFilesByDefault(t *testing.T) {
	be, sessionID, _ := newQueueTestBackend(t)
	ctx := context.Background()
	filePath := filepath.Join(t.TempDir(), "main.go")
	safePath := filepath.Join(t.TempDir(), "safe.go")
	beforeContent := []byte("before\n")
	afterContent := []byte("after\n")
	safeBefore := []byte("safe before\n")
	safeAfter := []byte("safe after\n")
	if err := os.WriteFile(filePath, []byte("external\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(safePath, safeAfter, 0o644); err != nil {
		t.Fatal(err)
	}
	target := appendHistoryMessage(t, be, sessionID, conversation.Message{
		Role: conversation.RoleUser, Content: "change file",
	})
	appendHistoryMessage(t, be, sessionID, conversation.Message{
		Role: conversation.RoleTool,
		Diff: "--- " + filePath + "\n+++ " + filePath +
			"\n@@ -1,1 +1,1 @@\n-before\n+after\n",
		FileChange: capturedFileChange(filePath, true, string(beforeContent), string(afterContent)),
	})
	appendHistoryMessage(t, be, sessionID, conversation.Message{
		Role: conversation.RoleTool,
		Diff: "--- " + safePath + "\n+++ " + safePath +
			"\n@@ -1,1 +1,1 @@\n-safe before\n+safe after\n",
		FileChange: capturedFileChange(safePath, true, string(safeBefore), string(safeAfter)),
	})

	preview, err := be.RewindTurn(ctx, sessionID, target, false, 0, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Files) != 2 ||
		preview.Files[0].Status != RewindFileModified ||
		preview.Files[1].Status != RewindFileReady {
		t.Fatalf("rewind preview = %#v", preview)
	}
	_, err = be.RewindTurn(
		ctx,
		sessionID,
		target,
		true,
		preview.HeadSeq,
		preview.FileStateToken,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filePath, "external\n")
	assertFileContent(t, safePath, "safe before\n")
	history, err := be.History(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 0 {
		t.Fatalf("history was not rewound: %#v", history)
	}
	events, err := be.log.Read(ctx, sessionID, 0)
	if err != nil {
		t.Fatal(err)
	}
	rewound := lastEvent(events, conversation.KindHistoryRewound)
	if rewound == nil {
		t.Fatalf("missing history_rewound event: %#v", eventKinds(events))
	}
	payload, ok := rewound.Payload.(conversation.HistoryRewound)
	if !ok || len(payload.Files) != 2 ||
		payload.Files[0].Action != conversation.RewindFileKept ||
		payload.Files[1].Action != conversation.RewindFileRestored {
		t.Fatalf("rewind file results = %#v", rewound)
	}
}

func TestRewindTurnMergesNonOverlappingUserChanges(t *testing.T) {
	be, sessionID, _ := newQueueTestBackend(t)
	ctx := context.Background()
	filePath := filepath.Join(t.TempDir(), "main.go")
	beforeContent := "name\nold implementation\nfooter\n"
	afterContent := "name\nagent implementation\nfooter\n"
	currentContent := "custom name\nagent implementation\nfooter\n"
	if err := os.WriteFile(filePath, []byte(currentContent), 0o644); err != nil {
		t.Fatal(err)
	}
	target := appendHistoryMessage(t, be, sessionID, conversation.Message{
		Role: conversation.RoleUser, Content: "change implementation",
	})
	appendHistoryMessage(t, be, sessionID, conversation.Message{
		Role: conversation.RoleTool,
		Diff: "--- " + filePath + "\n+++ " + filePath +
			"\n@@ -1,3 +1,3 @@\n name\n-old implementation\n+agent implementation\n footer\n",
		FileChange: capturedFileChange(filePath, true, beforeContent, afterContent),
	})

	preview, err := be.RewindTurn(ctx, sessionID, target, false, 0, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Files) != 1 || preview.Files[0].Status != RewindFileMergeable {
		t.Fatalf("rewind preview = %#v", preview)
	}
	_, err = be.RewindTurn(
		ctx,
		sessionID,
		target,
		true,
		preview.HeadSeq,
		preview.FileStateToken,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filePath, "custom name\nold implementation\nfooter\n")
}

func TestRewindTurnRejectsStaleFilePreview(t *testing.T) {
	be, sessionID, _ := newQueueTestBackend(t)
	ctx := context.Background()
	filePath := filepath.Join(t.TempDir(), "main.go")
	beforeContent := []byte("before\n")
	afterContent := []byte("after\n")
	if err := os.WriteFile(filePath, afterContent, 0o644); err != nil {
		t.Fatal(err)
	}
	target := appendHistoryMessage(t, be, sessionID, conversation.Message{
		Role: conversation.RoleUser, Content: "change file",
	})
	appendHistoryMessage(t, be, sessionID, conversation.Message{
		Role: conversation.RoleTool,
		Diff: "--- " + filePath + "\n+++ " + filePath +
			"\n@@ -1,1 +1,1 @@\n-before\n+after\n",
		FileChange: capturedFileChange(filePath, true, string(beforeContent), string(afterContent)),
	})

	preview, err := be.RewindTurn(ctx, sessionID, target, false, 0, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, []byte("external\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = be.RewindTurn(
		ctx,
		sessionID,
		target,
		true,
		preview.HeadSeq,
		preview.FileStateToken,
		nil,
	)
	if !errors.Is(err, ErrFileStateChanged) {
		t.Fatalf("stale file preview error = %v", err)
	}
	assertFileContent(t, filePath, "external\n")
	history, err := be.History(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("history changed after stale preview: %#v", history)
	}
}

func TestRewindTurnForceRestoresModifiedFile(t *testing.T) {
	be, sessionID, _ := newQueueTestBackend(t)
	ctx := context.Background()
	filePath := filepath.Join(t.TempDir(), "main.go")
	beforeContent := []byte("before\n")
	afterContent := []byte("after\n")
	if err := os.WriteFile(filePath, []byte("external\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := appendHistoryMessage(t, be, sessionID, conversation.Message{
		Role: conversation.RoleUser, Content: "change file",
	})
	appendHistoryMessage(t, be, sessionID, conversation.Message{
		Role: conversation.RoleTool,
		Diff: "--- " + filePath + "\n+++ " + filePath +
			"\n@@ -1,1 +1,1 @@\n-before\n+after\n",
		FileChange: capturedFileChange(filePath, true, string(beforeContent), string(afterContent)),
	})

	preview, err := be.RewindTurn(ctx, sessionID, target, false, 0, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Files) != 1 || preview.Files[0].Status != RewindFileModified {
		t.Fatalf("rewind preview = %#v", preview)
	}
	_, err = be.RewindTurn(
		ctx,
		sessionID,
		target,
		true,
		preview.HeadSeq,
		preview.FileStateToken,
		[]string{preview.Files[0].Key},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filePath, "before\n")
	events, err := be.log.Read(ctx, sessionID, 0)
	if err != nil {
		t.Fatal(err)
	}
	rewound := lastEvent(events, conversation.KindHistoryRewound)
	if rewound == nil {
		t.Fatalf("missing history_rewound event: %#v", eventKinds(events))
	}
	payload, ok := rewound.Payload.(conversation.HistoryRewound)
	if !ok || len(payload.Files) != 1 ||
		payload.Files[0].Action != conversation.RewindFileForceRestored {
		t.Fatalf("rewind file results = %#v", rewound.Payload)
	}
}

func appendHistoryMessage(
	t *testing.T,
	be *Service,
	sessionID string,
	msg conversation.Message,
) conversation.Seq {
	t.Helper()
	seq, err := be.log.Append(context.Background(), conversation.Event{
		Kind:    conversation.KindMessageEnd,
		Session: sessionID,
		Payload: msg,
		Time:    time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return seq
}
