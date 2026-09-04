package backend

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/artifact"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/config"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/queue"
	"github.com/freesoulcode/foya/internal/session"
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

func (p *controlledProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.StreamEvent, error) {
	var text string
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == message.RoleUser {
			for _, part := range req.Messages[i].Parts {
				if part.Type == "text" {
					text += part.Text
				}
			}
			break
		}
	}
	p.started <- text

	out := make(chan provider.StreamEvent, 1)
	go func() {
		defer close(out)
		select {
		case <-ctx.Done():
		case <-p.releases:
			out <- provider.StreamEvent{Type: "done", FinishReason: "stop"}
		}
	}()
	return out, nil
}

func newQueueTestBackend(t *testing.T) (*Backend, string, *controlledProvider) {
	t.Helper()
	sessions := newTestSessionManager(t)
	log := newTestStore(t)
	bus := broker.New[event.Event]()
	gateway := approval.NewGateway(bus, log)
	prov := newControlledProvider()
	engine := agent.NewEngine(log, bus, sessions, prov, "test-model", tool.NewRegistry(), gateway)
	dataDir := t.TempDir()
	be := New(
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
	sess, err := be.CreateSession(session.CreateOptions{
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
	item, err := be.EnqueueInput(context.Background(), sessionID, message.UserInput{
		Attachments: []message.AttachmentRef{{ID: ref.ID, Name: "spoofed.jpg"}},
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
	_, err := be.EnqueueInput(context.Background(), sessionID, message.UserInput{
		Attachments: []message.AttachmentRef{{
			ID:   "image-attachment",
			Kind: "image",
		}},
	})
	if !errors.Is(err, ErrImageInputUnsupported) {
		t.Fatalf("enqueue image error = %v, want %v", err, ErrImageInputUnsupported)
	}
	_, err = be.SubmitInput(context.Background(), sessionID, message.UserInput{
		Attachments: []message.AttachmentRef{{
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
	element := message.BrowserElement{
		PageURL:   "https://example.com/settings",
		PageTitle: "Settings",
		Tag:       "button",
		Selector:  "#save",
		Text:      "Save",
		HTML:      `<button id="save">Save</button>`,
	}
	item, err := be.EnqueueInput(context.Background(), sessionID, message.UserInput{
		BrowserElements: []message.BrowserElement{element},
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
	_, err := be.EnqueueInput(context.Background(), sessionID, message.UserInput{
		BrowserElements: []message.BrowserElement{{
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
	be *Backend,
	sessionID string,
	matches func([]event.Event) bool,
) []event.Event {
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

func lastEvent(events []event.Event, kind event.Kind) *event.Event {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind == kind {
			return &events[i]
		}
	}
	return nil
}

func eventKinds(events []event.Event) []event.Kind {
	kinds := make([]event.Kind, 0, len(events))
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
	events := awaitEventSnapshot(t, be, sessionID, func(events []event.Event) bool {
		return lastEvent(events, event.KindTurnComplete) != nil
	})
	if ev := lastEvent(events, event.KindTurnCancelRequested); ev != nil {
		t.Fatalf("queue dispatch should not record user stop request: %#v", ev)
	}
	complete := lastEvent(events, event.KindTurnComplete)
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

	events := awaitEventSnapshot(t, be, sessionID, func(events []event.Event) bool {
		return lastEvent(events, event.KindTurnComplete) != nil
	})
	cancelRequested := lastEvent(events, event.KindTurnCancelRequested)
	if cancelRequested == nil {
		t.Fatalf("turn_cancel_requested was not recorded: %#v", eventKinds(events))
	}
	if got := payloadString(cancelRequested.Payload, "reason"); got != string(agent.TurnCancelReasonUserStop) {
		t.Fatalf("cancel request reason = %q, want %q", got, agent.TurnCancelReasonUserStop)
	}
	complete := lastEvent(events, event.KindTurnComplete)
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
	if finalAssistant.Role != message.RoleAssistant ||
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

	for i, ch := range []<-chan event.Event{first, second} {
		select {
		case ev := <-ch:
			if ev.Kind != event.KindQueueUpdated {
				t.Fatalf("subscriber %d event kind = %q", i, ev.Kind)
			}
			snapshot, ok := ev.Payload.(queue.Snapshot)
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
	child, err := be.sessions.Create(session.CreateOptions{ParentID: parentID, Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	grandchild, err := be.sessions.Create(session.CreateOptions{ParentID: child.ID, Model: "test-model"})
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

func TestEditTurnStartsFromActiveHistoryPrefix(t *testing.T) {
	be, sessionID, prov := newQueueTestBackend(t)
	ctx := context.Background()
	appendHistoryMessage(t, be, sessionID, message.Message{
		Role: message.RoleUser, Content: "first",
	})
	appendHistoryMessage(t, be, sessionID, message.Message{
		Role: message.RoleAssistant, Content: "first answer",
	})
	target := appendHistoryMessage(t, be, sessionID, message.Message{
		Role: message.RoleUser, Content: "old second",
	})
	appendHistoryMessage(t, be, sessionID, message.Message{
		Role: message.RoleAssistant, Content: "old answer",
	})

	result, err := be.EditTurn(ctx, sessionID, target, "edited second", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != EditStarted {
		t.Fatalf("edit status = %q", result.Status)
	}
	awaitStarted(t, prov, "edited second")

	history, err := be.History(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 ||
		history[0].Content != "first" ||
		history[1].Content != "first answer" ||
		history[2].Content != "edited second" {
		t.Fatalf("active history = %#v", history)
	}
	prov.releases <- struct{}{}
}

func TestEditTurnRejectsMessageWithAttachments(t *testing.T) {
	be, sessionID, _ := newQueueTestBackend(t)
	target := appendHistoryMessage(t, be, sessionID, message.Message{
		Role:    message.RoleUser,
		Content: "describe",
		Attachments: []message.AttachmentRef{{
			ID: "artifact-1", Name: "screen.png", Kind: "image", MediaType: "image/png",
		}},
	})
	_, err := be.EditTurn(context.Background(), sessionID, target, "edited", false, 0)
	if !errors.Is(err, ErrAttachmentEditUnsupported) {
		t.Fatalf("edit attachment message error = %v", err)
	}
}

func TestEditTurnRequiresConfirmationForRetainedEffects(t *testing.T) {
	be, sessionID, prov := newQueueTestBackend(t)
	ctx := context.Background()
	target := appendHistoryMessage(t, be, sessionID, message.Message{
		Role: message.RoleUser, Content: "change the file",
	})
	appendHistoryMessage(t, be, sessionID, message.Message{
		Role: message.RoleAssistant,
		ToolCalls: []message.ToolCall{{
			ID:    "write-1",
			Name:  "write",
			Input: json.RawMessage(`{"path":"main.go","content":"new"}`),
		}},
	})
	appendHistoryMessage(t, be, sessionID, message.Message{
		Role:       message.RoleTool,
		ToolCallID: "write-1",
		Content:    "written",
		Diff:       "@@ -0,0 +1 @@\n+new",
	})

	preview, err := be.EditTurn(
		ctx,
		sessionID,
		target,
		"change it differently",
		false,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Status != EditConfirmationRequired ||
		len(preview.Effects) != 1 ||
		preview.Effects[0].Detail != "main.go" {
		t.Fatalf("edit preview = %#v", preview)
	}
	select {
	case started := <-prov.started:
		t.Fatalf("turn started before confirmation: %q", started)
	case <-time.After(25 * time.Millisecond):
	}

	started, err := be.EditTurn(
		ctx,
		sessionID,
		target,
		"change it differently",
		true,
		preview.HeadSeq,
	)
	if err != nil {
		t.Fatal(err)
	}
	if started.Status != EditStarted {
		t.Fatalf("confirmed edit status = %q", started.Status)
	}
	awaitStarted(t, prov, "change it differently")
	prov.releases <- struct{}{}
}

func TestEditFirstTurnRefreshesAutomaticTitle(t *testing.T) {
	be, sessionID, prov := newQueueTestBackend(t)
	ctx := context.Background()
	target := appendHistoryMessage(t, be, sessionID, message.Message{
		Role: message.RoleUser, Content: "old first request",
	})
	appendHistoryMessage(t, be, sessionID, message.Message{
		Role: message.RoleAssistant, Content: "old answer",
	})
	if _, err := be.sessions.SetGeneratedTitle(sessionID, "old title"); err != nil {
		t.Fatal(err)
	}

	if _, err := be.EditTurn(
		ctx,
		sessionID,
		target,
		"edited first request",
		false,
		0,
	); err != nil {
		t.Fatal(err)
	}
	awaitStarted(t, prov, "edited first request")

	deadline := time.Now().Add(time.Second)
	for {
		sess, ok := be.sessions.Get(sessionID)
		if !ok {
			t.Fatal("session disappeared")
		}
		if sess.Title == "edited first request" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("automatic title = %q, want edited first request", sess.Title)
		}
		time.Sleep(time.Millisecond)
	}
	prov.releases <- struct{}{}
}

func appendHistoryMessage(
	t *testing.T,
	be *Backend,
	sessionID string,
	msg message.Message,
) event.Seq {
	t.Helper()
	seq, err := be.log.Append(context.Background(), event.Event{
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
