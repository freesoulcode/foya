package backend

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/freesoulcode/foya/internal/project"
	"github.com/freesoulcode/foya/internal/skill"
)

func TestInvocableSkillsFiltersDisabledAndUnsupportedRequirements(t *testing.T) {
	items := invocableSkills([]skill.Skill{
		{Name: "ready", Enabled: true},
		{Name: "disabled", Enabled: false},
		{Name: "missing-tool", Enabled: true, RequiredTools: []string{"missing_tool"}},
		{Name: "missing-capability", Enabled: true, RequiredCapabilities: []string{"missing_capability"}},
	})

	if len(items) != 1 || items[0].Name != "ready" {
		t.Fatalf("invocable skills = %#v, want only ready", items)
	}
}

func TestInvocableProjectSkillsUsesEffectiveProjectPrecedence(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	projectPath := filepath.Join(root, "workspace")
	writeBackendSkill(
		t,
		filepath.Join(home, ".foya", "skills", "review", "SKILL.md"),
		"---\nname: review\ndescription: global\n---\nglobal body",
	)
	writeBackendSkill(
		t,
		filepath.Join(projectPath, ".foya", "skills", "review", "SKILL.md"),
		"---\nname: review\ndescription: project\nrequired-tools: [read]\n---\nproject body",
	)

	skillManager, err := skill.NewManager(filepath.Join(root, "skill-data"), home, nil)
	if err != nil {
		t.Fatal(err)
	}
	projectManager, err := project.NewManager(filepath.Join(root, "project-data"))
	if err != nil {
		t.Fatal(err)
	}
	projectItem, err := projectManager.Create(projectPath, "Workspace")
	if err != nil {
		t.Fatal(err)
	}
	be := &Backend{skills: skillManager, projects: projectManager}

	items, err := be.InvocableProjectSkills(context.Background(), projectItem.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 ||
		items[0].Ref != "project:"+projectItem.ID+":review" ||
		items[0].Description != "project" {
		t.Fatalf("project invocable skills = %#v", items)
	}
}

func writeBackendSkill(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
