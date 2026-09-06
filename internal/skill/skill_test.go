package skill

import (
	"context"
	"encoding/json"
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
	item, diagnostics, err := parseFile(path, ScopeGlobal)
	if err != nil {
		t.Fatal(err)
	}
	if item.Name != "plain" || item.Body != "# Plain\nDo the work." {
		t.Fatalf("unexpected skill: %#v", item)
	}
	if len(diagnostics) == 0 || diagnostics[0].Code != "missing_frontmatter" {
		t.Fatalf("expected missing frontmatter diagnostic, got %#v", diagnostics)
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

func TestNestedMetadataIsAccepted(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	writeSkill(t, filepath.Join(home, ".agents", "skills", "nested", "SKILL.md"), `---
name: nested
description: nested metadata
metadata:
  requires:
    bins: ["example-cli"]
  enabled: true
---
body`)
	manager, err := NewManager(filepath.Join(root, "data"), home, nil)
	if err != nil {
		t.Fatal(err)
	}
	items, err := manager.List(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "nested" {
		t.Fatalf("unexpected skills: %#v", items)
	}
	encoded, err := json.Marshal(items[0].Manifest.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"bins":["example-cli"]`) {
		t.Fatalf("nested metadata was not preserved: %s", encoded)
	}
}

func TestPluginSkillsUseImmediateChildrenAndNamespacedRefs(t *testing.T) {
	root := t.TempDir()
	pluginSkills := filepath.Join(root, "plugins", "release-tools", "skills")
	writeSkill(t, filepath.Join(pluginSkills, "release", "SKILL.md"), `---
name: release
description: Create a release.
---
release`)
	writeSkill(t, filepath.Join(pluginSkills, "release", "references", "checklist.md"), "checklist")
	writeSkill(t, filepath.Join(pluginSkills, "nested", "child", "SKILL.md"), `---
name: ignored
description: Nested plugin skill.
---
ignored`)

	manager, err := NewManager(filepath.Join(root, "data"), filepath.Join(root, "home"), nil)
	if err != nil {
		t.Fatal(err)
	}
	manager.SetPluginRoots(func() []PluginRoot {
		return []PluginRoot{{PluginID: "release-tools", Path: pluginSkills}}
	})
	items, err := manager.List(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Ref != "plugin:release-tools:release" ||
		items[0].Scope != ScopePlugin {
		t.Fatalf("unexpected plugin skills: %#v", items)
	}
	resource, err := manager.ReadResource(
		context.Background(), "", "", items[0].Ref, "references/checklist.md",
	)
	if err != nil || resource.Content != "checklist" {
		t.Fatalf("plugin skill resource was not readable: %#v, %v", resource, err)
	}
}

func TestPluginSkillCanBeResolvedByNamespacedRef(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first", "skills")
	second := filepath.Join(root, "second", "skills")
	writeSkill(t, filepath.Join(first, "shared", "SKILL.md"), `---
name: shared
description: First.
---
first`)
	writeSkill(t, filepath.Join(second, "shared", "SKILL.md"), `---
name: shared
description: Second.
---
second`)
	manager, err := NewManager(filepath.Join(root, "data"), filepath.Join(root, "home"), nil)
	if err != nil {
		t.Fatal(err)
	}
	manager.SetPluginRoots(func() []PluginRoot {
		return []PluginRoot{
			{PluginID: "first", Path: first},
			{PluginID: "second", Path: second},
		}
	})
	item, err := manager.Get(context.Background(), "", "", "plugin:second:shared")
	if err != nil {
		t.Fatal(err)
	}
	if item.Body != "second" {
		t.Fatalf("resolved plugin skill body = %q", item.Body)
	}
}

func TestSymlinkedSkillPackageIsDiscovered(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	target := filepath.Join(root, "installed", "linked")
	writeSkill(t, filepath.Join(target, "SKILL.md"), `---
name: linked
description: symlinked package
---
body`)
	writeSkill(t, filepath.Join(target, "references", "details.md"), "Details.")
	link := filepath.Join(home, ".agents", "skills", "linked")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	manager, err := NewManager(filepath.Join(root, "data"), home, nil)
	if err != nil {
		t.Fatal(err)
	}
	items, err := manager.List(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "linked" || len(items[0].Resources) != 1 {
		t.Fatalf("unexpected symlinked skill: %#v", items)
	}
	resource, err := manager.ReadResource(
		context.Background(), "", "", "linked", "references/details.md",
	)
	if err != nil {
		t.Fatal(err)
	}
	if resource.Content != "Details." {
		t.Fatalf("resource content = %q", resource.Content)
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

func TestReadResourceStaysInsideSkillPackage(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	data := filepath.Join(root, "data")
	writeSkill(t, filepath.Join(home, ".foya", "skills", "pkg", "SKILL.md"), `---
name: pkg
description: packaged skill
required-tools:
  - read
required-capabilities:
  - filesystem
---
Read references/details.md`)
	writeSkill(t, filepath.Join(home, ".foya", "skills", "pkg", "references", "details.md"), "Details.")
	writeSkill(t, filepath.Join(home, ".foya", "outside.md"), "outside")

	manager, err := NewManager(data, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	items, err := manager.List(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].Resources) != 1 ||
		items[0].RequiredTools[0] != "read" ||
		items[0].RequiredCapabilities[0] != "filesystem" {
		t.Fatalf("unexpected skill package metadata: %#v", items)
	}
	resource, err := manager.ReadResource(context.Background(), "", "", "pkg", "references/details.md")
	if err != nil {
		t.Fatal(err)
	}
	if resource.Content != "Details." {
		t.Fatalf("resource content = %q", resource.Content)
	}
	if _, err := manager.ReadResource(context.Background(), "", "", "pkg", "../outside.md"); err == nil {
		t.Fatal("expected path traversal to be rejected")
	}
}

func TestInvalidSkillDoesNotBreakDiscovery(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	writeSkill(t, filepath.Join(home, ".foya", "skills", "good", "SKILL.md"), `---
name: good
description: good skill
---
body`)
	writeSkill(t, filepath.Join(home, ".foya", "skills", "bad", "SKILL.md"), `---
name: bad
description: broken
`)
	manager, err := NewManager(filepath.Join(root, "data"), home, nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := manager.Inspect(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Skills) != 1 || report.Skills[0].Name != "good" {
		t.Fatalf("unexpected valid skills: %#v", report.Skills)
	}
	if len(report.Rejected) != 1 || len(report.Diagnostics) == 0 {
		t.Fatalf("expected rejected diagnostics, got %#v %#v", report.Rejected, report.Diagnostics)
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
