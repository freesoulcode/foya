package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const registryBaseURL = "https://registry.modelcontextprotocol.io/v0.1/servers"

type RegistryServer struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Version     string       `json:"version,omitempty"`
	Installable bool         `json:"installable"`
	Reason      string       `json:"reason,omitempty"`
	Config      ServerConfig `json:"config"`
}

type registryArgument struct {
	Name       string `json:"name"`
	Value      string `json:"value"`
	Default    string `json:"default"`
	IsRequired bool   `json:"isRequired"`
}

type registryPackage struct {
	RegistryType         string             `json:"registryType"`
	Identifier           string             `json:"identifier"`
	Version              string             `json:"version"`
	RuntimeHint          string             `json:"runtimeHint"`
	RuntimeArguments     []registryArgument `json:"runtimeArguments"`
	PackageArguments     []registryArgument `json:"packageArguments"`
	EnvironmentVariables []registryArgument `json:"environmentVariables"`
	Transport            struct {
		Type string `json:"type"`
	} `json:"transport"`
}

type registryRemote struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

type registryEntry struct {
	Server struct {
		Name        string            `json:"name"`
		Title       string            `json:"title"`
		Description string            `json:"description"`
		Version     string            `json:"version"`
		Packages    []registryPackage `json:"packages"`
		Remotes     []registryRemote  `json:"remotes"`
	} `json:"server"`
}

func SearchRegistry(ctx context.Context, query string) ([]RegistryServer, error) {
	location, _ := url.Parse(registryBaseURL)
	values := location.Query()
	values.Set("limit", "40")
	if query = strings.TrimSpace(query); query != "" {
		values.Set("search", query)
	}
	location.RawQuery = values.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location.String(), nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("query MCP registry: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("MCP registry returned HTTP %d", response.StatusCode)
	}
	var payload struct {
		Servers []registryEntry `json:"servers"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode MCP registry: %w", err)
	}

	byID := make(map[string]RegistryServer)
	order := make([]string, 0, len(payload.Servers))
	for _, entry := range payload.Servers {
		item := registryInstall(entry)
		if item.ID == "" {
			continue
		}
		if _, exists := byID[item.ID]; !exists {
			order = append(order, item.ID)
		}
		byID[item.ID] = item
	}
	out := make([]RegistryServer, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out, nil
}

func registryInstall(entry registryEntry) RegistryServer {
	server := entry.Server
	title := strings.TrimSpace(server.Title)
	if title == "" {
		title = server.Name
	}
	result := RegistryServer{
		ID: server.Name, Name: title, Description: server.Description, Version: server.Version,
		Config: ServerConfig{ID: server.Name, Name: title, Enabled: true},
	}
	for _, remote := range server.Remotes {
		transport := strings.ReplaceAll(remote.Type, "-", "_")
		if (transport == "streamable_http" || transport == "sse") && remote.URL != "" {
			result.Installable = true
			result.Config.Transport = transport
			result.Config.URL = remote.URL
			return result
		}
	}
	for _, item := range server.Packages {
		if item.RegistryType != "npm" || item.Transport.Type != "stdio" {
			continue
		}
		command := item.RuntimeHint
		if command == "" {
			command = "npx"
		}
		args, ok := registryArguments(item.RuntimeArguments)
		if !ok {
			continue
		}
		identifier := item.Identifier
		version := item.Version
		if version == "" {
			version = server.Version
		}
		if version != "" && !strings.HasSuffix(identifier, "@"+version) {
			identifier += "@" + version
		}
		args = append(args, identifier)
		packageArgs, ok := registryArguments(item.PackageArguments)
		if !ok {
			continue
		}
		args = append(args, packageArgs...)
		env := make(map[string]string)
		ready := true
		for _, variable := range item.EnvironmentVariables {
			value := variable.Value
			if value == "" {
				value = variable.Default
			}
			if variable.IsRequired && value == "" {
				ready = false
			}
			if value != "" {
				env[variable.Name] = value
			}
		}
		result.Installable = ready
		if !ready {
			result.Reason = "Additional configuration required"
		}
		result.Config.Transport = "stdio"
		result.Config.Command = command
		result.Config.Args = args
		result.Config.Env = env
		return result
	}
	result.Reason = "No compatible installation method"
	return result
}

func registryArguments(items []registryArgument) ([]string, bool) {
	out := make([]string, 0, len(items))
	for _, item := range items {
		value := item.Value
		if value == "" {
			value = item.Default
		}
		if item.IsRequired && (value == "" || strings.Contains(value, "{")) {
			return nil, false
		}
		if value == "" {
			continue
		}
		if item.Name != "" {
			out = append(out, item.Name)
		}
		out = append(out, value)
	}
	return out, true
}
