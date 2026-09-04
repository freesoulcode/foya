package server

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/backend"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/config"
	"github.com/freesoulcode/foya/internal/contextdata"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/project"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/terminal"
	"github.com/freesoulcode/foya/internal/tool"
)

func TestRuleAndMemoryRoutesKeepScopesSeparate(t *testing.T) {
	dataDir := t.TempDir()
	sessions := newTestSessionManager(t)
	log := newTestStore(t)
	bus := broker.New[event.Event]()
	gateway := approval.NewGateway(bus, log)
	prov := idleProvider{}
	engine := agent.NewEngine(log, bus, sessions, prov, "fallback", tool.NewRegistry(), gateway)
	be := backend.New(
		sessions, log, bus, engine, gateway, terminal.NewManager(),
		func(connection config.Provider) (provider.Provider, string) {
			return prov, connection.Model
		},
		config.Provider{}, dataDir,
	)
	projects, err := project.NewManager(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	contextStore, err := contextdata.NewStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	be.SetProjectManager(projects)
	be.SetContextStore(contextStore)
	projectItem, err := projects.Create(filepath.Dir(dataDir), "test")
	if err != nil {
		t.Fatal(err)
	}
	handler := New(config.Config{}, be).Handler()

	var rule contextdata.Rule
	if code := requestJSON(t, handler, http.MethodPost, "/rules", map[string]any{
		"scope": "global", "content": "Use Go formatting.",
	}, &rule); code != http.StatusCreated {
		t.Fatalf("create rule status = %d", code)
	}
	var memory contextdata.Memory
	if code := requestJSON(t, handler, http.MethodPost, "/memories", map[string]any{
		"scope": "project", "project_id": projectItem.ID, "content": "The API uses SSE.",
	}, &memory); code != http.StatusCreated {
		t.Fatalf("create memory status = %d", code)
	}

	var rules []contextdata.Rule
	if code := requestJSON(t, handler, http.MethodGet, "/rules?scope=global", nil, &rules); code != http.StatusOK {
		t.Fatalf("list rules status = %d", code)
	}
	if len(rules) != 1 || rules[0].ID != rule.ID {
		t.Fatalf("rules = %#v", rules)
	}
	var memories []contextdata.Memory
	path := "/memories?scope=project&project_id=" + projectItem.ID
	if code := requestJSON(t, handler, http.MethodGet, path, nil, &memories); code != http.StatusOK {
		t.Fatalf("list memories status = %d", code)
	}
	if len(memories) != 1 || memories[0].ID != memory.ID {
		t.Fatalf("memories = %#v", memories)
	}

	if code := requestJSON(t, handler, http.MethodPatch, "/rules/"+rule.ID, map[string]string{
		"content": "Use gofmt.",
	}, &rule); code != http.StatusOK || rule.Content != "Use gofmt." {
		t.Fatalf("updated rule = %#v, status = %d", rule, code)
	}
	if code := requestJSON(t, handler, http.MethodDelete, "/memories/"+memory.ID, nil, nil); code != http.StatusNoContent {
		t.Fatalf("delete memory status = %d", code)
	}
}

func TestRulePatchWithoutMetadataPreservesRuleConfiguration(t *testing.T) {
	dataDir := t.TempDir()
	sessions := newTestSessionManager(t)
	log := newTestStore(t)
	bus := broker.New[event.Event]()
	gateway := approval.NewGateway(bus, log)
	prov := idleProvider{}
	engine := agent.NewEngine(log, bus, sessions, prov, "fallback", tool.NewRegistry(), gateway)
	be := backend.New(
		sessions, log, bus, engine, gateway, terminal.NewManager(),
		func(connection config.Provider) (provider.Provider, string) {
			return prov, connection.Model
		},
		config.Provider{}, dataDir,
	)
	contextStore, err := contextdata.NewStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	be.SetContextStore(contextStore)
	handler := New(config.Config{}, be).Handler()

	var rule contextdata.Rule
	if code := requestJSON(t, handler, http.MethodPost, "/rules", map[string]any{
		"scope": "global", "content": "Review migrations.",
		"name": "migrations", "description": "Schema changes.",
		"trigger": "model_decision",
	}, &rule); code != http.StatusCreated {
		t.Fatalf("create rule status = %d", code)
	}
	if code := requestJSON(t, handler, http.MethodPatch, "/rules/"+rule.ID, map[string]string{
		"content": "Review every migration.",
	}, &rule); code != http.StatusOK {
		t.Fatalf("update rule status = %d", code)
	}
	if rule.Trigger != contextdata.RuleModelDecision ||
		rule.Name != "migrations" || rule.Description != "Schema changes." {
		t.Fatalf("rule metadata was cleared: %#v", rule)
	}
}

func TestMemorySettingsRoutes(t *testing.T) {
	dataDir := t.TempDir()
	sessions := newTestSessionManager(t)
	log := newTestStore(t)
	bus := broker.New[event.Event]()
	gateway := approval.NewGateway(bus, log)
	prov := idleProvider{}
	engine := agent.NewEngine(log, bus, sessions, prov, "fallback", tool.NewRegistry(), gateway)
	be := backend.New(
		sessions, log, bus, engine, gateway, terminal.NewManager(),
		func(connection config.Provider) (provider.Provider, string) {
			return prov, connection.Model
		},
		config.Provider{}, dataDir,
	)
	contextStore, err := contextdata.NewStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	be.SetContextStore(contextStore)
	handler := New(config.Config{}, be).Handler()

	var settings contextdata.MemorySettings
	if code := requestJSON(t, handler, http.MethodGet, "/settings/memory", nil, &settings); code != http.StatusOK ||
		!settings.Enabled {
		t.Fatalf("initial settings = %#v, status = %d", settings, code)
	}
	if code := requestJSON(
		t, handler, http.MethodPut, "/settings/memory",
		contextdata.MemorySettings{Enabled: false}, &settings,
	); code != http.StatusOK || settings.Enabled {
		t.Fatalf("updated settings = %#v, status = %d", settings, code)
	}
}
