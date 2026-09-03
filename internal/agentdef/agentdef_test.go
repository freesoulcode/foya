package agentdef

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoveryScopesAndPrecedence(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	writeDefinition(t, filepath.Join(home, ".agents", "agents", "researcher.md"), `---
name: researcher
description: user researcher
tools: [read, agent, web_search]
model: user-model
---
User instructions.
`)
	writeDefinition(t, filepath.Join(project, ".agents", "agents", "researcher.md"), `---
name: researcher
description: project researcher
tools: [read]
---
Project instructions.
`)

	manager := NewManager(home, BuiltinDefinitions())
	items, err := manager.List(context.Background(), "project-1", project)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("effective definitions = %d, want 3", len(items))
	}
	resolved, err := manager.Get(context.Background(), "project-1", project, "researcher")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Scope != ScopeProject || resolved.Description != "project researcher" {
		t.Fatalf("resolved definition = %+v", resolved)
	}
	user, err := manager.Get(context.Background(), "project-1", project, "user:researcher")
	if err != nil {
		t.Fatal(err)
	}
	if user.Scope != ScopeUser || user.Model != "user-model" {
		t.Fatalf("global definition = %+v", user)
	}
	if len(user.Tools) != 2 {
		t.Fatalf("recursive agent tools were not removed: %#v", user.Tools)
	}
}

func TestBuiltinDefinitions(t *testing.T) {
	definitions := BuiltinDefinitions()
	if len(definitions) != 3 {
		t.Fatalf("builtin definitions = %d, want 3", len(definitions))
	}
	expected := map[string][]string{
		"explorer":   {"read", "skill_search", "skill_load", "skill_read_resource"},
		"researcher": {"web_search", "web_fetch", "skill_search", "skill_load", "skill_read_resource"},
		"worker": {
			"read", "bash", "write", "edit",
			"skill_search", "skill_load", "skill_read_resource",
		},
	}
	for _, definition := range definitions {
		tools, ok := expected[definition.Name]
		if !ok {
			t.Fatalf("unexpected builtin definition %q", definition.Name)
		}
		if len(definition.Tools) != len(tools) {
			t.Fatalf("%s tools = %#v, want %#v", definition.Name, definition.Tools, tools)
		}
		for index := range tools {
			if definition.Tools[index] != tools[index] {
				t.Fatalf("%s tools = %#v, want %#v", definition.Name, definition.Tools, tools)
			}
		}
	}
	if got := WorkerDefinition(); got.Name != "worker" {
		t.Fatalf("worker definition name = %q, want worker", got.Name)
	}
}

func TestDefinitionDigestChangesWithInstructions(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".agents", "agents", "analyst.md")
	writeDefinition(t, path, `---
name: analyst
description: analyst
---
First instructions.
`)
	manager := NewManager(home, nil)
	first, err := manager.Get(context.Background(), "", "", "analyst")
	if err != nil {
		t.Fatal(err)
	}
	writeDefinition(t, path, `---
name: analyst
description: analyst
---
Second instructions.
`)
	second, err := manager.Get(context.Background(), "", "", "analyst")
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest == second.Digest {
		t.Fatal("definition digest did not change")
	}
}

func writeDefinition(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
