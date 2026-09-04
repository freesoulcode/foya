package browseruse

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/testkit"
)

func newTestController(t *testing.T) *Controller {
	t.Helper()
	controller, err := NewController(
		t.TempDir(),
		broker.New[event.Event](),
		testkit.NewLog(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return controller
}

func TestNormalizeBrowserURL(t *testing.T) {
	for input, want := range map[string]string{
		"https://example.com/":     "https://example.com/",
		"`https://example.com/`":   "https://example.com/",
		"  `https://example.com/`": "https://example.com/",
	} {
		if got := normalizeBrowserURL(input); got != want {
			t.Fatalf("normalizeBrowserURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeSnapshotURLs(t *testing.T) {
	raw := `{"url":"` + "`https://example.com/`" + `","elements":[{"attrs":{"href":"` + "`https://example.com/docs`" + `"}}]}`
	want := `{"elements":[{"attrs":{"href":"https://example.com/docs"}}],"url":"https://example.com/"}`
	if got := normalizeSnapshot(raw); got != want {
		t.Fatalf("normalizeSnapshot() = %s, want %s", got, want)
	}
}

func TestWebViewActionPublishesAndResolves(t *testing.T) {
	controller := newTestController(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	events := controller.bus.Subscribe(ctx, "session:session-1")
	resultCh := make(chan ActionResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := controller.Execute(ctx, ActionRequest{
			ID: "request-1", SessionID: "session-1", Action: "snapshot",
		})
		resultCh <- result
		errCh <- err
	}()

	select {
	case ev := <-events:
		if ev.Kind != event.KindBrowserActionRequested {
			t.Fatalf("event kind = %q", ev.Kind)
		}
		request, ok := ev.Payload.(ActionRequest)
		if !ok || request.ID != "request-1" ||
			request.BrowserID != "agent-session-1" {
			t.Fatalf("event payload = %#v", ev.Payload)
		}
	case <-ctx.Done():
		t.Fatal("browser action request was not published")
	}

	want := ActionResult{
		URL: "https://example.com", Title: "Example",
		Revision: 3, Snapshot: `{"elements":[]}`,
	}
	if err := controller.Resolve("session-1", "request-1", want); err != nil {
		t.Fatal(err)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	got := <-resultCh
	if got.URL != want.URL || got.Title != want.Title ||
		got.Revision != want.Revision || got.Snapshot != want.Snapshot {
		t.Fatalf("result = %#v, want %#v", got, want)
	}

	select {
	case ev := <-events:
		if ev.Kind != event.KindBrowserActionResolved {
			t.Fatalf("resolved event kind = %q", ev.Kind)
		}
	case <-ctx.Done():
		t.Fatal("browser action resolution was not published")
	}
}

func TestWebViewActionCancellationRemovesPendingRequest(t *testing.T) {
	controller := newTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	eventCtx, stopEvents := context.WithCancel(context.Background())
	defer stopEvents()
	events := controller.bus.Subscribe(eventCtx, "session:session-1")
	errCh := make(chan error, 1)
	go func() {
		_, err := controller.Execute(ctx, ActionRequest{
			ID: "request-1", SessionID: "session-1", Action: "snapshot",
		})
		errCh <- err
	}()
	<-events
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("execute error = %v", err)
	}
	if err := controller.Resolve(
		"session-1",
		"request-1",
		ActionResult{},
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("resolve after cancellation error = %v", err)
	}
}
