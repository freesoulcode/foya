package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/artifact"
	"github.com/freesoulcode/foya/internal/backend"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/config"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/queue"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/state"
	"github.com/freesoulcode/foya/internal/terminal"
	"github.com/freesoulcode/foya/internal/tool"
)

type idleProvider struct{}

func (idleProvider) Name() string { return "idle" }

func (idleProvider) Stream(context.Context, provider.Request) (<-chan provider.StreamEvent, error) {
	out := make(chan provider.StreamEvent, 1)
	out <- provider.StreamEvent{Type: "done", FinishReason: "stop"}
	close(out)
	return out, nil
}

func newQueueTestServer(t *testing.T) (http.Handler, string) {
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
		ModelSettings: map[string]config.ModelSettings{
			"test-model": {ImageInput: true},
		},
	}})
	sess, err := be.CreateSession(session.CreateOptions{
		ConnectionID: "test-connection",
		Model:        "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	return New(config.Config{}, be).Handler(), sess.ID
}

func requestJSON(t *testing.T, handler http.Handler, method, path string, body any, dst any) int {
	t.Helper()
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if dst != nil {
		if err := json.NewDecoder(rec.Body).Decode(dst); err != nil {
			t.Fatalf("decode %s %s response: %v", method, path, err)
		}
	}
	return rec.Code
}

func TestQueueRoutesEditReorderAndDelete(t *testing.T) {
	handler, sessionID := newQueueTestServer(t)
	base := "/sessions/" + sessionID + "/queue"

	var first queue.Message
	if code := requestJSON(t, handler, http.MethodPost, base, map[string]string{"message": "first"}, &first); code != http.StatusCreated {
		t.Fatalf("first enqueue status = %d", code)
	}
	var second queue.Message
	if code := requestJSON(t, handler, http.MethodPost, base, map[string]string{"message": "second"}, &second); code != http.StatusCreated {
		t.Fatalf("second enqueue status = %d", code)
	}

	var updated queue.Message
	if code := requestJSON(
		t,
		handler,
		http.MethodPatch,
		base+"/"+second.ID,
		map[string]any{"message": "second edited", "position": 0},
		&updated,
	); code != http.StatusOK {
		t.Fatalf("update status = %d", code)
	}
	if updated.Text != "second edited" || updated.Position != 0 {
		t.Fatalf("updated item = %#v", updated)
	}

	var items []queue.Message
	if code := requestJSON(t, handler, http.MethodGet, base, nil, &items); code != http.StatusOK {
		t.Fatalf("list status = %d", code)
	}
	if len(items) != 2 || items[0].ID != second.ID || items[1].ID != first.ID {
		t.Fatalf("ordered queue = %#v", items)
	}

	if code := requestJSON(t, handler, http.MethodDelete, base+"/"+first.ID, nil, nil); code != http.StatusNoContent {
		t.Fatalf("delete status = %d", code)
	}
	items = nil
	requestJSON(t, handler, http.MethodGet, base, nil, &items)
	if len(items) != 1 || items[0].ID != second.ID {
		t.Fatalf("queue after delete = %#v", items)
	}
}
