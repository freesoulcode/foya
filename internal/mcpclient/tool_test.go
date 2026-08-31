package mcpclient

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPResultPartsPreserveImageBytes(t *testing.T) {
	imageData := []byte{0x89, 'P', 'N', 'G'}
	parts := mcpResultParts(&mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "captured"},
			&mcp.ImageContent{MIMEType: "image/png", Data: imageData},
		},
	}, "screenshot")
	if len(parts) != 2 || parts[0].Type != "text" || parts[0].Text != "captured" {
		t.Fatalf("unexpected MCP content parts: %#v", parts)
	}
	image := parts[1]
	if image.Type != "image" || image.Name != "screenshot-image" ||
		image.MediaType != "image/png" || !bytes.Equal(image.Data, imageData) {
		t.Fatalf("MCP image part = %#v", image)
	}
	imageData[0] = 0
	if image.Data[0] != 0x89 {
		t.Fatal("MCP image bytes were not copied")
	}
}

func TestMCPToolNameIsStableAndCollisionResistant(t *testing.T) {
	prefix := strings.Repeat("a", 80)
	first := mcpToolName("server", prefix+"-first")
	second := mcpToolName("server", prefix+"-second")

	if len(first) > 64 || len(second) > 64 {
		t.Fatalf("tool names exceed provider limit: %d, %d", len(first), len(second))
	}
	if first == second {
		t.Fatalf("long tool names collided: %q", first)
	}
	if first != mcpToolName("server", prefix+"-first") {
		t.Fatal("tool name is not stable")
	}
}

func TestMinimalEnvironmentFiltersParentSecrets(t *testing.T) {
	t.Setenv("FOYA_TEST_SECRET", "must-not-leak")
	t.Setenv("HOME", "/tmp/foya-home")

	environment := minimalEnvironment(map[string]string{
		"EXPLICIT_TOKEN": "${FOYA_TEST_SECRET}",
	})
	joined := strings.Join(environment, "\n")
	if strings.Contains(joined, "FOYA_TEST_SECRET=") {
		t.Fatal("inherited secret leaked into MCP process environment")
	}
	if !strings.Contains(joined, "HOME=/tmp/foya-home") {
		t.Fatal("expected safe HOME variable")
	}
	if !strings.Contains(joined, "EXPLICIT_TOKEN=must-not-leak") {
		t.Fatal("explicit environment variable was not expanded")
	}
}

func TestPublicConfigAlwaysExposesServersArray(t *testing.T) {
	config := publicConfig(Config{Version: 1})
	if config.Servers == nil {
		t.Fatal("servers must be an empty array, not nil")
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"servers":[]`) {
		t.Fatalf("servers array missing from JSON: %s", data)
	}
}

func TestManagerMigratesMCPConfigAndSeparatesCredentials(t *testing.T) {
	dataDir := t.TempDir()
	homeDir := t.TempDir()
	legacy := `{"version":1,"servers":[{"id":"remote","name":"Remote","enabled":true,"transport":"streamable_http","url":"https://example.com/mcp","bearer_token":"secret"}]}`
	if err := os.WriteFile(filepath.Join(dataDir, "mcp.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	manager, err := NewManager(dataDir, homeDir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	config := manager.Config()
	if len(config.Servers) != 1 || !config.Servers[0].HasToken ||
		config.Servers[0].BearerToken != "" {
		t.Fatalf("unexpected public config: %#v", config)
	}

	configPath := filepath.Join(homeDir, ".foya", "mcp.json")
	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configData), `"mcpServers"`) ||
		strings.Contains(string(configData), "secret") {
		t.Fatalf("unexpected migrated config: %s", configData)
	}
	credentialData, err := os.ReadFile(filepath.Join(homeDir, ".foya", "mcp-credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(credentialData), "secret") {
		t.Fatalf("credential was not migrated: %s", credentialData)
	}
	if mode := fileMode(t, configPath); mode != 0o600 {
		t.Fatalf("config mode = %o, want 600", mode)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "mcp.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy config was not removed: %v", err)
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}
