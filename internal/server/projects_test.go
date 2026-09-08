package server

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/config"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	interaction "github.com/freesoulcode/foya/internal/interaction"
	kernel "github.com/freesoulcode/foya/internal/kernel"
	model "github.com/freesoulcode/foya/internal/model"
	"github.com/freesoulcode/foya/internal/project"

	"github.com/freesoulcode/foya/internal/skill"
	"github.com/freesoulcode/foya/internal/terminal"
	"github.com/freesoulcode/foya/internal/tool"
)

func TestProjectRoutesBindSessionsAndDiscoverSkills(t *testing.T) {
	dataDir := t.TempDir()
	projectPath := filepath.Join(t.TempDir(), "alpha")
	skillPath := filepath.Join(projectPath, ".foya", "skills", "review", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte("---\nname: review\n---\nreview project"), 0o644); err != nil {
		t.Fatal(err)
	}

	sessions := newTestSessionManager(t)
	log := newTestStore(t)
	bus := broker.New[conversation.Event]()
	gateway := interaction.NewGateway(bus, log)
	prov := idleProvider{}
	engine := agent.NewEngine(log, bus, sessions, prov, "fallback", tool.NewRegistry(), gateway)
	be := kernel.NewService(
		sessions, log, bus, engine, gateway, terminal.NewManager(),
		func(connection config.Provider) (model.Provider, string) {
			return prov, connection.Model
		},
		config.Provider{}, dataDir,
	)
	projects, err := project.NewManager(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	skills, err := skill.NewManager(dataDir, filepath.Join(t.TempDir(), "home"), nil)
	if err != nil {
		t.Fatal(err)
	}
	be.SetProjectManager(projects)
	be.SetCapabilityManagers(skills, nil, nil)
	handler := New(config.Config{}, be).Handler()

	if code := requestJSON(t, handler, http.MethodPost, "/sessions", map[string]string{
		"workspace": projectPath,
	}, nil); code != http.StatusBadRequest {
		t.Fatalf("legacy workspace status = %d, want %d", code, http.StatusBadRequest)
	}

	var createdProject project.Project
	if code := requestJSON(t, handler, http.MethodPost, "/projects", map[string]string{
		"path": projectPath,
		"name": "设计",
	}, &createdProject); code != http.StatusCreated {
		t.Fatalf("create project status = %d", code)
	}
	if createdProject.Path == "" {
		t.Fatal("create project path is empty")
	}
	if createdProject.Name != "设计" {
		t.Fatalf("create project name = %q, want %q", createdProject.Name, "设计")
	}
	if code := requestJSON(
		t,
		handler,
		http.MethodPatch,
		"/projects/"+createdProject.ID,
		map[string]any{"name": "设计项目", "pinned": true},
		&createdProject,
	); code != http.StatusOK {
		t.Fatalf("update project status = %d", code)
	}
	if createdProject.Name != "设计项目" || !createdProject.Pinned ||
		createdProject.PinnedAt == nil {
		t.Fatalf("updated project = %#v", createdProject)
	}

	var defaultProject project.Project
	if code := requestJSON(t, handler, http.MethodPost, "/projects", map[string]string{
		"path": projectPath,
	}, &defaultProject); code != http.StatusCreated {
		t.Fatalf("create default project status = %d", code)
	}
	if defaultProject.Name != "New project" {
		t.Fatalf("default project name = %q, want %q", defaultProject.Name, "New project")
	}
	if defaultProject.ID == createdProject.ID || defaultProject.Path != createdProject.Path {
		t.Fatalf("shared folder project = %#v", defaultProject)
	}
	if code := requestJSON(
		t,
		handler,
		http.MethodDelete,
		"/projects/"+defaultProject.ID,
		nil,
		nil,
	); code != http.StatusNoContent {
		t.Fatalf("delete project status = %d, want %d", code, http.StatusNoContent)
	}
	if code := requestJSON(t, handler, http.MethodPost, "/sessions", map[string]string{
		"project_id": defaultProject.ID,
	}, nil); code != http.StatusBadRequest {
		t.Fatalf("deleted project session status = %d, want %d", code, http.StatusBadRequest)
	}

	var createdSession conversation.Session
	if code := requestJSON(t, handler, http.MethodPost, "/sessions", map[string]string{
		"project_id": createdProject.ID,
	}, &createdSession); code != http.StatusOK {
		t.Fatalf("create session status = %d", code)
	}
	if createdSession.ProjectID != createdProject.ID {
		t.Fatalf("session project = %q, want %q", createdSession.ProjectID, createdProject.ID)
	}
	var items []skill.Skill
	if code := requestJSON(
		t,
		handler,
		http.MethodGet,
		"/projects/"+createdProject.ID+"/skills",
		nil,
		&items,
	); code != http.StatusOK {
		t.Fatalf("list project skills status = %d", code)
	}
	if len(items) != 1 || items[0].Ref != "project:"+createdProject.ID+":review" {
		t.Fatalf("project skills = %#v", items)
	}
	if code := requestJSON(
		t,
		handler,
		http.MethodPatch,
		"/skills/"+items[0].Ref+"/pinned",
		map[string]bool{"pinned": true},
		nil,
	); code != http.StatusNoContent {
		t.Fatalf("pin project skill status = %d", code)
	}
	var report skill.ScanResult
	if code := requestJSON(
		t,
		handler,
		http.MethodGet,
		"/projects/"+createdProject.ID+"/skills/inspect",
		nil,
		&report,
	); code != http.StatusOK {
		t.Fatalf("inspect project skills status = %d", code)
	}
	if len(report.Skills) != 1 || !report.Skills[0].Pinned ||
		report.Skills[0].Ref != "project:"+createdProject.ID+":review" {
		t.Fatalf("project skill inspection = %#v", report.Skills)
	}
	if code := requestJSON(
		t,
		handler,
		http.MethodDelete,
		"/projects/"+createdProject.ID,
		nil,
		nil,
	); code != http.StatusNoContent {
		t.Fatalf("in-use project delete status = %d, want %d", code, http.StatusNoContent)
	}
	if _, ok := sessions.Get(createdSession.ID); ok {
		t.Fatal("project-bound session remains after project deletion")
	}
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("project deletion removed project files: %v", err)
	}

	if code := requestJSON(t, handler, http.MethodPost, "/sessions", map[string]string{
		"project_id": "missing",
	}, nil); code != http.StatusBadRequest {
		t.Fatalf("missing project status = %d, want %d", code, http.StatusBadRequest)
	}
}
