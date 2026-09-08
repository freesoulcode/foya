package workflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestListProjectOverridesGlobal(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	writeCommand(t, filepath.Join(GlobalRoot(homeDir), "review.md"), "review", "global review", "global")
	writeCommand(t, filepath.Join(ProjectRoot(projectDir), "review.md"), "review", "project review", "project")
	writeCommand(t, filepath.Join(ProjectRoot(projectDir), "git", "commit.md"), "", "commit message", "commit body")

	items, err := NewCommandManager(homeDir).List(context.Background(), "project-1", projectDir)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Command{}
	for _, item := range items {
		byName[item.Name] = item
	}
	if byName["review"].Scope != ScopeProject || byName["review"].Body != "project" {
		t.Fatalf("effective review = %#v", byName["review"])
	}
	if byName["git:commit"].Scope != ScopeProject {
		t.Fatalf("nested command = %#v", byName["git:commit"])
	}
	if byName["plan"].Scope != ScopeBuiltin ||
		byName["spec"].Scope != ScopeBuiltin ||
		byName["goal"].Scope != ScopeBuiltin {
		t.Fatalf("builtins missing: %#v", byName)
	}
}

func TestCreateAndUpdateCommand(t *testing.T) {
	homeDir := t.TempDir()
	manager := NewCommandManager(homeDir)
	created, err := manager.Create(context.Background(), CreateInput{
		Scope: ScopeGlobal, Name: "git:commit",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(created.Path) != "commit.md" {
		t.Fatalf("path = %q", created.Path)
	}
	updated, err := manager.Update(context.Background(), created.Ref, UpdateInput{
		Scope: ScopeGlobal, Name: "git:commit", Description: "生成提交信息", Body: "生成 Conventional Commit。",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Description != "生成提交信息" || updated.Body != "生成 Conventional Commit。" {
		t.Fatalf("updated = %#v", updated)
	}
	if updated.Path != filepath.Join(GlobalRoot(homeDir), "git", "commit.md") {
		t.Fatalf("updated path = %q", updated.Path)
	}
}

func TestUpdateRejectsDuplicateRenamedCommand(t *testing.T) {
	homeDir := t.TempDir()
	manager := NewCommandManager(homeDir)
	first, err := manager.Create(context.Background(), CreateInput{Scope: ScopeGlobal, Name: "first"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Create(context.Background(), CreateInput{Scope: ScopeGlobal, Name: "second"}, ""); err != nil {
		t.Fatal(err)
	}
	_, err = manager.Update(context.Background(), first.Ref, UpdateInput{
		Scope: ScopeGlobal, Name: "second", Body: "duplicate",
	}, "")
	if !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("update error = %v, want ErrDuplicateName", err)
	}
}

func TestScanLimitsNestedDirectories(t *testing.T) {
	homeDir := t.TempDir()
	root := GlobalRoot(homeDir)
	writeCommand(t, filepath.Join(root, "a", "b", "c", "ok.md"), "", "ok", "body")
	writeCommand(t, filepath.Join(root, "a", "b", "c", "d", "ignored.md"), "", "ignored", "body")

	items, err := NewCommandManager(homeDir).ListScope(context.Background(), ScopeGlobal, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "a:b:c:ok" {
		t.Fatalf("items = %#v", items)
	}
}

func TestExpand(t *testing.T) {
	if got := Expand("审查：$ARGUMENTS", "当前 PR"); got != "审查：当前 PR" {
		t.Fatalf("expanded = %q", got)
	}
	if got := Expand("Review", "current PR"); got != "Review\n\nAdditional user arguments:\ncurrent PR" {
		t.Fatalf("appended = %q", got)
	}
}

func writeCommand(t *testing.T, path, name, description, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(render(name, description, body)), 0o600); err != nil {
		t.Fatal(err)
	}
}
