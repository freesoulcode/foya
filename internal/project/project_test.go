package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagerPersistsProjectsWithSharedFolders(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	projectDir := filepath.Join(root, "work", "foya")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.Create(projectDir, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Create(filepath.Join(projectDir, "."), "代码项目")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatalf("projects sharing a folder must remain distinct: first=%#v second=%#v", first, second)
	}
	if first.Name != "新项目" || first.Path == "" ||
		second.Name != "代码项目" || second.Path != first.Path {
		t.Fatalf("unexpected first project: %#v", first)
	}
	renamed := "设计项目"
	pinned := true
	first, err = manager.Update(first.ID, &renamed, &pinned)
	if err != nil {
		t.Fatal(err)
	}
	if first.Name != renamed || !first.Pinned || first.PinnedAt == nil {
		t.Fatalf("project update was not applied: %#v", first)
	}
	reloaded, err := NewManager(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	items := reloaded.List()
	if len(items) != 2 || items[0].ID != first.ID || items[1].ID != second.ID ||
		!items[0].Available || items[0].Name != renamed ||
		!items[0].Pinned || items[0].PinnedAt == nil {
		t.Fatalf("unexpected persisted projects: %#v", items)
	}
	if err := reloaded.Delete(first.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Get(first.ID); ok {
		t.Fatal("deleted project remains in manager")
	}
	again, err := NewManager(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if items := again.List(); len(items) != 1 || items[0].ID != second.ID {
		t.Fatalf("unexpected projects after delete: %#v", items)
	}
	if _, err := os.Stat(projectDir); err != nil {
		t.Fatalf("delete removed project directory: %v", err)
	}
}

func TestManagerRejectsMissingDirectory(t *testing.T) {
	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Create(filepath.Join(t.TempDir(), "missing"), "Missing"); err == nil {
		t.Fatal("expected missing project directory to be rejected")
	}
	if _, err := manager.Create("", "Missing"); err == nil {
		t.Fatal("expected empty project directory to be rejected")
	}
}
