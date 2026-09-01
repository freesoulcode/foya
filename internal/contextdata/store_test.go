package contextdata

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStorePersistsRulesAndMemoriesByScope(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	globalRule, err := store.CreateRule(ScopeGlobal, "", "Use concise responses.")
	if err != nil {
		t.Fatal(err)
	}
	projectRule, err := store.CreateRule(ScopeProject, "project-1", "Run go test ./...")
	if err != nil {
		t.Fatal(err)
	}
	memory, err := store.CreateMemory(ScopeProject, "project-1", "The API uses REST and SSE.")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateRule(projectRule.ID, "Run go test ./... before completion."); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteRule(globalRule.ID); err != nil {
		t.Fatal(err)
	}

	restarted, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	rules := restarted.EffectiveRules("project-1")
	if len(rules) != 1 || rules[0].Content != "Run go test ./... before completion." {
		t.Fatalf("rules = %#v", rules)
	}
	memories := restarted.EffectiveMemories("project-1")
	if len(memories) != 1 || memories[0].ID != memory.ID {
		t.Fatalf("memories = %#v", memories)
	}
	if got := restarted.EffectiveMemories("project-2"); len(got) != 0 {
		t.Fatalf("other project memories = %#v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "context", "events.jsonl")); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryCreateIsIdempotentForExactContent(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateMemory(ScopeGlobal, "", "Prefer Go.")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateMemory(ScopeGlobal, "", " Prefer Go. ")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || len(store.ListMemories(ScopeGlobal, "")) != 1 {
		t.Fatalf("duplicate memories: %#v %#v", first, second)
	}
}

func TestMemoryScopeUsesOneDocumentAndAppendsFacts(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.AppendMemory(ScopeGlobal, "", "Prefer Go.")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AppendMemory(ScopeGlobal, "", "Use focused tests.")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("document IDs differ: %q %q", first.ID, second.ID)
	}
	items := store.ListMemories(ScopeGlobal, "")
	if len(items) != 1 ||
		!strings.Contains(items[0].Content, "Prefer Go.") ||
		!strings.Contains(items[0].Content, "Use focused tests.") {
		t.Fatalf("memory document = %#v", items)
	}
}

func TestMemorySettingsPersistWithoutDeletingDocuments(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.SetMemory(ScopeGlobal, "", "Prefer concise replies.")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateMemorySettings(MemorySettings{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if store.MemoryEnabled() {
		t.Fatal("memory should be disabled")
	}

	restarted, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.MemoryEnabled() {
		t.Fatal("disabled setting was not restored")
	}
	items := restarted.ListMemories(ScopeGlobal, "")
	if len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("memory document was not retained: %#v", items)
	}
	if effective := restarted.EffectiveMemories(""); len(effective) != 0 {
		t.Fatalf("disabled memory entered effective context: %#v", effective)
	}
}

func TestStoreValidationAndEvents(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRule(ScopeProject, "", "rule"); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("missing project error = %v", err)
	}
	if _, err := store.CreateMemory(ScopeGlobal, "", " "); !errors.Is(err, ErrEmptyContent) {
		t.Fatalf("empty content error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := store.Subscribe(ctx)
	item, err := store.CreateRule(ScopeGlobal, "", "Use tests.")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-ch:
		if ev.Kind != "rule_created" || ev.Rule == nil || ev.Rule.ID != item.ID {
			t.Fatalf("event = %#v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for context event")
	}
}

func TestConfiguredMemoryPathsAreHumanReadableMarkdown(t *testing.T) {
	dataDir := t.TempDir()
	homeDir := t.TempDir()
	projectPath := filepath.Join(t.TempDir(), "workspace", "repo")
	store, err := NewStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ConfigureFiles(
		filepath.Join(homeDir, ".foya", "memory"),
		filepath.Join(homeDir, ".foya", "rules"),
		func(id string) (string, bool) {
			return projectPath, id == "project-1"
		},
		func() map[string]string {
			return map[string]string{"project-1": projectPath}
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateMemory(ScopeGlobal, "", "Prefer concise answers."); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateMemory(ScopeProject, "project-1", "Use Go."); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRule(ScopeGlobal, "", "Prefer explicit errors."); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRule(ScopeProject, "project-1", "Run go test ./..."); err != nil {
		t.Fatal(err)
	}

	globalPath := filepath.Join(homeDir, ".foya", "memory", "user_profile.md")
	if data, err := os.ReadFile(globalPath); err != nil ||
		!strings.Contains(string(data), "Prefer concise answers.") {
		t.Fatalf("global memory: data=%q err=%v", data, err)
	}
	relative, err := projectPathFragment(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	projectFile := filepath.Join(
		homeDir, ".foya", "memory", "projects", relative, "project_memory.md",
	)
	if data, err := os.ReadFile(projectFile); err != nil ||
		!strings.Contains(string(data), "Use Go.") {
		t.Fatalf("project memory: data=%q err=%v", data, err)
	}
	globalRules := filepath.Join(homeDir, ".foya", "rules", "user_rules.md")
	if data, err := os.ReadFile(globalRules); err != nil ||
		!strings.Contains(string(data), "Prefer explicit errors.") {
		t.Fatalf("global rules: data=%q err=%v", data, err)
	}
	projectRules := filepath.Join(projectPath, ".foya", "rules")
	entries, err := os.ReadDir(projectRules)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("project rule files = %d, want 1", len(entries))
	}
	if data, err := os.ReadFile(filepath.Join(projectRules, entries[0].Name())); err != nil ||
		!strings.Contains(string(data), "Run go test ./...") {
		t.Fatalf("project rule: data=%q err=%v", data, err)
	}
	events, err := os.ReadFile(filepath.Join(dataDir, "context", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(events), "Prefer concise answers.") ||
		strings.Contains(string(events), "Use Go.") ||
		strings.Contains(string(events), "Prefer explicit errors.") ||
		strings.Contains(string(events), "Run go test ./...") {
		t.Fatal("rule or memory content leaked into the context event journal")
	}
}

func TestProjectRulesDiscoverThreeLevelsAndInferDirectoryGlobs(t *testing.T) {
	dataDir := t.TempDir()
	homeDir := t.TempDir()
	projectPath := t.TempDir()
	rulesDir := filepath.Join(projectPath, ".foya", "rules")
	files := map[string]string{
		"global-rules.md":     "Apply throughout the project.",
		"module-a/rules-a.md": "Apply in module A.",
		"module-a/submodule-a2/submodule-a2-b1/rules-a2-b1.md":   "Apply at level three.",
		"module-a/submodule-a2/submodule-a2-b1/too-deep/rule.md": "Ignore level four.",
	}
	for relative, content := range files {
		path := filepath.Join(rulesDir, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store, err := NewStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ConfigureFiles(
		filepath.Join(homeDir, ".foya", "memory"),
		filepath.Join(homeDir, ".foya", "rules"),
		func(id string) (string, bool) { return projectPath, id == "project-1" },
		func() map[string]string { return map[string]string{"project-1": projectPath} },
	); err != nil {
		t.Fatal(err)
	}
	items := store.ListRules(ScopeProject, "project-1")
	if len(items) != 3 {
		t.Fatalf("rules = %#v, want three recognized levels", items)
	}
	active := store.ActiveRules(
		"project-1",
		`editing "module-a/submodule-a2/submodule-a2-b1/service.go"`,
	)
	if len(active) != 3 {
		t.Fatalf("active rules = %#v, want root and matching ancestors", active)
	}
}

func TestRuleActivationModes(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	always, err := store.CreateRule(ScopeGlobal, "", "Always active.", RuleOptions{
		Name: "always", Trigger: RuleAlways,
	})
	if err != nil {
		t.Fatal(err)
	}
	manual, err := store.CreateRule(ScopeGlobal, "", "Manual.", RuleOptions{
		Name: "review", Trigger: RuleManual,
	})
	if err != nil {
		t.Fatal(err)
	}
	model, err := store.CreateRule(ScopeGlobal, "", "Model selected.", RuleOptions{
		Name: "database", Description: "Database migration constraints.",
		Trigger: RuleModelDecision,
	})
	if err != nil {
		t.Fatal(err)
	}
	active := store.ActiveRules("", "please use @review")
	if len(active) != 2 || active[0].ID != always.ID || active[1].ID != manual.ID {
		t.Fatalf("active rules = %#v", active)
	}
	available := store.AvailableRules("")
	if len(available) != 1 || available[0].ID != model.ID {
		t.Fatalf("available rules = %#v", available)
	}
	loaded, err := store.GetRule("", model.Name)
	if err != nil || loaded.ID != model.ID {
		t.Fatalf("loaded rule = %#v, err = %v", loaded, err)
	}
}

func TestUpdateProjectRuleMovesItsMarkdownFile(t *testing.T) {
	dataDir := t.TempDir()
	projectPath := t.TempDir()
	store, err := NewStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ConfigureFiles(
		filepath.Join(t.TempDir(), ".foya", "memory"),
		filepath.Join(t.TempDir(), ".foya", "rules"),
		func(id string) (string, bool) { return projectPath, id == "project-1" },
		func() map[string]string { return map[string]string{"project-1": projectPath} },
	); err != nil {
		t.Fatal(err)
	}
	item, err := store.CreateRule(
		ScopeProject, "project-1", "Run focused tests.", RuleOptions{
			Name: "tests", Trigger: RuleGlob, Globs: []string{"backend/**"},
			Path: "backend/testing.md",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(projectPath, ".foya", "rules", "backend", "testing.md")
	updated, err := store.UpdateRule(item.ID, item.Content, RuleOptions{
		Name: item.Name, Trigger: RuleGlob, Globs: []string{"internal/**"},
		Path: "internal/testing.md",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Path != "internal/testing.md" {
		t.Fatalf("updated path = %q", updated.Path)
	}
	if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old rule file still exists: %v", err)
	}
	newPath := filepath.Join(projectPath, ".foya", "rules", "internal", "testing.md")
	if data, err := os.ReadFile(newPath); err != nil ||
		!strings.Contains(string(data), "Run focused tests.") {
		t.Fatalf("new rule file: data=%q err=%v", data, err)
	}
}

func TestCreateProjectRuleDefaultsPathFromName(t *testing.T) {
	dataDir := t.TempDir()
	projectPath := t.TempDir()
	store, err := NewStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ConfigureFiles(
		filepath.Join(t.TempDir(), ".foya", "memory"),
		filepath.Join(t.TempDir(), ".foya", "rules"),
		func(id string) (string, bool) { return projectPath, id == "project-1" },
		func() map[string]string { return map[string]string{"project-1": projectPath} },
	); err != nil {
		t.Fatal(err)
	}
	item, err := store.CreateRule(
		ScopeProject, "project-1", "Run focused tests.", RuleOptions{
			Name: "Go Testing", Trigger: RuleAlways,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if item.Path != "go-testing.md" {
		t.Fatalf("default path = %q", item.Path)
	}
	path := filepath.Join(projectPath, ".foya", "rules", "go-testing.md")
	if data, err := os.ReadFile(path); err != nil ||
		!strings.Contains(string(data), "Run focused tests.") {
		t.Fatalf("rule file: data=%q err=%v", data, err)
	}
}
