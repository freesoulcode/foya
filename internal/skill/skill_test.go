package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagerPrecedenceAndEnablement(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	projectPath := filepath.Join(root, "project")
	projectID := "project-alpha"
	data := filepath.Join(root, "data")
	writeSkill(t, filepath.Join(home, ".agents", "skills", "review", "SKILL.md"), `---
name: review
description: agent global version
allowed-tools:
  - read
---
agent global body`)
	writeSkill(t, filepath.Join(home, ".foya", "skills", "review", "SKILL.md"), `---
name: review
description: foya global version
---
foya global body`)
	writeSkill(t, filepath.Join(projectPath, ".foya", "skills", "review", "SKILL.md"), `---
name: review
description: project version
---
project body`)

	manager, err := NewManager(data, home, []Skill{{
		Name: "review", Description: "builtin version", Body: "builtin body",
	}})
	if err != nil {
		t.Fatal(err)
	}
	items, err := manager.List(context.Background(), projectID, projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Scope != ScopeProject || items[0].Body != "project body" {
		t.Fatalf("unexpected effective skills: %#v", items)
	}
	all, err := manager.ListAll(context.Background(), projectID, projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("expected builtin, global, and project skills, got %#v", all)
	}
	var global Skill
	for _, item := range all {
		if item.Scope == ScopeGlobal {
			global = item
		}
	}
	if global.Body != "foya global body" || !strings.Contains(global.Path, ".foya") {
		t.Fatalf(".foya global skill did not override .agents skill: %#v", global)
	}
	if err := manager.SetEnabled(items[0].Ref, false); err != nil {
		t.Fatal(err)
	}
	items, err = manager.List(context.Background(), projectID, projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Enabled {
		t.Fatal("expected disabled skill")
	}

	reloaded, err := NewManager(data, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	items, err = reloaded.List(context.Background(), projectID, projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Enabled {
		t.Fatalf("disabled state did not persist: %#v", items)
	}
}

func TestParseSkillWithoutFrontmatterUsesDirectoryName(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "plain", "SKILL.md")
	writeSkill(t, path, "# Plain\nDo the work.")
	item, err := parseFile(path, ScopeGlobal)
	if err != nil {
		t.Fatal(err)
	}
	if item.Name != "plain" || item.Body != "# Plain\nDo the work." {
		t.Fatalf("unexpected skill: %#v", item)
	}
}

func TestAgentProjectSkillIsDiscovered(t *testing.T) {
	root := t.TempDir()
	projectPath := filepath.Join(root, "project")
	projectID := "project-alpha"
	writeSkill(t, filepath.Join(projectPath, ".agents", "skills", "test", "SKILL.md"), `---
name: test
description: project agent skill
---
run tests`)
	manager, err := NewManager(filepath.Join(root, "data"), filepath.Join(root, "home"), nil)
	if err != nil {
		t.Fatal(err)
	}
	items, err := manager.List(context.Background(), projectID, projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Scope != ScopeProject ||
		!strings.Contains(items[0].Path, filepath.Join(".agents", "skills")) {
		t.Fatalf("unexpected project skills: %#v", items)
	}
}

func TestProjectSkillEnablementIsIsolatedByProjectID(t *testing.T) {
	root := t.TempDir()
	firstPath := filepath.Join(root, "first")
	secondPath := filepath.Join(root, "second")
	for _, path := range []string{firstPath, secondPath} {
		writeSkill(t, filepath.Join(path, ".foya", "skills", "review", "SKILL.md"), `---
name: review
description: project review
---
review project`)
	}
	manager, err := NewManager(filepath.Join(root, "data"), filepath.Join(root, "home"), nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.List(context.Background(), "project-first", firstPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.List(context.Background(), "project-second", secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if first[0].Ref == second[0].Ref {
		t.Fatalf("project skill refs must differ: %q", first[0].Ref)
	}
	if err := manager.SetEnabled(first[0].Ref, false); err != nil {
		t.Fatal(err)
	}
	first, _ = manager.List(context.Background(), "project-first", firstPath)
	second, _ = manager.List(context.Background(), "project-second", secondPath)
	if first[0].Enabled || !second[0].Enabled {
		t.Fatalf("enablement leaked between projects: first=%v second=%v", first[0].Enabled, second[0].Enabled)
	}
}

func writeSkill(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
