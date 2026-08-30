package mcpclient

import "testing"

func TestRegistryInstallRemote(t *testing.T) {
	var entry registryEntry
	entry.Server.Name = "io.example/weather"
	entry.Server.Title = "Weather"
	entry.Server.Version = "1.2.3"
	entry.Server.Remotes = []registryRemote{{
		Type: "streamable-http",
		URL:  "https://example.com/mcp",
	}}

	item := registryInstall(entry)
	if !item.Installable || item.Config.Transport != "streamable_http" ||
		item.Config.URL != "https://example.com/mcp" {
		t.Fatalf("unexpected remote install: %#v", item)
	}
}

func TestRegistryInstallNPM(t *testing.T) {
	var entry registryEntry
	entry.Server.Name = "io.example/files"
	entry.Server.Version = "2.0.0"
	entry.Server.Packages = []registryPackage{{
		RegistryType: "npm",
		Identifier:   "@example/files",
		RuntimeHint:  "npx",
		Transport: struct {
			Type string `json:"type"`
		}{Type: "stdio"},
		RuntimeArguments: []registryArgument{{Value: "-y"}},
	}}

	item := registryInstall(entry)
	if !item.Installable || item.Config.Command != "npx" ||
		len(item.Config.Args) != 2 ||
		item.Config.Args[0] != "-y" ||
		item.Config.Args[1] != "@example/files@2.0.0" {
		t.Fatalf("unexpected npm install: %#v", item)
	}
}

func TestRegistryInstallRequiresConfiguration(t *testing.T) {
	var entry registryEntry
	entry.Server.Name = "io.example/private"
	entry.Server.Packages = []registryPackage{{
		RegistryType: "npm",
		Identifier:   "private-mcp",
		Transport: struct {
			Type string `json:"type"`
		}{Type: "stdio"},
		EnvironmentVariables: []registryArgument{{
			Name: "API_KEY", IsRequired: true,
		}},
	}}

	item := registryInstall(entry)
	if item.Installable || item.Reason == "" {
		t.Fatalf("server requiring configuration must not be one-click installable: %#v", item)
	}
}
