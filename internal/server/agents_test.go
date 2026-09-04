package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/agentdef"
	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/backend"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/config"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/project"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/subagent"
	"github.com/freesoulcode/foya/internal/terminal"
	"github.com/freesoulcode/foya/internal/tool"
)

func TestAgentRoutesExposeGlobalAndProjectDefinitions(t *testing.T) {
	home := t.TempDir()
	projectPath := t.TempDir()
	writeAgentDefinition(t, filepath.Join(home, ".agents", "agents", "global.md"), `---
name: global-researcher
description: global agent
---
Research globally.
`)
	writeAgentDefinition(t, filepath.Join(projectPath, ".agents", "agents", "project.md"), `---
name: project-researcher
description: project agent
---
Research this project.
`)

	sessions := newTestSessionManager(t)
	log := newTestStore(t)
	bus := broker.New[event.Event]()
	gateway := approval.NewGateway(bus, log)
	prov := idleProvider{}
	engine := agent.NewEngine(log, bus, sessions, prov, "fallback", tool.NewRegistry(), gateway)
	be := backend.New(
		sessions, log, bus, engine, gateway, terminal.NewManager(),
		nil, config.Provider{}, t.TempDir(),
	)
	projects, err := project.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := projects.Create(projectPath, "project")
	if err != nil {
		t.Fatal(err)
	}
	be.SetProjectManager(projects)
	be.SetAgentManager(agentdef.NewManager(home, agentdef.BuiltinDefinitions()))
	handler := New(config.Config{}, be).Handler()

	var global []agentdef.Definition
	if code := requestJSON(t, handler, http.MethodGet, "/agents", nil, &global); code != http.StatusOK {
		t.Fatalf("global agents status = %d", code)
	}
	if len(global) != 4 {
		t.Fatalf("global agents = %#v", global)
	}

	var scoped []agentdef.Definition
	if code := requestJSON(
		t, handler, http.MethodGet, "/projects/"+created.ID+"/agents", nil, &scoped,
	); code != http.StatusOK {
		t.Fatalf("project agents status = %d", code)
	}
	if len(scoped) != 5 {
		t.Fatalf("project agents = %#v", scoped)
	}
}

func TestAgentLimitsRoutesPersistAndApplySettings(t *testing.T) {
	dataDir := t.TempDir()
	sessions := newTestSessionManager(t)
	log := newTestStore(t)
	bus := broker.New[event.Event]()
	gateway := approval.NewGateway(bus, log)
	engine := agent.NewEngine(
		log, bus, sessions, idleProvider{}, "fallback", tool.NewRegistry(), gateway,
	)
	definitions := agentdef.NewManager("", agentdef.BuiltinDefinitions())
	manager := subagent.NewManager(
		definitions, sessions, engine, log, log, bus, nil,
		subagent.Limits{
			MaxGlobalConcurrency: 4,
			MaxPerRoot:           4,
			MaxChildrenPerRoot:   config.InternalMaxChildrenPerRoot,
		},
	)
	be := backend.New(
		sessions, log, bus, engine, gateway, terminal.NewManager(),
		nil, config.Provider{}, dataDir,
	)
	be.SetSubAgentManager(manager)
	handler := New(config.Config{}, be).Handler()

	want := config.AgentLimits{
		MaxGlobalConcurrency: 6,
		MaxPerRoot:           3,
		MaxTreeTokens:        150_000,
	}
	var updated config.AgentLimits
	if code := requestJSON(
		t, handler, http.MethodPut, "/settings/agent-limits", want, &updated,
	); code != http.StatusOK {
		t.Fatalf("update limits status = %d", code)
	}
	if updated != want {
		t.Fatalf("updated limits = %+v, want %+v", updated, want)
	}

	var loaded config.AgentLimits
	if code := requestJSON(
		t, handler, http.MethodGet, "/settings/agent-limits", nil, &loaded,
	); code != http.StatusOK {
		t.Fatalf("get limits status = %d", code)
	}
	if loaded != want {
		t.Fatalf("loaded limits = %+v, want %+v", loaded, want)
	}
	persisted, ok, err := config.LoadAgentLimits(dataDir)
	if err != nil || !ok || persisted != want {
		t.Fatalf("persisted limits = %+v, %v, %v", persisted, ok, err)
	}
	if manager.Limits().MaxGlobalConcurrency != want.MaxGlobalConcurrency {
		t.Fatalf("runtime concurrency = %d, want %d", manager.Limits().MaxGlobalConcurrency, want.MaxGlobalConcurrency)
	}
	if manager.Limits().MaxChildrenPerRoot != config.InternalMaxChildrenPerRoot {
		t.Fatalf("internal children safety = %d, want %d (must not change via API)",
			manager.Limits().MaxChildrenPerRoot, config.InternalMaxChildrenPerRoot)
	}

	withRemovedField := map[string]any{
		"max_global_concurrency": 6,
		"max_per_root":           3,
		"max_tree_tokens":        150_000,
		"max_depth":              1,
	}
	if code := requestJSON(
		t, handler, http.MethodPut, "/settings/agent-limits", withRemovedField, nil,
	); code != http.StatusBadRequest {
		t.Fatalf("removed field status = %d, want %d", code, http.StatusBadRequest)
	}
}

func TestAgentLimitsChildSafetyStillEnforced(t *testing.T) {
	dataDir := t.TempDir()
	sessions := newTestSessionManager(t)
	parent, _ := sessions.Create(session.CreateOptions{Model: "model"})
	log := newTestStore(t)
	bus := broker.New[event.Event]()
	gateway := approval.NewGateway(bus, log)
	engine := agent.NewEngine(
		log, bus, sessions, idleProvider{}, "fallback", tool.NewRegistry(), gateway,
	)
	definitions := agentdef.NewManager("", agentdef.BuiltinDefinitions())
	manager := subagent.NewManager(
		definitions, sessions, engine, log, log, bus, nil,
		subagent.Limits{
			MaxGlobalConcurrency: 256,
			MaxPerRoot:           256,
			MaxChildrenPerRoot:   3,
		},
	)
	be := backend.New(
		sessions, log, bus, engine, gateway, terminal.NewManager(),
		nil, config.Provider{}, dataDir,
	)
	be.SetSubAgentManager(manager)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := be.StartAgent(ctx, subagent.SpawnRequest{
			ParentSessionID: parent.ID, Task: "t", RootRunID: "root",
		}); err != nil {
			t.Fatalf("child %d failed: %v", i, err)
		}
	}
	_, err := be.StartAgent(ctx, subagent.SpawnRequest{
		ParentSessionID: parent.ID, Task: "overflow", RootRunID: "root",
	})
	if err == nil || !strings.Contains(err.Error(), "internal safety limit") {
		t.Fatalf("4th child should be blocked by internal safety, got err = %v", err)
	}
}

func writeAgentDefinition(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
