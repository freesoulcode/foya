package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/backend"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/browseruse"
	"github.com/freesoulcode/foya/internal/config"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/state"
	"github.com/freesoulcode/foya/internal/terminal"
	"github.com/freesoulcode/foya/internal/tool"
)

func newBrowserUseTestServer(
	t *testing.T,
) (http.Handler, *browseruse.Controller, *broker.Broker[event.Event], string) {
	t.Helper()
	sessions := session.NewMemManager()
	log := state.NewMemLog()
	bus := broker.New[event.Event]()
	gateway := approval.NewGateway(bus, log)
	engine := agent.NewEngine(
		log,
		bus,
		sessions,
		idleProvider{},
		"test-model",
		tool.NewRegistry(),
		gateway,
	)
	dataDir := t.TempDir()
	be := backend.New(
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
	controller, err := browseruse.NewController(dataDir, bus, log)
	if err != nil {
		t.Fatal(err)
	}
	be.SetBrowserController(controller)
	sess, err := be.CreateSession(session.CreateOptions{Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	return New(config.Config{}, be).Handler(), controller, bus, sess.ID
}

func TestBrowserUseRoutesReportStatusAndResolveAction(t *testing.T) {
	handler, controller, bus, sessionID := newBrowserUseTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	events := bus.Subscribe(ctx, "session:"+sessionID)
	resultCh := make(chan browseruse.ActionResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := controller.Execute(ctx, browseruse.ActionRequest{
			ID: "request-1", SessionID: sessionID, Action: "snapshot",
		})
		resultCh <- result
		errCh <- err
	}()
	select {
	case ev := <-events:
		if ev.Kind != event.KindBrowserActionRequested {
			t.Fatalf("event kind = %q", ev.Kind)
		}
	case <-ctx.Done():
		t.Fatal("browser request was not published")
	}

	if code := requestJSON(
		t,
		handler,
		http.MethodPost,
		"/sessions/"+sessionID+"/browser-actions/request-1",
		map[string]any{
			"request_id":        "request-1",
			"url":               "https://example.com",
			"title":             "Example",
			"revision":          2,
			"snapshot":          `{"elements":[]}`,
			"screenshot_base64": "cG5n",
			"media_type":        "image/png",
		},
		nil,
	); code != http.StatusNoContent {
		t.Fatalf("browser resolution code = %d", code)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	result := <-resultCh
	if result.URL != "https://example.com" ||
		result.Title != "Example" ||
		result.Revision != 2 ||
		string(result.Screenshot) != "png" ||
		result.MediaType != "image/png" {
		t.Fatalf("browser result = %#v", result)
	}
}
