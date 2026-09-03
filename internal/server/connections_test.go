package server

import (
	"net/http"
	"testing"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/backend"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/config"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/protocol"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/state"
	"github.com/freesoulcode/foya/internal/terminal"
	"github.com/freesoulcode/foya/internal/tool"
)

func TestConnectionRoutesRedactKeysAndBindSession(t *testing.T) {
	sessions := session.NewMemManager()
	log := state.NewMemLog()
	bus := broker.New[event.Event]()
	gateway := approval.NewGateway(bus, log)
	prov := idleProvider{}
	engine := agent.NewEngine(log, bus, sessions, prov, "fallback", tool.NewRegistry(), gateway)
	be := backend.New(
		sessions, log, bus, engine, gateway, terminal.NewManager(),
		func(connection config.Provider) (provider.Provider, string) {
			return prov, connection.Model
		},
		config.Provider{}, t.TempDir(),
	)
	handler := New(config.Config{}, be).Handler()

	if code := requestJSON(t, handler, http.MethodPost, "/connections", map[string]string{
		"name":      "Future subscription",
		"auth_kind": "oauth_subscription",
	}, nil); code != http.StatusBadRequest {
		t.Fatalf("unsupported auth status = %d, want %d", code, http.StatusBadRequest)
	}

	var connection connectionResponse
	if code := requestJSON(t, handler, http.MethodPost, "/connections", map[string]any{
		"name":      "DeepSeek",
		"kind":      "openai",
		"auth_kind": "api_key",
		"base_url":  "https://api.deepseek.com/v1",
		"api_key":   "secret",
		"model_settings": map[string]any{
			"deepseek-chat": map[string]any{
				"image_input":       true,
				"image_generation":  true,
				"context_window":    131072,
				"max_input_tokens":  130048,
				"max_output_tokens": 16384,
			},
		},
	}, &connection); code != http.StatusCreated {
		t.Fatalf("create connection status = %d", code)
	}
	if connection.APIKey != "secret" ||
		!connection.HasAPIKey ||
		connection.ID == "" ||
		!connection.ModelSettings["deepseek-chat"].ImageInput ||
		!connection.ModelSettings["deepseek-chat"].ImageGeneration ||
		connection.ModelSettings["deepseek-chat"].ContextWindow != 131072 ||
		connection.ModelSettings["deepseek-chat"].MaxInputTokens != 130048 ||
		connection.ModelSettings["deepseek-chat"].MaxOutputTokens != 16384 {
		t.Fatalf("public connection = %#v", connection)
	}

	var listed []connectionResponse
	if code := requestJSON(t, handler, http.MethodGet, "/connections", nil, &listed); code != http.StatusOK {
		t.Fatalf("list connection status = %d", code)
	}
	if len(listed) != 1 || listed[0].ID != connection.ID || listed[0].APIKey != "secret" {
		t.Fatalf("listed connections = %#v", listed)
	}

	var created session.Session
	if code := requestJSON(t, handler, http.MethodPost, "/sessions", map[string]string{
		"connection_id": connection.ID,
		"model":         "deepseek-chat",
	}, &created); code != http.StatusOK {
		t.Fatalf("create session status = %d", code)
	}
	if created.ConnectionID != connection.ID || created.Model != "deepseek-chat" {
		t.Fatalf("created session = %#v", created)
	}

	if code := requestJSON(t, handler, http.MethodDelete, "/connections/"+connection.ID, nil, nil); code != http.StatusNoContent {
		t.Fatalf("delete bound connection status = %d", code)
	}
	got, ok := sessions.Get(created.ID)
	if !ok {
		t.Fatalf("session %q missing after deleting connection", created.ID)
	}
	if got.ConnectionID != "" || got.Model != "deepseek-chat" {
		t.Fatalf("detached session = %#v", got)
	}
	var remaining []connectionResponse
	if code := requestJSON(t, handler, http.MethodGet, "/connections", nil, &remaining); code != http.StatusOK {
		t.Fatalf("list connections after deletion status = %d", code)
	}
	if len(remaining) != 0 {
		t.Fatalf("remaining connections = %#v", remaining)
	}
}

type connectionResponse struct {
	ID            string                            `json:"id"`
	Name          string                            `json:"name"`
	APIKey        string                            `json:"api_key"`
	HasAPIKey     bool                              `json:"has_api_key"`
	ModelSettings map[string]protocol.ModelSettings `json:"model_settings"`
}
