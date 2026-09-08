package kernel

import (
	"context"
	"testing"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/config"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	interaction "github.com/freesoulcode/foya/internal/interaction"
	model "github.com/freesoulcode/foya/internal/model"

	"github.com/freesoulcode/foya/internal/terminal"
	"github.com/freesoulcode/foya/internal/tool"
)

func TestSessionBindsConfiguredConnection(t *testing.T) {
	sessions := newTestSessionManager(t)
	log := newTestStore(t)
	bus := broker.New[conversation.Event]()
	gateway := interaction.NewGateway(bus, log)
	fallback := newControlledProvider()
	engine := agent.NewEngine(log, bus, sessions, fallback, "fallback", tool.NewRegistry(), gateway)
	be := NewService(
		sessions, log, bus, engine, gateway, terminal.NewManager(),
		func(connection config.Provider) (model.Provider, string) {
			return fallback, connection.Model
		},
		config.Provider{}, t.TempDir(),
	)
	be.SetConnections([]config.Connection{
		{ID: "openai", Name: "OpenAI", Type: config.ConnectionTypeLanguage, Kind: "openai", AuthKind: "api_key"},
		{ID: "deepseek", Name: "DeepSeek", Type: config.ConnectionTypeLanguage, Kind: "openai", AuthKind: "api_key"},
	})

	created, err := be.CreateSession(conversation.CreateOptions{
		ConnectionID: "deepseek",
		Model:        "deepseek-chat",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ConnectionID != "deepseek" || created.Model != "deepseek-chat" {
		t.Fatalf("session target = %#v", created)
	}

	if err := be.DeleteConnection("deepseek"); err != nil {
		t.Fatal(err)
	}
	updated, ok := sessions.Get(created.ID)
	if !ok {
		t.Fatalf("session %q missing after deleting connection", created.ID)
	}
	if updated.ConnectionID != "" || updated.Model != "deepseek-chat" {
		t.Fatalf("detached session target = %#v", updated)
	}

	if _, err := be.UpdateSession(
		context.Background(),
		created.ID,
		stringPointer("openai"),
		stringPointer("gpt-5"),
		nil,
		nil,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	updated, ok = sessions.Get(created.ID)
	if !ok || updated.ConnectionID != "openai" || updated.Model != "gpt-5" {
		t.Fatalf("updated session target = %#v", updated)
	}
}

func TestConnectionOrderSelectsNewSessionConnection(t *testing.T) {
	sessions := newTestSessionManager(t)
	log := newTestStore(t)
	bus := broker.New[conversation.Event]()
	gateway := interaction.NewGateway(bus, log)
	fallback := newControlledProvider()
	engine := agent.NewEngine(log, bus, sessions, fallback, "fallback", tool.NewRegistry(), gateway)
	be := NewService(
		sessions, log, bus, engine, gateway, terminal.NewManager(),
		func(connection config.Provider) (model.Provider, string) {
			return fallback, connection.Model
		},
		config.Provider{}, t.TempDir(),
	)
	be.SetConnections([]config.Connection{
		{ID: "openai", Name: "OpenAI", Type: config.ConnectionTypeLanguage, AuthKind: "api_key"},
		{ID: "deepseek", Name: "DeepSeek", Type: config.ConnectionTypeLanguage, AuthKind: "api_key"},
	})

	if _, err := be.UpdateConnection("deepseek", config.Connection{Type: config.ConnectionTypeLanguage, SortOrder: 0}); err != nil {
		t.Fatal(err)
	}
	ordered := be.Connections()
	if len(ordered) != 2 || ordered[0].ID != "deepseek" || ordered[0].SortOrder != 0 {
		t.Fatalf("connection order = %#v", ordered)
	}

	created, err := be.CreateSession(conversation.CreateOptions{
		ConnectionID: "deepseek",
		Model:        "deepseek-chat",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ConnectionID != "deepseek" || created.Model != "deepseek-chat" {
		t.Fatalf("new session target = %#v", created)
	}
}

func stringPointer(value string) *string { return &value }
