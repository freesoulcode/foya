package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMarketplaceBrowseAndInstallSubdirectory(t *testing.T) {
	repository := t.TempDir()
	writeFile(t, filepath.Join(repository, "plugins", "hello", "plugin.json"), `{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
  "name": "market-hello",
  "version": "1.0.0"
}`)
	writeFile(t, filepath.Join(repository, "plugins", "hello", "skills", "hello", "SKILL.md"), `---
name: hello
description: Marketplace installation test.
---
Hello.`)
	writeFile(t, filepath.Join(repository, "plugins", "hello", "agents", "reviewer.md"), "review")
	writeFile(t, filepath.Join(repository, ".agents", "plugins", "marketplace.json"), `{
  "name": "test-market",
  "owner": {"name": "Test"},
  "metadata": {"pluginRoot": "./plugins"},
  "plugins": [{
    "name": "market-hello",
    "description": "Test plugin",
    "version": "1.0.0",
    "source": "./hello"
  }]
}`)
	runGit(t, repository, "init", "-b", "main")
	runGit(t, repository, "add", ".")
	runGit(t, repository, "-c", "user.name=Foya Test", "-c", "user.email=test@example.com", "commit", "-m", "initial")

	dataDir := t.TempDir()
	homeDir := t.TempDir()
	manager, err := NewManager(dataDir, homeDir)
	if err != nil {
		t.Fatal(err)
	}
	market, err := manager.AddMarketplace(
		context.Background(),
		MarketplaceRegistrationInput{Source: repository},
	)
	if err != nil {
		t.Fatal(err)
	}
	if market.ID != "test-market" || market.Format != "codex" || !market.Enabled {
		t.Fatalf("unexpected marketplace: %#v", market)
	}

	catalog, err := manager.BrowseMarketplace(context.Background(), "test-market")
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Plugins) != 1 || !catalog.Plugins[0].Installable || catalog.Plugins[0].Installed {
		t.Fatalf("unexpected catalog: %#v", catalog)
	}
	preview, err := manager.PreviewMarketplacePlugin(
		context.Background(),
		"test-market",
		"market-hello",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Valid || preview.Compatibility != "partial" ||
		preview.SkillCount != 1 || preview.MCPServerCount != 0 ||
		len(preview.UnsupportedComponents) != 1 ||
		preview.UnsupportedComponents[0] != "agents" {
		t.Fatalf("unexpected preview: %#v", preview)
	}

	item, err := manager.InstallMarketplace(context.Background(), "test-market", "market-hello", false)
	if err != nil {
		t.Fatal(err)
	}
	if item.Name != "market-hello" || item.SkillCount != 1 ||
		item.Source != "marketplace:test-market:market-hello" {
		t.Fatalf("unexpected installed plugin: %#v", item)
	}

	catalog, err = manager.BrowseMarketplace(context.Background(), "test-market")
	if err != nil {
		t.Fatal(err)
	}
	if !catalog.Plugins[0].Installed || catalog.Plugins[0].InstalledVersion != "1.0.0" {
		t.Fatalf("installed state missing from catalog: %#v", catalog.Plugins[0])
	}
	reloaded, err := NewManager(dataDir, homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if items := reloaded.Marketplaces(); len(items) != 1 || items[0].ID != "test-market" {
		t.Fatalf("marketplace registration was not persisted: %#v", items)
	}
	if err := reloaded.SetMarketplaceEnabled("test-market", false); err != nil {
		t.Fatal(err)
	}
	if _, err := reloaded.BrowseMarketplace(context.Background(), "test-market"); !errors.Is(err, ErrMarketplaceDisabled) {
		t.Fatalf("disabled marketplace should not be browsable: %v", err)
	}
	if err := reloaded.SetMarketplaceEnabled("test-market", true); err != nil {
		t.Fatal(err)
	}
	if _, err := reloaded.RefreshMarketplace(context.Background(), "test-market"); err != nil {
		t.Fatal(err)
	}
	if err := reloaded.RemoveMarketplace("test-market"); err != nil {
		t.Fatal(err)
	}
	if items := reloaded.Marketplaces(); len(items) != 0 {
		t.Fatalf("marketplace removal failed: %#v", items)
	}
	if installed, err := reloaded.List(); err != nil || len(installed) != 1 {
		t.Fatalf("removing a marketplace should retain installed plugins: %#v, %v", installed, err)
	}
}

func TestMarketplaceSourceValidation(t *testing.T) {
	definition := marketplaceDefinition{
		Repository: "github/awesome-copilot",
	}
	source, err := resolveMarketplaceSource(
		definition,
		json.RawMessage(`{"source":"github","repo":"acme/tools","path":"plugins/lint","sha":"0123456789012345678901234567890123456789"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if source.repository != "acme/tools" || source.path != "plugins/lint" ||
		source.revision != "0123456789012345678901234567890123456789" {
		t.Fatalf("unexpected source: %#v", source)
	}
	if _, err := resolveMarketplaceSource(definition, json.RawMessage(`"../outside"`)); err == nil {
		t.Fatal("escaping marketplace path should be rejected")
	}
	if _, err := resolveMarketplaceSource(
		definition,
		json.RawMessage(`{"source":"url","url":"http://example.com/plugin.git"}`),
	); err == nil {
		t.Fatal("insecure marketplace source should be rejected")
	}
	if kind, location, _, err := normalizeSource("git@github.com:acme/plugins.git"); err != nil ||
		kind != "git" || location != "git@github.com:acme/plugins.git" {
		t.Fatalf("Git SSH source was not accepted: kind=%q location=%q err=%v", kind, location, err)
	}
}

func TestCloneMarketplaceRepositoryUsesSparsePaths(t *testing.T) {
	repository := t.TempDir()
	writeFile(t, filepath.Join(repository, ".agents", "plugins", "marketplace.json"), `{
  "name": "sparse-market",
  "owner": {"name": "Test"},
  "plugins": []
}`)
	writeFile(t, filepath.Join(repository, "plugins", "wanted", "plugin.json"), `{}`)
	writeFile(t, filepath.Join(repository, "plugins", "ignored", "large.txt"), "ignored")
	runGit(t, repository, "init", "-b", "main")
	runGit(t, repository, "add", ".")
	runGit(t, repository, "-c", "user.name=Foya Test", "-c", "user.email=test@example.com", "commit", "-m", "initial")

	destination := filepath.Join(t.TempDir(), "checkout")
	if err := cloneMarketplaceRepository(
		context.Background(),
		repository,
		destination,
		"main",
		[]string{"plugins/wanted"},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, ".agents", "plugins", "marketplace.json")); err != nil {
		t.Fatalf("marketplace manifest missing from sparse checkout: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "plugins", "wanted", "plugin.json")); err != nil {
		t.Fatalf("requested sparse path missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "plugins", "ignored", "large.txt")); !os.IsNotExist(err) {
		t.Fatalf("unrequested sparse path should be absent: %v", err)
	}
}

func runGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
