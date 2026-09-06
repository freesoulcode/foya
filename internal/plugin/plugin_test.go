package plugin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectAgentPluginComponents(t *testing.T) {
	root := t.TempDir()
	dataRoot := filepath.Join(t.TempDir(), "plugin-data")
	writeFile(t, filepath.Join(root, "plugin.json"), `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
  "name": "release-tools",
  "version": "1.2.0",
  "unknown": true
}`)
	writeFile(t, filepath.Join(root, "skills", "release-notes", "SKILL.md"), `---
name: release-notes
description: Generate release notes.
---
Generate release notes.`)
	writeFile(t, filepath.Join(root, "skills", "nested", "child", "SKILL.md"), `---
name: ignored
description: Nested skills are not discovered.
---
Ignored.`)
	writeFile(t, filepath.Join(root, "mcp.json"), `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
  "mcpServers": {
    "local": {
      "type": "stdio",
      "command": "node",
      "args": ["${PLUGIN_ROOT}/server.js", "--data", "${PLUGIN_DATA}"],
      "env": {"CONFIG": "${PLUGIN_ROOT}/config.json"}
    },
    "remote": {
      "type": "streamable-http",
      "url": "https://example.com/mcp",
      "headers": {"X-Tenant": "public"}
    },
    "broken": {
      "type": "stdio",
      "command": "node",
      "extra": true
    }
  }
}`)

	item := inspectDirectory(root, dataRoot)
	if !item.Valid || item.Name != "release-tools" || item.SkillCount != 1 || item.MCPServerCount != 2 {
		t.Fatalf("unexpected plugin inspection: %#v", item)
	}
	if !hasDiagnostic(item.Diagnostics, "unknown_field", "warning") ||
		!hasDiagnostic(item.Diagnostics, "invalid_mcp_server", "warning") {
		t.Fatalf("expected isolated warnings: %#v", item.Diagnostics)
	}
	servers, diagnostics := inspectMCP(root, item.DataPath, item.Name)
	if len(diagnostics) != 1 || len(servers) != 2 {
		t.Fatalf("unexpected MCP inspection: servers=%#v diagnostics=%#v", servers, diagnostics)
	}
	local := servers[0]
	if local.ID != "plugin:release-tools:local" || local.PluginID != "release-tools" ||
		local.Env["PLUGIN_ROOT"] != root || local.Env["PLUGIN_DATA"] != item.DataPath ||
		!strings.HasPrefix(local.Args[0], root) {
		t.Fatalf("plugin variables were not applied: %#v", local)
	}
}

func TestManifestFatalAndNonFatalValidation(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "plugin.json"), `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
  "name": "valid-name",
  "extensions": "ignored",
  "future": 1
}`)
	item := inspectDirectory(root, filepath.Join(t.TempDir(), "data"))
	if !item.Valid {
		t.Fatalf("non-fatal fields rejected plugin: %#v", item.Diagnostics)
	}
	if !hasDiagnostic(item.Diagnostics, "invalid_extensions", "warning") ||
		!hasDiagnostic(item.Diagnostics, "unknown_field", "warning") {
		t.Fatalf("missing non-fatal diagnostics: %#v", item.Diagnostics)
	}

	writeFile(t, filepath.Join(root, "plugin.json"), `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
  "name": "Invalid Name"
}`)
	item = inspectDirectory(root, filepath.Join(t.TempDir(), "data"))
	if item.Valid || !hasDiagnostic(item.Diagnostics, "invalid_name", "error") {
		t.Fatalf("invalid manifest was accepted: %#v", item)
	}
}

func TestManagerInstallEnableAndRemove(t *testing.T) {
	source := t.TempDir()
	writeFile(t, filepath.Join(source, "plugin.json"), `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
  "name": "sample-plugin",
  "version": "1.0.0"
}`)
	writeFile(t, filepath.Join(source, "skills", "sample", "SKILL.md"), `---
name: sample
description: Sample skill.
---
Sample.`)

	manager, err := NewManager(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	item, err := manager.Install(context.Background(), source, false)
	if err != nil {
		t.Fatal(err)
	}
	if !item.Enabled || item.SkillCount != 1 || len(manager.SkillRoots()) != 1 {
		t.Fatalf("installed plugin is not active: %#v", item)
	}
	if err := manager.SetEnabled(item.Name, false); err != nil {
		t.Fatal(err)
	}
	items, err := manager.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Enabled || len(manager.SkillRoots()) != 0 {
		t.Fatalf("plugin disablement failed: %#v", items)
	}
	if err := os.MkdirAll(item.DataPath, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(item.DataPath, "state.json"), `{}`)
	if err := manager.Remove(item.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(item.Path); !os.IsNotExist(err) {
		t.Fatalf("plugin package was not removed: %v", err)
	}
	if _, err := os.Stat(item.DataPath); err != nil {
		t.Fatalf("PLUGIN_DATA should survive uninstall: %v", err)
	}
}

func TestPluginMCPRejectsInsecureRemoteAndEscapingCWD(t *testing.T) {
	root := t.TempDir()
	dataRoot := filepath.Join(t.TempDir(), "data")
	writeFile(t, filepath.Join(root, "mcp.json"), `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
  "mcpServers": {
    "remote": {"type": "streamable-http", "url": "http://example.com/mcp"},
    "escape": {"type": "stdio", "command": "node", "cwd": "./../outside"},
    "loopback": {"type": "streamable-http", "url": "http://127.0.0.1:3000/mcp"}
  }
}`)
	servers, diagnostics := inspectMCP(root, dataRoot, "security")
	if len(servers) != 1 || servers[0].Name != "security/loopback" {
		t.Fatalf("unexpected valid servers: %#v", servers)
	}
	if len(diagnostics) != 2 {
		t.Fatalf("expected two isolated MCP failures: %#v", diagnostics)
	}
}

func TestInstallRejectsSymlinkedPackageContent(t *testing.T) {
	source := t.TempDir()
	writeFile(t, filepath.Join(source, "plugin.json"), `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
  "name": "linked-plugin"
}`)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	writeFile(t, outside, "secret")
	if err := os.Symlink(outside, filepath.Join(source, "secret.txt")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	manager, err := NewManager(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Install(context.Background(), source, false); err == nil {
		t.Fatal("plugin containing a symlink should be rejected")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func hasDiagnostic(items []Diagnostic, code, severity string) bool {
	for _, item := range items {
		if item.Code == code && item.Severity == severity {
			return true
		}
	}
	return false
}
