package backend

import (
	"context"
	"testing"
	"time"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/config"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/queue"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/state"
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
			text = req.Messages[i].Content
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
	sessions := session.NewMemManager()
	log := state.NewMemLog()
	bus := broker.New[event.Event]()
	gateway := approval.NewGateway(bus, log)
	prov := newControlledProvider()
	engine := agent.NewEngine(log, bus, sessions, prov, "test-model", tool.NewRegistry(), gateway)
	be := New(sessions, log, bus, engine, gateway, nil, config.Provider{}, t.TempDir())
	sess, err := be.CreateSession(session.CreateOptions{Model: "test-model", ApprovalMode: "ask"})
	if err != nil {
		t.Fatal(err)
	}
	return be, sess.ID, prov
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
