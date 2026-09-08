package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/freesoulcode/foya/internal/mcpclient"
)

func inspectDirectory(root, dataRoot string) Plugin {
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if absolute, err := filepath.Abs(dataRoot); err == nil {
		dataRoot = absolute
	}
	fallback := filepath.Base(root)
	item := Plugin{
		Manifest: Manifest{Name: fallback},
		Path:     root,
		DataPath: filepath.Join(dataRoot, fallback),
	}
	manifest, diagnostics := loadManifest(root)
	item.Manifest = manifest
	if item.Name == "" {
		item.Name = fallback
	}
	item.DataPath = filepath.Join(dataRoot, item.Name)
	item.Diagnostics = append(item.Diagnostics, diagnostics...)
	if hasErrors(diagnostics) {
		return item
	}
	item.Valid = true
	item.SkillCount, diagnostics = inspectSkills(root)
	item.Diagnostics = append(item.Diagnostics, diagnostics...)
	servers, diagnostics := inspectMCP(root, item.DataPath, item.Name)
	item.MCPServerCount = len(servers)
	item.Diagnostics = append(item.Diagnostics, diagnostics...)
	return item
}

func loadManifest(root string) (Manifest, []Diagnostic) {
	path := filepath.Join(root, "plugin.json")
	if !resolvedInside(root, path) {
		return Manifest{}, []Diagnostic{diag("manifest", path, "path_escape", "error", "plugin.json resolves outside the plugin root")}
	}
	data, err := readFileLimit(path, maxManifestBytes)
	if err != nil {
		return Manifest{}, []Diagnostic{diag("manifest", path, "manifest_unreadable", "error", err.Error())}
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return Manifest{}, []Diagnostic{diag("manifest", path, "manifest_invalid", "error", err.Error())}
	}
	if raw == nil {
		return Manifest{}, []Diagnostic{diag("manifest", path, "manifest_invalid", "error", "plugin.json must contain an object")}
	}
	allowed := map[string]bool{
		"$schema": true, "name": true, "version": true, "description": true,
		"author": true, "homepage": true, "repository": true, "license": true,
		"keywords": true, "extensions": true,
	}
	var diagnostics []Diagnostic
	for key := range raw {
		if !allowed[key] {
			diagnostics = append(diagnostics, diag("manifest", path, "unknown_field", "warning", "unknown field ignored: "+key))
			delete(raw, key)
		}
	}
	for _, field := range []string{
		"$schema", "name", "version", "description", "homepage", "repository", "license",
	} {
		if value, ok := raw[field]; ok && !jsonString(value) {
			diagnostics = append(diagnostics, diag("manifest", path, "invalid_field", "error", field+" must be a string"))
		}
	}
	if value, ok := raw["keywords"]; ok {
		var keywords []string
		if isJSONNull(value) || json.Unmarshal(value, &keywords) != nil {
			diagnostics = append(diagnostics, diag("manifest", path, "invalid_keywords", "error", "keywords must be an array of strings"))
		}
	}
	if value, ok := raw["extensions"]; ok && !jsonObject(value) {
		diagnostics = append(diagnostics, diag("manifest", path, "invalid_extensions", "warning", "non-object extensions field ignored"))
		delete(raw, "extensions")
	} else if ok {
		var extensions map[string]json.RawMessage
		_ = json.Unmarshal(value, &extensions)
		for namespace, extension := range extensions {
			if !jsonObject(extension) {
				diagnostics = append(diagnostics, diag("manifest", path, "invalid_extension", "error", "extension must be an object: "+namespace))
			}
		}
	}
	clean, _ := json.Marshal(raw)
	var manifest Manifest
	if err := json.Unmarshal(clean, &manifest); err != nil {
		return Manifest{}, append(diagnostics, diag("manifest", path, "manifest_invalid", "error", err.Error()))
	}
	if manifest.Schema != ManifestSchema {
		diagnostics = append(diagnostics, diag("manifest", path, "unsupported_schema", "error", "unsupported or missing $schema"))
	}
	if !validPluginName(manifest.Name) {
		diagnostics = append(diagnostics, diag("manifest", path, "invalid_name", "error", "name does not satisfy Agent Plugins 1.0"))
	}
	if rawAuthor, ok := raw["author"]; ok {
		var authorFields map[string]json.RawMessage
		if json.Unmarshal(rawAuthor, &authorFields) != nil || authorFields == nil {
			diagnostics = append(diagnostics, diag("manifest", path, "invalid_author", "error", "author must be an object"))
		} else {
			for key := range authorFields {
				if key != "name" && key != "email" && key != "url" {
					diagnostics = append(diagnostics, diag("manifest", path, "invalid_author", "error", "unknown author field: "+key))
				}
			}
			for key, value := range authorFields {
				if !jsonString(value) {
					diagnostics = append(diagnostics, diag("manifest", path, "invalid_author", "error", "author."+key+" must be a string"))
				}
			}
		}
	}
	return manifest, diagnostics
}

func inspectSkills(root string) (int, []Diagnostic) {
	skillsRoot := filepath.Join(root, "skills")
	info, err := os.Lstat(skillsRoot)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, []Diagnostic{diag("skills", skillsRoot, "skills_unreadable", "error", err.Error())}
	}
	if !info.IsDir() || !resolvedInside(root, skillsRoot) {
		return 0, []Diagnostic{diag("skills", skillsRoot, "invalid_skills_root", "error", "skills must be a directory inside the plugin root")}
	}
	entries, err := os.ReadDir(skillsRoot)
	if err != nil {
		return 0, []Diagnostic{diag("skills", skillsRoot, "skills_unreadable", "error", err.Error())}
	}
	count := 0
	var diagnostics []Diagnostic
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		mainPath := filepath.Join(skillsRoot, entry.Name(), "SKILL.md")
		mainInfo, err := os.Stat(mainPath)
		if err != nil || mainInfo.IsDir() {
			continue
		}
		if !resolvedInside(root, mainPath) {
			diagnostics = append(diagnostics, diag("skills", mainPath, "skill_path_escape", "warning", "skill skipped because SKILL.md resolves outside the plugin root"))
			continue
		}
		count++
	}
	return count, diagnostics
}

type mcpDocument struct {
	Schema     string                     `json:"$schema"`
	MCPServers map[string]json.RawMessage `json:"mcpServers"`
}

func inspectMCP(root, dataRoot, pluginID string) ([]mcpclient.ServerConfig, []Diagnostic) {
	path := filepath.Join(root, "mcp.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !info.Mode().IsRegular() || !resolvedInside(root, path) {
		return nil, []Diagnostic{diag("mcp", path, "invalid_mcp_document", "error", "mcp.json must be a regular file inside the plugin root")}
	}
	data, err := readFileLimit(path, maxMCPBytes)
	if err != nil {
		return nil, []Diagnostic{diag("mcp", path, "mcp_unreadable", "error", err.Error())}
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		return nil, []Diagnostic{diag("mcp", path, "mcp_invalid", "error", "mcp.json must contain a JSON object")}
	}
	if len(raw) != 2 || raw["$schema"] == nil || raw["mcpServers"] == nil {
		return nil, []Diagnostic{diag("mcp", path, "mcp_invalid", "error", "mcp.json may contain only $schema and mcpServers")}
	}
	var document mcpDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, []Diagnostic{diag("mcp", path, "mcp_invalid", "error", err.Error())}
	}
	if document.Schema != MCPSchema || document.MCPServers == nil {
		return nil, []Diagnostic{diag("mcp", path, "unsupported_mcp_schema", "error", "unsupported schema or missing mcpServers")}
	}
	names := make([]string, 0, len(document.MCPServers))
	for name := range document.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []mcpclient.ServerConfig
	var diagnostics []Diagnostic
	for _, name := range names {
		config, err := decodeMCPServer(document.MCPServers[name], root, dataRoot, pluginID, name)
		if err != nil {
			diagnostics = append(diagnostics, diag("mcp:"+name, path, "invalid_mcp_server", "warning", err.Error()))
			continue
		}
		out = append(out, config)
	}
	return out, diagnostics
}

func decodeMCPServer(raw json.RawMessage, root, dataRoot, pluginID, name string) (mcpclient.ServerConfig, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return mcpclient.ServerConfig{}, errors.New("server must be an object")
	}
	var transport string
	if err := json.Unmarshal(object["type"], &transport); err != nil || transport == "" {
		return mcpclient.ServerConfig{}, errors.New("server type is required")
	}
	config := mcpclient.ServerConfig{
		ID:            "plugin:" + pluginID + ":" + name,
		Name:          pluginID + "/" + name,
		Enabled:       true,
		PluginID:      pluginID,
		LiteralValues: true,
	}
	switch transport {
	case "stdio":
		if err := rejectUnknown(object, "type", "command", "args", "env", "cwd"); err != nil {
			return config, err
		}
		var command string
		if err := json.Unmarshal(object["command"], &command); err != nil || strings.TrimSpace(command) == "" {
			return config, errors.New("stdio command is required")
		}
		command = strings.TrimSpace(command)
		if strings.HasPrefix(command, "./") {
			resolved, err := packagePath(root, command)
			if err != nil {
				return config, fmt.Errorf("command: %w", err)
			}
			command = resolved
		} else if filepath.IsAbs(command) || strings.ContainsAny(command, `/\`) {
			return config, errors.New("command must be a bare executable or start with ./")
		}
		config.Transport = "stdio"
		config.Command = command
		config.Cwd = root
		if value := object["args"]; value != nil {
			if isJSONNull(value) || json.Unmarshal(value, &config.Args) != nil {
				return config, errors.New("args must be an array of strings")
			}
			for index := range config.Args {
				config.Args[index] = expandPluginVariables(config.Args[index], root, dataRoot)
			}
		}
		if value := object["env"]; value != nil {
			if isJSONNull(value) || json.Unmarshal(value, &config.Env) != nil {
				return config, errors.New("env must contain string values")
			}
		}
		if config.Env == nil {
			config.Env = make(map[string]string)
		}
		if _, ok := config.Env["PLUGIN_ROOT"]; ok {
			return config, errors.New("env cannot override PLUGIN_ROOT")
		}
		if _, ok := config.Env["PLUGIN_DATA"]; ok {
			return config, errors.New("env cannot override PLUGIN_DATA")
		}
		for key, value := range config.Env {
			config.Env[key] = expandPluginVariables(value, root, dataRoot)
		}
		config.Env["PLUGIN_ROOT"] = root
		config.Env["PLUGIN_DATA"] = dataRoot
		if value := object["cwd"]; value != nil {
			var cwd string
			if !jsonString(value) || json.Unmarshal(value, &cwd) != nil {
				return config, errors.New("cwd must be a string")
			}
			resolved, err := pluginWorkingDirectory(cwd, root, dataRoot)
			if err != nil {
				return config, err
			}
			config.Cwd = resolved
		}
	case "streamable-http", "sse":
		if err := rejectUnknown(object, "type", "url", "headers"); err != nil {
			return config, err
		}
		var endpoint string
		if err := json.Unmarshal(object["url"], &endpoint); err != nil {
			return config, errors.New("url is required")
		}
		if err := validateRemoteURL(endpoint); err != nil {
			return config, err
		}
		config.Transport = strings.ReplaceAll(transport, "-", "_")
		config.URL = endpoint
		if value := object["headers"]; value != nil {
			if isJSONNull(value) || json.Unmarshal(value, &config.Headers) != nil {
				return config, errors.New("headers must contain string values")
			}
		}
	default:
		return config, fmt.Errorf("unsupported transport %q", transport)
	}
	return config, nil
}

func pluginWorkingDirectory(value, root, dataRoot string) (string, error) {
	switch {
	case strings.HasPrefix(value, "./"):
		return packagePath(root, value)
	case value == "${PLUGIN_ROOT}" || strings.HasPrefix(value, "${PLUGIN_ROOT}/"):
		return rootedPath(root, strings.TrimPrefix(value, "${PLUGIN_ROOT}"))
	case value == "${PLUGIN_DATA}" || strings.HasPrefix(value, "${PLUGIN_DATA}/"):
		return rootedPath(dataRoot, strings.TrimPrefix(value, "${PLUGIN_DATA}"))
	default:
		return "", errors.New("cwd must start with ./, ${PLUGIN_ROOT}, or ${PLUGIN_DATA}")
	}
}

func packagePath(root, value string) (string, error) {
	if !strings.HasPrefix(value, "./") {
		return "", errors.New("plugin-relative path must start with ./")
	}
	return rootedPath(root, strings.TrimPrefix(value, "./"))
}

func rootedPath(root, relative string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(rootAbs, filepath.FromSlash(strings.TrimPrefix(relative, "/")))
	if !pathInside(rootAbs, candidate) {
		return "", errors.New("path escapes its permitted root")
	}
	if resolved, err := filepath.EvalSymlinks(candidate); err == nil && !pathInside(rootAbs, resolved) {
		return "", errors.New("path resolves outside its permitted root")
	}
	return candidate, nil
}

func expandPluginVariables(value, root, dataRoot string) string {
	value = strings.ReplaceAll(value, "${PLUGIN_ROOT}", root)
	return strings.ReplaceAll(value, "${PLUGIN_DATA}", dataRoot)
}

func validateRemoteURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("url must be an absolute HTTP URL without user info or fragment")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("url must use HTTP or HTTPS")
	}
	host := strings.Trim(parsed.Hostname(), "[]")
	loopback := strings.EqualFold(host, "localhost")
	if ip := net.ParseIP(host); ip != nil {
		loopback = ip.IsLoopback()
	}
	if !loopback && parsed.Scheme != "https" {
		return errors.New("non-loopback MCP endpoints must use HTTPS")
	}
	return nil
}
